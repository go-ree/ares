package jenkins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	runtime := Current()
	if runtime == nil {
		return nil, errors.New("jenkins not initialized")
	}
	nodes, err := runtime.Client.GetAllNodes(ctx)
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
	runtime := Current()
	if runtime == nil {
		return nil, errors.New("jenkins not initialized")
	}
	nodes, err := runtime.Client.GetAllNodes(ctx)
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
	runtime := Current()
	if runtime == nil {
		return "", errors.New("jenkins not initialized")
	}
	job, err := runtime.Client.GetJob(ctx, jobName)
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
func StreamJenkinsBuildLog(ctx context.Context, req *BuildLogQuery, logChan chan<- BuildLogChunk, errChan chan<- error) bool {
	snapshot := Acquire()
	if snapshot == nil {
		sendJenkinsStreamError(ctx, errChan, errors.New("jenkins not initialized"))
		return false
	}
	return snapshot.StreamJenkinsBuildLog(ctx, req, logChan, errChan)
}

// StreamJenkinsBuildLog streams through one immutable client snapshot. This
// prevents a settings change between task-instance validation and the first
// Jenkins request from redirecting a historical log query to another server.
func (s *ClientSnapshot) StreamJenkinsBuildLog(ctx context.Context, req *BuildLogQuery, logChan chan<- BuildLogChunk, errChan chan<- error) bool {
	if s == nil || s.runtime == nil {
		sendJenkinsStreamError(ctx, errChan, errors.New("jenkins not initialized"))
		return false
	}
	runtime := s.runtime
	jobParts := splitJobName(req.JobName)
	if len(jobParts) == 0 {
		sendJenkinsStreamError(ctx, errChan, errors.New("jenkins job name is required"))
		return false
	}
	job, err := clientForContext(runtime, ctx).GetJob(ctx, jobParts[len(jobParts)-1], jobParts[:len(jobParts)-1]...)
	if err != nil {
		slog.Error("获取Job失败", "job_name", req.JobName, "build_id", req.BuildId, "err", err)
		sendJenkinsStreamError(ctx, errChan, err)
		return false
	}
	build, err := job.GetBuild(ctx, req.BuildId)
	if err != nil {
		slog.Error("获取buildId失败", "job_name", req.JobName, "build_id", req.BuildId, "err", err)
		sendJenkinsStreamError(ctx, errChan, err)
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

		chunkText, nextStart, moreData, err := getProgressiveText(ctx, runtime, req.JobName, req.BuildId, start)
		if err != nil {
			if ctx.Err() != nil {
				return true
			}
			sendJenkinsStreamError(ctx, errChan, err)
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
				if !sendJenkinsLogChunk(ctx, logChan, BuildLogChunk{Lines: s.lines, NextStart: s.nextFrom}) {
					return true
				}
			}
		}

		start = nextStart

		// 心跳：仅用于保持连接活跃（不影响前端 onmessage 追加逻辑）
		if time.Since(lastPing) >= pingEvery {
			if !sendJenkinsLogChunk(ctx, logChan, BuildLogChunk{IsPing: true, NextStart: start}) {
				return true
			}
			lastPing = time.Now()
		}

		// 如果当前没有更多数据：
		// - 构建未结束：继续轮询
		// - 构建已结束：再拉一次确保最后增量，然后退出
		if !moreData {
			if _, err := build.Poll(ctx); err != nil {
				if ctx.Err() != nil {
					return true
				}
				sendJenkinsStreamError(ctx, errChan, fmt.Errorf("刷新 Jenkins 构建状态: %w", err))
				return false
			}
			if !build.Raw.Building {
				finalText, finalNext, _, err := getProgressiveText(ctx, runtime, req.JobName, req.BuildId, start)
				if err == nil && finalNext >= start {
					if finalText != "" {
						segs := splitToSegments(finalText, start, finalNext)
						for _, s := range segs {
							if len(s.lines) == 0 {
								continue
							}
							if !sendJenkinsLogChunk(ctx, logChan, BuildLogChunk{Lines: s.lines, NextStart: s.nextFrom}) {
								return true
							}
						}
					}
					start = finalNext
				}
				return true
			}
			if !waitForJenkinsPoll(ctx, time.Second) {
				return true
			}
			continue
		}

		// 还有更多数据：短暂 sleep 防止过高频请求
		if !waitForJenkinsPoll(ctx, 300*time.Millisecond) {
			return true
		}
	}
}

