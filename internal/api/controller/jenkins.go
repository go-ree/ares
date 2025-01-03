package controller

import (
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/util"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/jenkins"
	"io"
	"net/http"
	"strconv"
	"sync"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/home"

	"github.com/gin-gonic/gin"
)

func Home(c *gin.Context) {
	home.Home()
	c.String(http.StatusOK, "Hello, World!")
}

func GetJenkinsNodeStatus(c *gin.Context) {
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccess(nodeInfo))
}

func GetJenkinsBuildLog(c *gin.Context) {
	job := c.Param("job")
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("buildNumber转换失败"+err.Error()))
		return
	}
	log, err := jenkins.GetJenkinsBuildLog(job, id)
	if err != nil {
		c.JSON(http.StatusBadGateway, util.ResponseError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccess(log))
}

// StreamJenkinsBuildLogHandler 处理前端请求以持续获取 Jenkins 构建日志
func StreamJenkinsBuildLogHandler(c *gin.Context) {
	jobName := c.Query("job_name")
	buildNumberStr := c.Query("build_number")

	buildNumber, err := strconv.ParseInt(buildNumberStr, 10, 64) // 将 id 字符串转换为 int64
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("buildNumber转换失败"+err.Error()))
		return
	}

	logChan := make(chan string)    // 缓存日志通道
	errChan := make(chan error)     //	缓存错误通道
	logSet := make(map[string]bool) // 用于存储已输出的日志
	var mu sync.Mutex               // 添加一个互斥锁

	go jenkins.StreamJenkinsBuildLog(jobName, buildNumber, logChan, errChan)

	go func() {
		success := jenkins.StreamJenkinsBuildLog(jobName, buildNumber, logChan, errChan)
		if success {
			close(logChan) // 关闭日志通道
			close(errChan) // 关闭错误通道
		} else {
			err := <-errChan
			c.JSON(http.StatusFailedDependency, util.ResponseError(err.Error()))
		}
	}()

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// 使用 Gin 的 Stream 方法来处理流式响应
	c.Stream(func(w io.Writer) bool {
		for {
			select {
			case log := <-logChan:
				mu.Lock() // 加锁
				// 将日志写入响应流
				if _, exists := logSet[log]; !exists { // 检查日志是否已存在
					_, err := w.Write([]byte(log))
					if err != nil {
						mu.Unlock()  // 解锁
						return false // 如果写入失败，结束处理
					}
					logSet[log] = true // 记录已输出的日志
				}
				mu.Unlock() // 解锁
			case err := <-errChan:
				if err != nil {
					c.Error(err) // 处理错误
				}
				return false // 返回 false 结束处理
			}
		}
	})
}

func CreateBuildTask(c *gin.Context) {
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
		c.JSON(http.StatusBadRequest, util.ResponseError("未指定job名称"))
		return
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("请求数据格式错误"+err.Error()))
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

	buildNumber, err := jenkins.CreateBuildTask(job, requestMap)
	if err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccess(buildNumber))
}
