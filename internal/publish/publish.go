package publish

import (
	"encoding/json"
	"fmt"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/db"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/entity"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/jenkins"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/tool"
	"strconv"
	"time"
)

// PublishManager 发布管理器
type PublishManager struct {
}

// NewPublishManager 创建新的发布管理器
func NewPublishManager() *PublishManager {
	return &PublishManager{}
}

// CreatePublishRequest 触发发布动作请求
type CreatePublishRequest struct {
	AppName         string `json:"app_name"`
	Branch          string `json:"branch"`
	Env             string `json:"env"`
	AppId           int    `json:"app_id"`
	CodePackageType string `json:"code_package_type"`
}

// CreateBatchPublishRequest 批量触发发布动作请求
type CreateBatchPublishRequest struct {
	BatchPublish []CreatePublishRequest `json:"batch_publish"`
}

// CreatePublishResult 表示单次应用发布动作
type CreatePublishResult struct {
	TaskRecord *entity.TaskRecord `json:"task_record"`
	Error      string             `json:"error"`
	Success    bool               `json:"success"`
}

// CreateBatchPublishResponse 表示批量应用发布动作
type CreateBatchPublishResponse struct {
	TaskRecords  []CreatePublishResult `json:"task_records"`
	SuccessCount int                   `json:"success_count"` // 成功数量
	FailureCount int                   `json:"failure_count"` // 失败数量
	TotalCount   int                   `json:"total_count"`   // 总体数量
}

// VerifyApp 检验应用信息信息
func (pm *PublishManager) VerifyApp(req *CreatePublishRequest) (*entity.Apps, error) {
	var app []entity.Apps
	err := db.Engine.Where("app_name = ? AND deleted_at IS NULL", req.AppName).Find(&app)
	if err != nil {
		return nil, fmt.Errorf("应用信息查询失败：%s", err)
	}
	if len(app) == 0 {
		return nil, fmt.Errorf("未找到应用：%s", req.AppName)
	}
	if len(app) > 1 {
		return nil, fmt.Errorf("匹配到 %d 条记录信息，请检查app_name：%s 是否唯一存在", len(app), req.AppName)
	}
	return &app[0], nil
}

// VerifyAppConfig 检测应用对应环境配置信息
func (pm *PublishManager) VerifyAppConfig(req *CreatePublishRequest) (*entity.AppConfigs, error) {
	var appConfigs []entity.AppConfigs
	err := db.Engine.Where("app_id = ? AND env = ? AND deleted_at IS NULL", req.AppId, req.Env).Find(&appConfigs)
	if err != nil {
		return nil, fmt.Errorf("配置信息查询失败：%s", err)
	}
	if len(appConfigs) == 0 {
		return nil, fmt.Errorf("未找到对应环境配置信息，appId：%d,env：%s", req.AppId, req.Env)
	}
	if len(appConfigs) > 1 {
		return nil, fmt.Errorf("匹配到 %d 条记录信息，请检查appId：%d, env：%s 是否唯一存在", len(appConfigs), req.AppId, req.Env)
	}
	return &appConfigs[0], nil
}

// VerifyEnvConfigs 检验指定的环境信息
func (pm *PublishManager) VerifyEnvConfigs(req *CreatePublishRequest) (*entity.EnvConfigs, error) {
	var envConfigs []entity.EnvConfigs
	err := db.Engine.Where("env = ? ", req.Env).Find(&envConfigs)
	if err != nil {
		return nil, fmt.Errorf("配置信息查询失败：%s", err)
	}
	if len(envConfigs) == 0 {
		return nil, fmt.Errorf("未找到环境配置：%s", req.Env)
	}
	if len(envConfigs) > 1 {
		return nil, fmt.Errorf("匹配到 %d 条记录信息，请检查env：%s 是否唯一存在", len(envConfigs), req.Env)
	}
	return &envConfigs[0], nil
}

// VerifyPipelines 检验指定的管线信息
func (pm *PublishManager) VerifyPipelines(req *CreatePublishRequest) (*entity.Pipelines, error) {
	var pipelines []entity.Pipelines
	err := db.Engine.Where("code_package_type = ? AND deleted_at IS NULL", req.CodePackageType).Find(&pipelines)
	if err != nil {
		return nil, fmt.Errorf("代码包类型所属管线查询失败：%s", err)
	}
	if len(pipelines) == 0 {
		return nil, fmt.Errorf("未找到管线配置：%s", req.Env)
	}
	if len(pipelines) > 1 {
		return nil, fmt.Errorf("匹配到 %d 条记录信息，请检查code_package_type：%s 是否唯一存在", len(pipelines), req.CodePackageType)
	}
	return &pipelines[0], nil
}

