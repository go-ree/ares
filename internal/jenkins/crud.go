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
	// 关键优化：
	// 1) 不再依赖“必须等到 \n 才推送”，避免无换行时前端长时间不更新
	// 2) 把一次拿到的超大日志拆成多个较小的 SSE event（更利于前端及时渲染/减少中间层缓冲）
	// 3) 支持 \r / \r\n（常见的同一行刷新进度条）
	const maxLinesPerEvent = 120
	const maxBytesPerEvent = 16 * 1024

	type seg struct {
		lines    []string
		nextFrom int64 // 该段对应的 Jenkins offset（用于 SSE id / 断线续传）
	}

	splitToSegments := func(text string, baseStart int64, headerNext int64) []seg {
		if text == "" {
			return nil
		}
		out := make([]seg, 0, 8)
		lines := make([]string, 0, maxLinesPerEvent)
		bytesInSeg := 0

		emit := func(nextOffset int64) {
			if len(lines) == 0 {
				return
			}
			// clamp
			if nextOffset < baseStart {
				nextOffset = baseStart
			}
			if headerNext > 0 && nextOffset > headerNext {
				nextOffset = headerNext
			}
			out = append(out, seg{lines: lines, nextFrom: nextOffset})
			lines = make([]string, 0, maxLinesPerEvent)
			bytesInSeg = 0
		}

		lineStart := 0
		i := 0
		for i < len(text) {
			b := text[i]
			// delimiter: \n, \r, \r\n
			if b == '\n' || b == '\r' {
				end := i
				delimLen := 1
				if b == '\r' && i+1 < len(text) && text[i+1] == '\n' {
					delimLen = 2
				}
				line := text[lineStart:end]
				lineStart = i + delimLen
				i += delimLen

				// 过滤空行（保持旧行为）
				if line != "" {
					lines = append(lines, convertTimestamperToTZ(line, tz))
					bytesInSeg += (len(line) + delimLen)
				} else {
					// 空行也要把 delimiter 字节计入 offset 推进，否则 id 会落后
					bytesInSeg += delimLen
				}

				// 达到阈值就发一段：按行数或段内字节数控制 event 大小
				if len(lines) >= maxLinesPerEvent || bytesInSeg >= maxBytesPerEvent {
					emit(baseStart + int64(lineStart))
				}
				continue
			}
			i++
		}

		// 尾部没有 delimiter 的残留：也作为一行直接下发（避免“憋住”），offset 直接推进到 headerNext
		if lineStart < len(text) {
			tail := text[lineStart:]
			if tail != "" {
				lines = append(lines, convertTimestamperToTZ(tail, tz))
			}
			emit(headerNext)
			return out
		}

		// 恰好以 delimiter 结尾：最后一段的 offset 用 baseStart+lineStart（应等于 headerNext）
		if len(lines) > 0 {
			emit(baseStart + int64(lineStart))
		}
		return out
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

		// 分段推送：把一次 progressiveText 拿到的大块内容拆小，降低前端/中间层“攒包”概率
		if chunkText != "" {
			segs := splitToSegments(chunkText, start, nextStart)
			for _, s := range segs {
				if len(s.lines) == 0 {
					continue
				}
				logChan <- BuildLogChunk{Lines: s.lines, NextStart: s.nextFrom}
			}
		}

		start = nextStart

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
					if finalText != "" {
						segs := splitToSegments(finalText, start, finalNext)
						for _, s := range segs {
							if len(s.lines) == 0 {
								continue
							}
							logChan <- BuildLogChunk{Lines: s.lines, NextStart: s.nextFrom}
						}
					}
					start = finalNext
				}
				return true
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
