package controller

import (
	"ares/internal/api/util"
	"ares/internal/jenkins"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"sync"

	"ares/internal/home"

	"github.com/gin-gonic/gin"
)

func Home(c *gin.Context) {
	home.Home()
	c.JSON(200, util.Response(200, "Hello, World!", ""))
}

func GetJenkinsNodeStatus(c *gin.Context) {
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", nodeInfo))
}

func GetJenkinsBuildLog(c *gin.Context) {
	job := c.Param("job")
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(200, util.Response(400, "buildNumber转换失败"+err.Error(), ""))
		return
	}
	log, err := jenkins.GetJenkinsBuildLog(job, id)
	if err != nil {
		c.JSON(200, util.Response(502, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", log))
}

func StreamJenkinsBuildLogHandler(c *gin.Context) {
	jobName := c.Query("job_name")
	buildNumberStr := c.Query("build_number")

	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64)
	if err != nil {
		c.JSON(200, util.Response(400, "buildNumber转换失败"+err.Error(), ""))
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
				errorResponse := util.Response(424, streamErr.Error(), "")
				responseBytes, _ := json.Marshal(errorResponse)
				w.Write([]byte("\nERROR: " + string(responseBytes) + "\n"))
			}
			return false
		}
	})
}

func CreateBuildTask1(c *gin.Context) {
	var requestData struct {
		GitUrlPath      string `json:"git_url_path"`
		PackagePath     string `json:"package_path"`
		PackageName     string `json:"package_name"`
		AppName         string `json:"app_name"`
		Branch          string `json:"branch"`
		Env             string `json:"env"`
		BaseImageName   string `json:"base_image_name"`
		MavenImageName  string `json:"maven_image_name"`
		HarborUrl       string `json:"harbor_url"`
		ResinFileUrl    string `json:"resin_file_url"`
		PinpointFileUrl string `json:"pinpoint_file_url"`
		PublishUser     string `json:"publish_user"`
	}
	job := c.Param("job")
	if job == "" {
		c.JSON(200, util.Response(400, "未指定job名称", ""))
		return
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(200, util.Response(400, "请求数据格式错误"+err.Error(), ""))
		return
	}
	// 将 requestData 转换为 map[string]string
	requestMap := map[string]string{
		"git_url_path":      requestData.GitUrlPath,
		"package_path":      requestData.PackagePath,
		"package_name":      requestData.PackageName,
		"app_name":          requestData.AppName,
		"branch":            requestData.Branch,
		"env":               requestData.Env,
		"base_image_name":   requestData.BaseImageName,
		"maven_image_name":  requestData.MavenImageName,
		"harbor_url":        requestData.HarborUrl,
		"resin_file_url":    requestData.ResinFileUrl,
		"pinpoint_file_url": requestData.PinpointFileUrl,
		"publish_user":      requestData.PublishUser,
	}
	slog.Info("构建的任务参数为：", requestMap)
	jobBuildId, _, err := jenkins.CreateBuildTask(job, requestMap)
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", jobBuildId))
}

func GetBuildTaskStatus(c *gin.Context) {
	jobName := c.Query("job_name")
	buildNumberStr := c.Query("build_number")

	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(200, util.Response(400, "buildNumber转换失败:"+err.Error(), ""))
		return
	}
	buildStatus, err := jenkins.GetBuildStatus(jobName, buildNumber)
	if err != nil {
		c.JSON(400, util.Response(502, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", buildStatus))
}