// CreatePublish 创建单次发布动作
func (pm *PublishManager) CreatePublish(req *CreatePublishRequest) (*entity.TaskRecord, error) {
	app, err := pm.VerifyApp(req)
	if err != nil {
		return nil, err
	}

	req.AppId = app.AppId

	appConfig, err := pm.VerifyAppConfig(req)
	if err != nil {
		return nil, err
	}
	req.CodePackageType = appConfig.CodePackageType

	envConfigs, err := pm.VerifyEnvConfigs(req)
	if err != nil {
		return nil, err
	}

	pipelines, err := pm.VerifyPipelines(req)
	if err != nil {
		return nil, err
	}

	// 这里需要构建实际的发布数据
	jenkinsParam, taskId, err := pm.ComposePublishData(req, app, appConfig, envConfigs)
	if err != nil {
		return nil, err
	}

	// 对相应的管线创建新的构建任务
	jobBuildId, _, err := jenkins.CreateBuildTask(pipelines.JobName, jenkinsParam)
	if err != nil {
		return nil, err
	}
	// 回写数据库中的数据，将编译阶段产生的taskId回写到表中
	var taskRecord entity.TaskRecord
	taskRecord.CiBuildId = jobBuildId
	taskRecord.Status = "packaging"
	taskRecord.CiJobName = pipelines.JobName
	// 更新指定id的数据
	_, err = db.Engine.ID(taskId).Update(&taskRecord)
	if err != nil {
		return nil, fmt.Errorf("ci阶段taskId回写失败：%s", err)
	}
	response := &taskRecord
	return response, nil
}

// CreateBatchPublish 批量创建发布任务
func (pm *PublishManager) CreateBatchPublish(req *CreateBatchPublishRequest) (*CreateBatchPublishResponse, error) {
	response := &CreateBatchPublishResponse{
		TaskRecords: make([]CreatePublishResult, len(req.BatchPublish)),
	}
	response.TotalCount = len(req.BatchPublish)

	// 创建结果通道
	resultChan := make(chan struct {
		index  int
		result CreatePublishResult
	}, len(req.BatchPublish))

	// 并发处理每个请求
	for i, publishReq := range req.BatchPublish {
		go func(index int, req CreatePublishRequest) {
			publish, err := pm.CreatePublish(&publishReq)
			result := CreatePublishResult{
				Success:    err == nil,
				TaskRecord: publish,
			}
			if err != nil {
				result.Error = err.Error()
			}
			resultChan <- struct {
				index  int
				result CreatePublishResult
			}{index, result}
		}(i, publishReq)
	}
	// 收集结果
	for i := 0; i < len(req.BatchPublish); i++ {
		result := <-resultChan
		response.TaskRecords[result.index] = result.result
		if result.result.Success {
			response.SuccessCount++
		} else {
			response.FailureCount++
		}
	}
	return response, nil
}

// ComposePublishData 构建发布数据
// 这里主要是拼接各种发布参数信息
func (pm *PublishManager) ComposePublishData(req *CreatePublishRequest, app *entity.Apps, appConfig *entity.AppConfigs, envConfig *entity.EnvConfigs) (map[string]string, int, error) {
	JenkinsParam := make(map[string]string)
	// 输出当前时间的时间戳，精确到毫秒
	milliseconds := time.Now().UnixMilli()

	// 示例格式
	// harbor.ttpai.work/publish/dev/asr-job:1739265948923
	image := envConfig.HarborURL + "/" + envConfig.HarborProjectName + "/" + envConfig.Env + "/" + app.AppName + ":" + fmt.Sprintf("%d", milliseconds)

	JenkinsParam["app_name"] = app.AppName
	JenkinsParam["env"] = envConfig.Env
	JenkinsParam["branch"] = req.Branch
	JenkinsParam["git_url"] = app.GitUrl
	JenkinsParam["code_package_type"] = appConfig.CodePackageType
	JenkinsParam["code_package_path"] = appConfig.CodePackagePath
	JenkinsParam["code_package_name"] = appConfig.CodePackageName
	JenkinsParam["base_image"] = appConfig.BaseImage
	JenkinsParam["pod_count"] = strconv.Itoa(appConfig.PodCount)
	JenkinsParam["limits_memory"] = strconv.Itoa(appConfig.LimitsMemory)
	JenkinsParam["probe_type"] = appConfig.ProbeType
	JenkinsParam["probe_check_path"] = appConfig.ProbeCheckPath
	JenkinsParam["pre_stop_type"] = appConfig.PreStopType
	JenkinsParam["pre_stop_check_path"] = appConfig.ProbeCheckPath
	JenkinsParam["pre_stop_command"] = appConfig.PreStopCommand
	JenkinsParam["domain"] = appConfig.Domain
	JenkinsParam["domain_path"] = appConfig.DomainPath
	JenkinsParam["image"] = image
	JenkinsParam["dev_language"] = app.DevLanguage
	jsonStr, err := tool.ToJSON(JenkinsParam)
	if err != nil {
		return nil, 0, err
	}
	//fmt.Println(jsonStr)
	// 先直接在数据库中写入数据，此时记录状态信息
	// 然后携带相关的发布信息，对jenkins发送api请求，jenkins会返回对应的build_number，这个用于查询执行状态和获取日志信息。
	//
	taskRecord := &entity.TaskRecord{
		AppName:       app.AppName,
		PipelineParam: json.RawMessage(jsonStr),
		Products:      image,
		Status:        "init",
	}
	// 需要在这里显式的把一些有默认值的给排除掉，golang会将未使用的值赋值为零值，而不是使用默认值
	_, err = db.Engine.Omit("ci_build_id", "cd_build_id", "message", "ci_job_name", "cd_job_name", "auto_deploy").Insert(taskRecord)
	if err != nil {
		return nil, 0, fmt.Errorf("任务记录创建失败: %s", err)
	}
	return JenkinsParam, taskRecord.TaskId, nil
}
