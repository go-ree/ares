package controller

import (
	"ares/internal/api/util"
	"ares/internal/jenkins"
	"encoding/json"
	"io"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
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
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		c.JSON(502, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("", nodeInfo))
}

// GetJenkinsBuildLog
// @Tags Publish
// @Summary 获取构建日志
// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/job/log/ [get]
func GetJenkinsBuildLog(c *gin.Context) {
	job := c.Param("job")
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(400, util.ResponseFailure("buildNumber转换失败", err.Error()))
		return
	}
	log, err := jenkins.GetJenkinsBuildLog(job, id)
	if err != nil {
		c.JSON(502, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("", log))
}

// StreamJenkinsBuildLogHandler
// @Tags Publish
// @Summary 获取流式的构建日志
// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/job/log/ [get]
func StreamJenkinsBuildLogHandler(c *gin.Context) {
	jobName := c.Query("job_name")
	buildNumberStr := c.Query("build_number")

	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64)
	if err != nil {
		c.JSON(400, util.ResponseFailure("buildNumber转换失败", err.Error()))
		return
	}

	logChan := make(chan string)
	errChan := make(chan error, 1) // 添加缓冲区，防止goroutine泄漏
	logSet := make(map[string]bool)
	var mu sync.Mutex

	// 创建一个完成通道，用于通知主goroutine任务完成或出错
	doneChan := make(chan struct{})
	var streamErr error

	// 启动日志流处理
	go func() {
		defer close(doneChan)
		success := jenkins.StreamJenkinsBuildLog(jobName, buildNumber, logChan, errChan)
		if !success {
			if err := <-errChan; err != nil {
				streamErr = err
			}
		}
	}()

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// 使用Stream方法处理响应
	c.Stream(func(w io.Writer) bool {
		select {
		case log, ok := <-logChan:
			if !ok {
				return false
			}
			mu.Lock()
			if _, exists := logSet[log]; !exists {
				_, err := w.Write([]byte(log))
				if err != nil {
					mu.Unlock()
					return false
				}
				logSet[log] = true
			}
			mu.Unlock()
			return true
		case <-doneChan:
			// 如果有错误，返回错误响应
			if streamErr != nil {
				errorResponse := util.ResponseFailure("", streamErr.Error())
				responseBytes, _ := json.Marshal(errorResponse)
				w.Write([]byte("\nERROR: " + string(responseBytes) + "\n"))
			}
			return false
		}
	})
}

// GetBuildTaskStatus
// @Tags Publish
// @Summary 获取构建任务的状态
// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/deploy/query/status [get]
func GetBuildTaskStatus(c *gin.Context) {
	jobName := c.Query("job_name")
	buildNumberStr := c.Query("build_number")

	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(400, util.ResponseFailure("buildNumber转换失败:", err.Error()))
		return
	}
	buildStatus, err := jenkins.GetBuildStatus(jobName, buildNumber)
	if err != nil {
		c.JSON(502, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("", buildStatus))
}
