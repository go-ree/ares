package jenkins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ares/internal/config"
)

// JenkinsManager jenkins管理器
type JenkinsManager struct {
}

// NewJenkinsManager 创建新的jenkins管理器
func NewJenkinsManager() *JenkinsManager {
	return &JenkinsManager{}
}

// CreateBuildTaskRequest 触发jenkins任务的请求
type CreateBuildTaskRequest struct {
	JobName string            `json:"job_name"`
	BuildId int               `json:"build_id"`
	Params  map[string]string `json:"params"`
}

type CreateBuildTaskResult struct {
	TaskResult *CreateBuildTaskRequest `json:"task_result"`
	Error      string                  `json:"error"`
	Success    bool                    `json:"success"`
}

type BuildLogQuery struct {
	JobName string `form:"job_name" json:"job_name" query:"job_name"`
	BuildId int64  `form:"build_id" json:"build_id" query:"build_id"`
	// Start 可选：从 Jenkins progressiveText 的 offset 开始拉取
	Start int64 `form:"start" json:"start" query:"start"`
}

// BuildLogChunk 流式日志块（用于 SSE id/断线续传）
type BuildLogChunk struct {
	Lines     []string
	NextStart int64
	IsPing    bool
}

// GetJenkinsNodeStatus	获取jenkins中node的状态信息
func (jm *JenkinsManager) GetJenkinsNodeStatus() (*Nodes, error) {
	ctx := context.Background()
	if Jenkins == nil {
		return nil, errors.New("jenkins not initialized")
	}
	nodes, err := Jenkins.GetAllNodes(ctx)
	if err != nil {
		slog.Error("Failed to get all nodes", slog.Any("error", err))
		return nil, err
	}
	var nodeInfo Nodes
	for _, node := range nodes {
		poll, err := node.Poll(ctx)
		if err != nil {
			slog.Error("Failed to poll node", slog.Any("error", err))
			return nil, err
		}
		slog.Debug("获取node信息，poll的值为：", slog.Any("poll", poll))
		slog.Debug("当前node为：", slog.Any("node", node.Base))
		nodeStatus, err := node.IsOnline(ctx)
		if err != nil {
			slog.Error("Failed to get node status", slog.Any("error", err))
			return nil, err
		}
		if nodeStatus {
			slog.Debug("当前node在线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "在线"})
		} else {
			slog.Warn("当前node离线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "离线"})
			continue
		}
	}
	return &nodeInfo, nil
}

type Nodes []Node

type Node struct {
	NodeName   string `json:"node_name"`
	NodeStatus string `json:"node_status"`
}

// GetJenkinsNodeStatus	获取jenkins中node的状态信息
func GetJenkinsNodeStatus() (*Nodes, error) {
	ctx := context.Background()
	if Jenkins == nil {
		return nil, errors.New("jenkins not initialized")
	}
	nodes, err := Jenkins.GetAllNodes(ctx)
	if err != nil {
		slog.Error("Failed to get all nodes", slog.Any("error", err))
		return nil, err
	}
	var nodeInfo Nodes
	for _, node := range nodes {
		poll, err := node.Poll(ctx)
		if err != nil {
			slog.Error("Failed to poll node", slog.Any("error", err))
			return nil, err
		}
		slog.Debug("获取node信息，poll的值为：", slog.Any("poll", poll))
		slog.Debug("当前node为：", slog.Any("node", node.Base))
		nodeStatus, err := node.IsOnline(ctx)
		if err != nil {
			slog.Error("Failed to get node status", slog.Any("error", err))
			return nil, err
		}
		if nodeStatus {
			slog.Debug("当前node在线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "在线"})
		} else {
			slog.Warn("当前node离线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "离线"})
			continue
		}
	}
	return &nodeInfo, nil
}

// GetJenkinsBuildLog 获取jenkins构建日志
func GetJenkinsBuildLog(jobName string, buildId int64) (string, error) {
	ctx := context.Background()
	if Jenkins == nil {
		return "", errors.New("jenkins not initialized")
	}
	job, err := Jenkins.GetJob(ctx, jobName)
	if err != nil {
		slog.Error("获取 Job 失败", slog.Any("error", err))
		return "", err
	}
	build, err := job.GetBuild(ctx, buildId)
	if err != nil {
		slog.Error("获取 Job 构建失败", slog.Any("error", err))
		return "", err
	}
	log := build.GetConsoleOutput(ctx)
	return log, nil
}

// StreamJenkinsBuildLog	持续获取jenkins的构建日志
func StreamJenkinsBuildLog(req *BuildLogQuery, logChan chan<- BuildLogChunk, errChan chan<- error) bool {
	ctx := context.Background()
	if Jenkins == nil {
		errChan <- errors.New("jenkins not initialized")
		return false
	}
	job, err := Jenkins.GetJob(ctx, req.JobName)
	if err != nil {
		slog.Error("获取Job失败", "job_name", req.JobName, "build_id", req.BuildId, "err", err)
		errChan <- err
		return false
	}
	build, err := job.GetBuild(ctx, req.BuildId)
	if err != nil {
		slog.Error("获取buildId失败", "job_name", req.JobName, "build_id", req.BuildId, "err", err)
		errChan <- err
		return false
	}

	start := req.Start
	if start < 0 {
		start = 0
	}

	tz := time.FixedZone("UTC+8", 8*3600)
	pingEvery := 25 * time.Second
	lastPing := time.Now()
	flushEvery := 500 * time.Millisecond
	lastFlush := time.Now()
	pending := ""

	normalizeLogText := func(s string) string {
		// Jenkins/log 工具常见用 \r 做同一行刷新；这里把 \r 当作换行处理
		// 先处理 \r\n，避免变成两个换行
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		return s
	}

	emitLines := func(lines []string, nextStart int64) {
		if len(lines) == 0 {
			return
		}
		logChan <- BuildLogChunk{Lines: lines, NextStart: nextStart}
		lastFlush = time.Now()
	}

	flushPending := func(nextStart int64) {
		// 没有完整行但已经积累了内容：按时间强制 flush，避免前端“憋日志”
		s := strings.TrimSpace(pending)
		if s == "" {
			pending = ""
			return
		}
		pending = ""
		emitLines([]string{convertTimestamperToTZ(s, tz)}, nextStart)
	}

	consumeText := func(text string, nextStart int64) {
		if text == "" {
			return
		}
		text = normalizeLogText(text)
		pending += text

		// 把 pending 中的完整行（含 \n）切出来下发；尾部不完整行留在 pending
		lines := make([]string, 0, 32)
		for {
			idx := strings.IndexByte(pending, '\n')
			if idx < 0 {
				break
			}
			line := pending[:idx]
			pending = pending[idx+1:]
			if line == "" {
				continue
			}
			lines = append(lines, convertTimestamperToTZ(line, tz))
		}
		emitLines(lines, nextStart)
	}

	for {
		// 客户端断开时尽快退出（避免后台 goroutine 泄漏）
		select {
		case <-ctx.Done():
			return true
		default:
		}

		chunkText, nextStart, moreData, err := getProgressiveText(req.JobName, req.BuildId, start)
		if err != nil {
			errChan <- err
			return false
		}

		// Jenkins 可能返回 nextStart 小于当前 start（异常情况），做保护避免死循环
		if nextStart < start {
			nextStart = start
		}

		// 分行（支持 \r 刷新）+ 过滤空行 + 时间戳转换
		consumeText(chunkText, nextStart)

		start = nextStart

		// 无换行时也定时 flush，避免前端一直收不到日志
		if pending != "" && time.Since(lastFlush) >= flushEvery {
			flushPending(start)
		}

		// 心跳：仅用于保持连接活跃（不影响前端 onmessage 追加逻辑）
		if time.Since(lastPing) >= pingEvery {
			logChan <- BuildLogChunk{IsPing: true, NextStart: start}
			lastPing = time.Now()
		}

		// 如果当前没有更多数据：
		// - 构建未结束：继续轮询
		// - 构建已结束：再拉一次确保最后增量，然后退出
		if !moreData {
			if !build.IsRunning(ctx) {
				finalText, finalNext, _, err := getProgressiveText(req.JobName, req.BuildId, start)
				if err == nil && finalNext >= start {
					consumeText(finalText, finalNext)
					start = finalNext
				}
				// 退出前把残留半行也 flush 掉
				if pending != "" {
					flushPending(start)
				}
				return true
			}
			// 没有更多数据但还在构建：如果 pending 有残留，优先 flush
			if pending != "" && time.Since(lastFlush) >= flushEvery {
				flushPending(start)
			}
			time.Sleep(1 * time.Second)
			continue
		}

		// 还有更多数据：短暂 sleep 防止过高频请求
		time.Sleep(300 * time.Millisecond)
	}
}

// getProgressiveText 使用 Jenkins 标准接口获取增量日志：
// GET /job/<job>/<build>/logText/progressiveText?start=<offset>
// 返回：增量文本、nextStart(X-Text-Size)、moreData(X-More-Data)
func getProgressiveText(jobName string, buildID int64, start int64) (string, int64, bool, error) {
	base := strings.TrimRight(config.Main.Jenkins.Address, "/")
	jobPath := buildJobPath(jobName)
	u := base + jobPath + "/" + strconv.FormatInt(buildID, 10) + "/logText/progressiveText"

	parsed, err := url.Parse(u)
	if err != nil {
		return "", start, false, err
	}
	q := parsed.Query()
	q.Set("start", strconv.FormatInt(start, 10))
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", parsed.String(), nil)
	if err != nil {
		return "", start, false, err
	}
	// Jenkins token 作为 password 使用 basic auth
	req.SetBasicAuth(config.Main.Jenkins.UserName, config.Main.Jenkins.Token)
	req.Header.Set("Accept", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", start, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", start, false, errors.New("jenkins progressiveText 请求失败：" + resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", start, false, err
	}
	text := string(bodyBytes)

	nextStart := start
	if v := resp.Header.Get("X-Text-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			nextStart = n
		}
	}
	more := strings.EqualFold(resp.Header.Get("X-More-Data"), "true")
	return text, nextStart, more, nil
}

// buildJobPath 将 "a/b/c" 转换为 Jenkins 的 "/job/a/job/b/job/c"
func buildJobPath(jobName string) string {
	jobName = strings.Trim(jobName, "/")
	if jobName == "" {
		return ""
	}
	parts := strings.Split(jobName, "/")
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("/job/")
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}

// convertTimestamperToTZ 将形如 "[2026-01-12T08:14:42.716Z] xxx" 的时间戳转换到指定时区。
// 若不匹配/解析失败，则原样返回（兼容无时间戳日志）。
func convertTimestamperToTZ(line string, tz *time.Location) string {
	if len(line) < 3 || line[0] != '[' {
		return line
	}
	end := strings.IndexByte(line, ']')
	if end <= 1 {
		return line
	}
	ts := line[1:end]
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return line
	}
	tt := t.In(tz)
	newTS := tt.Format("2006-01-02T15:04:05.000-07:00")
	return "[" + newTS + "]" + line[end+1:]
}

// CreateBuildTask 创建一个新的 Jenkins 构建任务
// 传入：管线名称、入参
// 返回：构建id、管线名称、错误信息
func CreateBuildTask(jobName string, params map[string]string) (int64, string, error) {
	ctx := context.Background()
	// 实现创建逻辑
	if Jenkins == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	queueId, err := Jenkins.BuildJob(ctx, jobName, params)
	if err != nil {
		slog.Error("任务构建失败", slog.Any("error", err))
		return 0, "", err
	}
	if queueId == 0 {
		return 0, "", errors.New("作业任务已在队列中。 如需优化,请将该job的静默期调整为0秒。默认为5秒,需设置为0秒即可解决。配置路径：jobName-->configure-->Quiet period  静默功能说明：https://www.jenkins.io/blog/2010/08/11/quiet-period-feature/")
	}

	// 这段代码是一个循环，检查任务的 Executable.Number 是否为 0。根据 Jenkins 的 API，任务在队列中会有一个大约 4.7 秒的静默期。
	// 在此期间，任务的构建编号可能尚未分配。循环中每隔 1 秒调用一次 Poll 方法来更新任务状态。如果在此过程中发生错误，返回 nil 和错误信息。
	buildInfo, err := Jenkins.GetBuildFromQueueID(ctx, queueId)
	if err != nil {
		return 0, "", err
	}
	// job任务中的id
	jobBuildIdStr := buildInfo.Raw.ID

	jobBuildId, err := strconv.ParseInt(jobBuildIdStr, 10, 64)
	if err != nil {
		return 0, "", err
	}

	return jobBuildId, jobName, nil
}

func BuildTask() error {
	if Jenkins == nil {
		return errors.New("jenkins not initialized")
	}

	return nil
}

// GetBuildStatus 获取 Jenkins构建结果
// 传入：管线名称、构建id
// 返回：构建状态（RUNNING、SUCCESS、FAILURE、ABORTED）
func GetBuildStatus(jobName string, buildId int64) (string, error) {
	if Jenkins == nil {
		return "", errors.New("jenkins not initialized")
	}

	ctx := context.Background()

	// 获取job对象
	job, err := Jenkins.GetJob(ctx, jobName)
	if err != nil {
		slog.Error("获取 Job 失败", slog.Any("error", err))
		return "", err
	}
	// 获取具体构建实例
	build, err := job.GetBuild(ctx, buildId)
	if err != nil {
		slog.Error("获取 Job 构建失败", slog.Any("error", err))
		return "", err
	}

	// 检查构建是否仍在运行
	isRunning := build.IsRunning(ctx)
	if isRunning {
		return "RUNNING", nil
	}

	// 获取最终构建结果
	result := build.GetResult()
	switch result {
	case "SUCCESS":
		return "SUCCESS", nil
	case "FAILURE":
		return "FAILURE", nil
	case "ABORTED":
		return "ABORTED", nil
	default:
		return "ABNORMAL", nil
	}
}

//// GetJob 获取 Jenkins Job
//func GetJob(id string) (Job, error) {
//	// 实现获取逻辑
//	return Job{}, nil
//}

//// UpdateJob 更新 Jenkins Job
//func UpdateJob(job Job) error {
//	// 实现更新逻辑
//	return nil
//}

// DeleteJob 删除 Jenkins
func DeleteJob(id string) error {
	// 实现删除逻辑
	return nil
}