func sendJenkinsStreamError(ctx context.Context, errChan chan<- error, err error) bool {
	select {
	case errChan <- err:
		return true
	case <-ctx.Done():
		return false
	}
}

func sendJenkinsLogChunk(ctx context.Context, logChan chan<- BuildLogChunk, chunk BuildLogChunk) bool {
	select {
	case logChan <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForJenkinsPoll(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// getProgressiveText 使用 Jenkins 标准接口获取增量日志：
// GET /job/<job>/<build>/logText/progressiveText?start=<offset>
// 返回：增量文本、nextStart(X-Text-Size)、moreData(X-More-Data)
func getProgressiveText(ctx context.Context, runtime *Runtime, jobName string, buildID int64, start int64) (string, int64, bool, error) {
	base := runtime.Config.Address
	jobPath := buildJobPath(jobName)
	u := base + jobPath + "/" + strconv.FormatInt(buildID, 10) + "/logText/progressiveText"

	parsed, err := url.Parse(u)
	if err != nil {
		return "", start, false, err
	}
	q := parsed.Query()
	q.Set("start", strconv.FormatInt(start, 10))
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", start, false, err
	}
	// Jenkins token 作为 password 使用 basic auth
	req.SetBasicAuth(runtime.Config.Username, runtime.Config.Token)
	req.Header.Set("Accept", "text/plain")

	client := &http.Client{Timeout: runtime.Config.Timeout}
	if runtime.Client.Requester != nil && runtime.Client.Requester.Client != nil {
		client = runtime.Client.Requester.Client
	}
	resp, err := client.Do(req)
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
	parts := splitJobName(jobName)
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("/job/")
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}

func splitJobName(jobName string) []string {
	rawParts := strings.Split(strings.Trim(jobName, "/"), "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
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
	return CreateBuildTaskContext(context.Background(), jobName, params)
}

// QueueBuildState is a single Jenkins queue observation. BuildID remains zero
// during quiet-period/agent scheduling and is filled once Jenkins assigns the
// executable. It is deliberately safe to persist and reconcile after restart.
type QueueBuildState struct {
	BuildID   int64
	Cancelled bool
	Why       string
}

// QueueBuildTaskContext triggers a Jenkins build and returns immediately with
// the durable queue id. New workflow executors must persist this id before any
// attempt to discover a build number.
func QueueBuildTaskContext(ctx context.Context, jobName string, params map[string]string) (int64, string, error) {
	snapshot := Acquire()
	if snapshot == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	return snapshot.QueueBuildTaskContext(ctx, jobName, params)
}

func (s *ClientSnapshot) QueueBuildTaskContext(ctx context.Context, jobName string, params map[string]string) (int64, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.runtime == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	jobName = strings.TrimSpace(jobName)
	jobPath := buildJobPath(jobName)
	if jobPath == "" {
		return 0, "", errors.New("jenkins job name is required")
	}

	crumb, err := s.getCrumb(ctx)
	if err != nil {
		return 0, "", err
	}
	form := make(url.Values, len(params))
	for key, value := range params {
		form.Set(key, value)
	}
	buildAction := "build"
	if len(form) > 0 {
		buildAction = "buildWithParameters"
	}
	endpoint := strings.TrimRight(s.runtime.Config.Address, "/") + jobPath + "/" + buildAction
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return 0, "", fmt.Errorf("创建 Jenkins Job %s 触发请求: %w", jobName, err)
	}
	request.SetBasicAuth(s.runtime.Config.Username, s.runtime.Config.Token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if crumb.Header != "" {
		request.Header.Set(crumb.Header, crumb.Value)
	}
	for _, cookie := range crumb.Cookies {
		request.AddCookie(cookie)
	}

	client, err := s.httpClient()
	if err != nil {
		return 0, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		slog.Error("任务构建失败", slog.String("job", jobName), slog.Any("error", err))
		return 0, "", fmt.Errorf("触发 Jenkins Job %s: %w", jobName, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return 0, "", fmt.Errorf("触发 Jenkins Job %s 失败：%s", jobName, response.Status)
	}
	queueID, err := parseQueueID(response.Header.Get("Location"))
	if err != nil {
		return 0, "", fmt.Errorf("读取 Jenkins Job %s 队列引用: %w", jobName, err)
	}
	return queueID, jobName, nil
}

type jenkinsCrumb struct {
	Header  string
	Value   string
	Cookies []*http.Cookie
}

func (s *ClientSnapshot) getCrumb(ctx context.Context) (jenkinsCrumb, error) {
	client, err := s.httpClient()
	if err != nil {
		return jenkinsCrumb{}, err
	}
	endpoint := strings.TrimRight(s.runtime.Config.Address, "/") + "/crumbIssuer/api/json"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return jenkinsCrumb{}, fmt.Errorf("创建 Jenkins crumb 请求: %w", err)
	}
	request.SetBasicAuth(s.runtime.Config.Username, s.runtime.Config.Token)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return jenkinsCrumb{}, fmt.Errorf("获取 Jenkins crumb: %w", err)
	}
	defer response.Body.Close()
	// Jenkins returns 404 when CSRF protection (and therefore the crumb issuer)
	// is disabled. In that supported configuration the build POST needs no crumb.
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
		return jenkinsCrumb{}, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
		return jenkinsCrumb{}, fmt.Errorf("获取 Jenkins crumb 失败：%s", response.Status)
	}
	var payload struct {
		Header string `json:"crumbRequestField"`
		Value  string `json:"crumb"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(&payload); err != nil {
		return jenkinsCrumb{}, fmt.Errorf("解析 Jenkins crumb: %w", err)
	}
	payload.Header = strings.TrimSpace(payload.Header)
	payload.Value = strings.TrimSpace(payload.Value)
	if payload.Header == "" || payload.Value == "" {
		return jenkinsCrumb{}, errors.New("Jenkins crumb 响应缺少 crumbRequestField 或 crumb")
	}
	return jenkinsCrumb{Header: payload.Header, Value: payload.Value, Cookies: response.Cookies()}, nil
}

func (s *ClientSnapshot) httpClient() (*http.Client, error) {
	if s == nil || s.runtime == nil || s.runtime.Client == nil || s.runtime.Client.Requester == nil || s.runtime.Client.Requester.Client == nil {
		return nil, errors.New("jenkins HTTP client not initialized")
	}
	return s.runtime.Client.Requester.Client, nil
}

func parseQueueID(location string) (int64, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return 0, errors.New("Jenkins 响应缺少 Location 队列地址")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return 0, fmt.Errorf("Jenkins Location 无效: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-3] != "queue" || parts[len(parts)-2] != "item" {
		return 0, fmt.Errorf("Jenkins Location 不包含 queue/item：%q", location)
	}
	queueID, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil || queueID <= 0 {
		return 0, fmt.Errorf("Jenkins Location 队列 ID 无效：%q", location)
	}
	return queueID, nil
}

// GetQueueBuildStateContext performs exactly one context-aware queue poll.
func GetQueueBuildStateContext(ctx context.Context, queueID int64) (QueueBuildState, error) {
	snapshot := Acquire()
	if snapshot == nil {
		return QueueBuildState{}, errors.New("jenkins not initialized")
	}
	return snapshot.GetQueueBuildStateContext(ctx, queueID)
}

func (s *ClientSnapshot) GetQueueBuildStateContext(ctx context.Context, queueID int64) (QueueBuildState, error) {
	if s == nil || s.runtime == nil {
		return QueueBuildState{}, errors.New("jenkins not initialized")
	}
	if queueID <= 0 {
		return QueueBuildState{}, errors.New("jenkins queue_id 必须大于 0")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := strings.TrimRight(s.runtime.Config.Address, "/") + "/queue/item/" + strconv.FormatInt(queueID, 10) + "/api/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return QueueBuildState{}, err
	}
	req.SetBasicAuth(s.runtime.Config.Username, s.runtime.Config.Token)
	response, err := s.runtime.Client.Requester.Client.Do(req)
	if err != nil {
		return QueueBuildState{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return QueueBuildState{}, fmt.Errorf("jenkins queue item %d 请求失败：%s", queueID, response.Status)
	}
	var payload struct {
		Cancelled  bool   `json:"cancelled"`
		Why        string `json:"why"`
		Executable *struct {
			Number int64 `json:"number"`
		} `json:"executable"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(&payload); err != nil {
		return QueueBuildState{}, fmt.Errorf("解析 Jenkins queue item %d: %w", queueID, err)
	}
	state := QueueBuildState{Cancelled: payload.Cancelled, Why: strings.TrimSpace(payload.Why)}
	if payload.Executable != nil {
		state.BuildID = payload.Executable.Number
	}
	return state, nil
}

// CreateBuildTaskContext is the v1 compatibility entry point. It keeps the
// old build-number return contract while replacing gojenkins' uninterruptible
// sleep loop with bounded, context-aware polling. New workflows use
// QueueBuildTaskContext directly and never wait here.
func CreateBuildTaskContext(ctx context.Context, jobName string, params map[string]string) (int64, string, error) {
	snapshot := Acquire()
	if snapshot == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	return snapshot.CreateBuildTaskContext(ctx, jobName, params)
}

// CreateBuildTaskContext keeps the full v1 trigger-and-queue-poll operation on
// one immutable runtime snapshot. The legacy worker uses this together with
// AcquireForOperation so a settings hot-switch cannot split one transition
// across Jenkins instances.
func (s *ClientSnapshot) CreateBuildTaskContext(ctx context.Context, jobName string, params map[string]string) (int64, string, error) {
	if s == nil || s.runtime == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	queueID, job, err := s.QueueBuildTaskContext(ctx, jobName, params)
	if err != nil {
		return 0, "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		state, pollErr := s.GetQueueBuildStateContext(ctx, queueID)
		if pollErr != nil {
			return 0, "", pollErr
		}
		if state.Cancelled {
			return 0, "", fmt.Errorf("Jenkins 队列任务 %d 已取消", queueID)
		}
		if state.BuildID > 0 {
			return state.BuildID, job, nil
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func BuildTask() error {
	if !IsConfigured() {
		return errors.New("jenkins not initialized")
	}

	return nil
}

// GetBuildStatus 获取 Jenkins构建结果
// 传入：管线名称、构建id
// 返回：构建状态（RUNNING、SUCCESS、FAILURE、ABORTED）
func GetBuildStatus(jobName string, buildId int64) (string, error) {
	return GetBuildStatusContext(context.Background(), jobName, buildId)
}

// GetBuildStatusContext 查询 Jenkins 构建结果并贯穿调用方 context。
func GetBuildStatusContext(ctx context.Context, jobName string, buildId int64) (string, error) {
	snapshot := Acquire()
	if snapshot == nil {
		return "", errors.New("jenkins not initialized")
	}
	return snapshot.GetBuildStatusContext(ctx, jobName, buildId)
}

func (s *ClientSnapshot) GetBuildStatusContext(ctx context.Context, jobName string, buildId int64) (string, error) {
	if s == nil || s.runtime == nil {
		return "", errors.New("jenkins not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// 获取job对象。Jenkins folder 中的 job 必须把父级逐段传给
	// gojenkins，否则 "folder/job" 会被错误拼成普通 URL 路径。
	jobParts := splitJobName(jobName)
	if len(jobParts) == 0 {
		return "", errors.New("jenkins job name is required")
	}
	job, err := clientForContext(s.runtime, ctx).GetJob(ctx, jobParts[len(jobParts)-1], jobParts[:len(jobParts)-1]...)
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

	// GetBuild 已经完成一次带错误返回的 Poll；直接使用该快照。调用
	// IsRunning 会再次 Poll 且吞掉错误，瞬时网络失败会被误判为终态。
	if build.Raw.Building {
		return "RUNNING", nil
	}

	// Preserve every Jenkins terminal result. The executor maps known and
	// unknown terminal values deterministically instead of treating them as an
	// indefinitely running build.
	result := strings.ToUpper(strings.TrimSpace(build.GetResult()))
	if result == "" {
		result = "ABNORMAL"
	}
	return result, nil
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
