package controller

import (
	"ares/internal/api/util"
	"ares/internal/db"
	"ares/internal/entity"
	"ares/internal/jenkins"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// GetJenkinsNodeStatus
// @Tags Jenkins
// @Summary 获取jenkins节点的状态
// @Success 200 {object} util.ResponseTemplate{code=int,result=jenkins.Nodes} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/status/nodes [get]
func GetJenkinsNodeStatus(c *gin.Context) {
	if !jenkins.IsConfigured() {
		c.JSON(503, util.ResponseFailure("Jenkins 集成未启用", "jenkins integration is disabled"))
		return
	}
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		c.JSON(502, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("", nodeInfo))
}

//// GetJenkinsBuildLog
//// @Tags Publish
//// @Summary 获取构建日志
//// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
//// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
//// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
//// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
//// @Router	/api/v1/job/log/ [get]
//func GetJenkinsBuildLog(c *gin.Context) {
//	job := c.Param("job")
//	idStr := c.Param("id")
//	id, err := strconv.ParseInt(idStr, 10, 64) // 将 id 字符串转换为 int64
//	if err != nil {
//		c.JSON(400, util.ResponseFailure("buildNumber转换失败", err.Error()))
//		return
//	}
//	log, err := jenkins.GetJenkinsBuildLog(job, id)
//	if err != nil {
//		c.JSON(502, util.ResponseFailure("", err.Error()))
//		return
//	}
//	c.JSON(200, util.ResponseSuccessful("", log))
//}

// StreamJenkinsBuildLogHandler
// @Tags Publish
// @Summary 按 Ares 任务获取旧版 Jenkins 流式日志 (SSE格式)
// @Param task_id query int true "Ares 发布任务 ID"
// @Param log_type query string true "日志阶段：ci 或 cd"
// @Param start query int64 false "Jenkins progressiveText 起始 offset"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/job/stream/log [get]
func StreamJenkinsBuildLogHandler(c *gin.Context) {
	snapshot := jenkins.Acquire()
	if snapshot == nil {
		c.JSON(503, util.ResponseFailure("Jenkins 集成未启用", "jenkins integration is disabled"))
		return
	}
	var request struct {
		TaskID  int    `form:"task_id" binding:"required,min=1"`
		LogType string `form:"log_type" binding:"required,oneof=ci cd"`
		Start   int64  `form:"start" binding:"omitempty,min=0"`
	}
	if err := c.ShouldBindQuery(&request); err != nil {
		c.JSON(400, util.ResponseFailure("参数错误", err.Error()))
		return
	}
	var task entity.TaskRecord
	has, err := db.Engine.Context(c.Request.Context()).
		Where("task_id = ? AND deleted_at IS NULL", request.TaskID).Get(&task)
	if err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseFailure("查询任务失败", err.Error()))
		return
	}
	if !has {
		c.JSON(http.StatusNotFound, util.ResponseFailure("任务不存在", fmt.Sprintf("task_id=%d", request.TaskID)))
		return
	}
	query, err := taskBuildLogReference(task, request.LogType, request.Start, snapshot.Address())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, util.ResponseFailure("任务没有对应日志", err.Error()))
		return
	}

	// EventSource 断线重连会自动携带 Last-Event-ID：这里用它作为 progressiveText 的 start，实现零前端改动的断线续传
	if lastID := c.GetHeader("Last-Event-ID"); lastID != "" {
		if v, err := strconv.ParseInt(lastID, 10, 64); err == nil && v >= 0 {
			query.Start = v
		}
	}

	logChan := make(chan jenkins.BuildLogChunk)
	errChan := make(chan error, 1)
	var mu sync.Mutex

	// 创建一个完成通道，用于通知主goroutine任务完成或出错
	doneChan := make(chan struct{})
	var streamErr error

	// 启动日志流处理
	go func() {
		defer close(doneChan)
		success := snapshot.StreamJenkinsBuildLog(c.Request.Context(), query, logChan, errChan)
		if !success {
			// Cancellation can make the producer return without publishing an
			// error. Never wait indefinitely for a value that may not exist.
			select {
			case err := <-errChan:
				streamErr = err
			default:
			}
		}
	}()

	// 设置 SSE 相关的响应头
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲
	c.Writer.WriteHeaderNow()

	// 使用Stream方法处理响应
	c.Stream(func(w io.Writer) bool {
		select {
		case chunk, ok := <-logChan:
			if !ok {
				return false
			}
			// 心跳：只用于保持连接，不触发前端默认 onmessage（event != message）
			if chunk.IsPing {
				_, _ = fmt.Fprintf(w, "event: ping\nid: %d\ndata: {}\n\n", chunk.NextStart)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return true
			}
			mu.Lock()
			// 将日志列表包装在响应对象中
			response := util.ResponseSuccessful("", chunk.Lines)
			responseBytes, err := json.Marshal(response)
			if err != nil {
				mu.Unlock()
				return false
			}
			// 使用 SSE id 实现断线续传（EventSource 自动携带 Last-Event-ID 重连）
			_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", chunk.NextStart, string(responseBytes))
			if err != nil {
				mu.Unlock()
				return false
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			mu.Unlock()
			return true
		case <-doneChan:
			// 如果有错误，返回错误响应
			if streamErr != nil {
				errorResponse := util.ResponseFailure("", streamErr.Error())
				responseBytes, _ := json.Marshal(errorResponse)
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(responseBytes))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			// 发送结束事件
			fmt.Fprintf(w, "event: end\ndata: end of stream\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return false
		}
	})
}

func taskBuildLogReference(task entity.TaskRecord, logType string, start int64, currentAddress string) (*jenkins.BuildLogQuery, error) {
	storedAddress := strings.TrimRight(strings.TrimSpace(task.JenkinsAddress), "/")
	currentAddress = strings.TrimRight(strings.TrimSpace(currentAddress), "/")
	if storedAddress == "" {
		return nil, fmt.Errorf("任务 %d 未记录原 Jenkins 实例，无法安全查询历史日志", task.TaskId)
	}
	if currentAddress == "" || storedAddress != currentAddress {
		return nil, fmt.Errorf("任务 %d 的 Jenkins 实例与当前连接不匹配", task.TaskId)
	}
	query := &jenkins.BuildLogQuery{Start: start}
	switch strings.ToLower(strings.TrimSpace(logType)) {
	case "ci":
		query.JobName, query.BuildId = strings.TrimSpace(task.CiJobName), task.CiBuildId
	case "cd":
		query.JobName, query.BuildId = strings.TrimSpace(task.CdJobName), task.CdBuildId
	default:
		return nil, fmt.Errorf("log_type 只支持 ci 或 cd")
	}
	if query.JobName == "" || query.BuildId <= 0 {
		return nil, fmt.Errorf("任务 %d 的 %s Job/Build 引用不存在", task.TaskId, strings.ToUpper(logType))
	}
	return query, nil
}

//// GetBuildTaskStatus
//// @Tags Publish
//// @Summary 获取构建任务的状态
//// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
//// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
//// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
//// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
//// @Router	/api/v1/deploy/query/status [get]
//func GetBuildTaskStatus(c *gin.Context) {
//	jobName := c.Query("job_name")
//	buildNumberStr := c.Query("build_number")
//
//	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64) // 将 id 字符串转换为 int64
//	if err != nil {
//		c.JSON(400, util.ResponseFailure("buildNumber转换失败:", err.Error()))
//		return
//	}
//	buildStatus, err := jenkins.GetBuildStatus(jobName, buildNumber)
//	if err != nil {
//		c.JSON(502, util.ResponseFailure("", err.Error()))
//		return
//	}
//	c.JSON(200, util.ResponseSuccessful("", buildStatus))
//}
