package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/environment"
	"github.com/go-ree/ares/internal/release"
	"github.com/go-ree/ares/internal/tool"
	"github.com/go-ree/ares/internal/workflow"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// PublishManager 发布管理器
type PublishManager struct {
	utilManager *util.ParamPage
}

var ErrWorkflowNotConfigured = errors.New("应用环境尚未配置发布流程")

// NewPublishManager 创建新的发布管理器
func NewPublishManager() *PublishManager {
	return &PublishManager{
		utilManager: util.NewUtilManager(),
	}
}

// CreatePublishRequest 触发发布动作所需的请求参数
type CreatePublishRequest struct {
	AppName   string         `json:"app_name"`
	Branch    string         `json:"branch"`
	Env       string         `json:"env"`
	Publisher string         `json:"publisher"`
	IsRundeck bool           `json:"is_rundeck"`
	ExtraData map[string]any `json:"extra_data,omitempty"`
}

// PublishRequest 实际发布需要用到的参数
type PublishRequest struct {
	AppName         string         `json:"app_name"`
	RundeckAppName  *string        `json:"rundeck_app_name"`
	Branch          string         `json:"branch"`
	Env             string         `json:"env"`
	Publisher       string         `json:"publisher"`
	AppId           int            `json:"app_id"`
	CodePackageType string         `json:"code_package_type"`
	ExtraData       map[string]any `json:"extra_data,omitempty"`
}

// CreateBatchPublishRequest 批量触发发布动作请求
type CreateBatchPublishRequest struct {
	BatchPublish []CreatePublishRequest `json:"batch_publish"`
}

// CreatePublishResult 表示单次应用发布动作的结果
type CreatePublishResult struct {
	RequestIndex int                `json:"request_index"`
	AppName      string             `json:"app_name"`
	Env          string             `json:"env"`
	TaskRecord   *entity.TaskRecord `json:"task_record"`
	Error        string             `json:"error"`
	Success      bool               `json:"success"`
}

// CreateBatchPublishResponse 表示批量应用发布动作的结果
type CreateBatchPublishResponse struct {
	SuccessCount int                   `json:"success_count"` // 成功数量
	FailureCount int                   `json:"failure_count"` // 失败数量
	TotalCount   int                   `json:"total_count"`   // 总体数量
	TaskRecords  []CreatePublishResult `json:"task_records"`
}

// VerifyApp 检验应用信息信息
func (pm *PublishManager) VerifyApp(req *PublishRequest) (*entity.Apps, error) {
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

func (pm *PublishManager) VerifyRunDeckApp(req *PublishRequest) (*entity.Apps, error) {
	var app []entity.Apps
	err := db.Engine.Where("rundeck_app_name = ? AND deleted_at IS NULL", req.AppName).Find(&app)
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
func (pm *PublishManager) VerifyAppConfig(req *PublishRequest) (*entity.AppConfigs, error) {
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
func (pm *PublishManager) VerifyEnvConfigs(req *PublishRequest) (*entity.EnvConfigs, error) {
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
func (pm *PublishManager) VerifyPipelines(req *PublishRequest) (*entity.PipelinesJobCombination, error) {
	var pipelinesJobCombination []entity.PipelinesJobCombination
	err := db.Engine.Where("code_package_type = ? AND deleted_at IS NULL", req.CodePackageType).Find(&pipelinesJobCombination)
	if err != nil {
		return nil, fmt.Errorf("代码包类型所属管线查询失败：%s", err)
	}
	if len(pipelinesJobCombination) == 0 {
		return nil, fmt.Errorf("未找到%s应用类型对应管线配置,请在 pipelines_job_combination 表中定义", req.CodePackageType)
	}
	if len(pipelinesJobCombination) > 1 {
		return nil, fmt.Errorf("匹配到 %d 条记录信息，请检查pipelines_job_combination中code_package_type：%s 是否唯一存在", len(pipelinesJobCombination), req.CodePackageType)
	}
	return &pipelinesJobCombination[0], nil
}

// CreatePublish 创建单次发布动作。发布核心只面向通用工作流；
// Jenkins 是否必需由当前 AppConfig 的步骤定义决定。
func (pm *PublishManager) CreatePublish(ctx context.Context, createReq *CreatePublishRequest) (*entity.TaskRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// 验证所需的参数信息是否完成且不为空
	err := tool.ValidateStruct(createReq)
	if err != nil {
		return nil, err
	}

	envConfig, err := environment.NewService().RequireEnabled(ctx, createReq.Env)
	if err != nil {
		return nil, err
	}
	req := &PublishRequest{
		AppName:   createReq.AppName,
		Branch:    createReq.Branch,
		Env:       envConfig.Env,
		Publisher: createReq.Publisher,
		ExtraData: createReq.ExtraData,
		// RundeckAppName 将在验证后设置
	}
	var app *entity.Apps

	// 原先为了兼容 rundeck 名称做过分支处理；现在统一按 app_name 查询即可
	app, err = pm.VerifyApp(req)
	if err != nil {
		return nil, err
	}
	// 防御：避免后续 nil 解引用导致 panic
	if app == nil {
		return nil, fmt.Errorf("未找到应用：%s", req.AppName)
	}

	req.AppId = app.AppId

	appConfig, err := pm.VerifyAppConfig(req)
	if err != nil {
		return nil, err
	}
	req.CodePackageType = appConfig.CodePackageType

	// 构建平台无关的发布上下文；Jenkins Adapter 会在需要时把它
	// 转换为参数，Noop 或其他执行器可以直接忽略这些兼容字段。
	_, taskRecord, err := pm.ComposePublishData(req, app, appConfig, envConfig)
	if err != nil {
		return nil, err
	}
	runtime := release.Shared()
	if _, err := runtime.Service.CreateTask(ctx, runtime.Store, appConfig.ConfigID, taskRecord); err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			return nil, fmt.Errorf("%w，app=%s env=%s", ErrWorkflowNotConfigured, app.AppName, req.Env)
		}
		return nil, fmt.Errorf("创建发布任务失败：%w", err)
	}
	// The task and its immutable step snapshot are durable at this point. The
	// bounded background worker owns execution, so the HTTP request never waits
	// for an external system and a large batch cannot fan out unbounded goroutines.

	// 如果需要获取最新的记录（包括可能的数据库触发器更新等）
	var updatedRecord entity.TaskRecord
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	exists, err := db.Engine.Context(readCtx).ID(taskRecord.TaskId).Get(&updatedRecord)
	if err != nil {
		slog.Warn("发布任务已创建但读取最新状态失败", "task_id", taskRecord.TaskId, "error", err)
		normalizeTaskRecordNullableText(taskRecord)
		return taskRecord, nil
	}
	if !exists {
		slog.Warn("发布任务已创建但未能重新读取", "task_id", taskRecord.TaskId)
		normalizeTaskRecordNullableText(taskRecord)
		return taskRecord, nil
	}
	updatedRecord.Steps, err = runtime.Store.ListTaskSteps(readCtx, taskRecord.TaskId)
	if err != nil {
		slog.Warn("发布任务已创建但读取步骤失败", "task_id", taskRecord.TaskId, "error", err)
		updatedRecord.Steps = make([]entity.TaskStepRecord, 0)
	}
	updatedRecord.AppletImages = make([]entity.AppletImage, 0)
	normalizeTaskRecordNullableText(&updatedRecord)
	return &updatedRecord, nil
}

// CreateBatchPublish 批量创建发布任务
func (pm *PublishManager) CreateBatchPublish(ctx context.Context, req *CreateBatchPublishRequest) (*CreateBatchPublishResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil {
		return nil, fmt.Errorf("批量发布请求不能为空")
	}
	if len(req.BatchPublish) == 0 {
		return nil, fmt.Errorf("批量发布至少需要 1 个应用")
	}
	if len(req.BatchPublish) > 100 {
		return nil, fmt.Errorf("单次批量发布不能超过 100 个应用")
	}
	response := &CreateBatchPublishResponse{
		TaskRecords: make([]CreatePublishResult, len(req.BatchPublish)),
	}
	response.TotalCount = len(req.BatchPublish)

	// 创建结果通道
	resultChan := make(chan struct {
		index  int
		result CreatePublishResult
	}, len(req.BatchPublish))

	// 使用有界 worker，避免大批量请求创建无上限 goroutine。
	type indexedRequest struct {
		index int
		req   CreatePublishRequest
	}
	jobs := make(chan indexedRequest)
	workerCount := len(req.BatchPublish)
	if workerCount > 8 {
		workerCount = 8
	}
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				publish, err := pm.CreatePublish(ctx, &item.req)
				result := CreatePublishResult{
					RequestIndex: item.index,
					AppName:      item.req.AppName,
					Env:          item.req.Env,
					Success:      err == nil,
					TaskRecord:   publish,
				}
				if err != nil {
					result.Error = err.Error()
				}
				resultChan <- struct {
					index  int
					result CreatePublishResult
				}{item.index, result}
			}
		}()
	}
	go func() {
		for index, publishReq := range req.BatchPublish {
			jobs <- indexedRequest{index: index, req: publishReq}
		}
		close(jobs)
		workers.Wait()
		close(resultChan)
	}()
	// 收集结果
	for result := range resultChan {
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
func (pm *PublishManager) ComposePublishData(req *PublishRequest, app *entity.Apps, appConfig *entity.AppConfigs, envConfig *entity.EnvConfigs) (map[string]string, *entity.TaskRecord, error) {
	releaseInputs := make(map[string]string)
	// 输出当前时间的时间戳，精确到毫秒
	milliseconds := time.Now().UnixMilli()

	// Registry 配置是旧 Jenkins 参数合同的一部分，但不再是环境目录
	// 的必填项。没有配置 Registry 时，其他类型步骤仍可正常运行。
	image := ""
	if envConfig.HarborURL != "" && envConfig.HarborProjectName != "" {
		image = envConfig.HarborURL + "/" + envConfig.HarborProjectName + "/" + envConfig.Env + "/" + app.AppName + ":" + fmt.Sprintf("%d", milliseconds)
	}

	releaseInputs["app_name"] = app.AppName
	releaseInputs["env"] = envConfig.Env
	releaseInputs["branch"] = req.Branch
	releaseInputs["git_url"] = app.GitUrl
	releaseInputs["code_package_type"] = appConfig.CodePackageType
	releaseInputs["code_package_path"] = tool.NormalizeNullableText(appConfig.CodePackagePath)
	releaseInputs["code_package_name"] = tool.NormalizeNullableText(appConfig.CodePackageName)
	releaseInputs["base_image"] = tool.NormalizeNullableText(appConfig.BaseImage)
	releaseInputs["pod_count"] = strconv.Itoa(appConfig.PodCount)
	releaseInputs["limits_memory"] = strconv.Itoa(appConfig.LimitsMemory)
	releaseInputs["gpu_count"] = strconv.Itoa(appConfig.GpuCount)
	releaseInputs["probe_type"] = appConfig.ProbeType
	releaseInputs["probe_check_path"] = appConfig.ProbeCheckPath
	releaseInputs["probe_check_tcp_port"] = strconv.Itoa(appConfig.ProbeCheckTcpPort)
	releaseInputs["probe_check_http_port"] = strconv.Itoa(appConfig.ProbeCheckHttpPort)
	releaseInputs["probe_stop_check_http_port"] = strconv.Itoa(appConfig.ProbeStopCheckHttpPort)
	releaseInputs["container_port"] = strconv.Itoa(appConfig.ContainerPort)
	releaseInputs["pre_stop_type"] = appConfig.PreStopType
	releaseInputs["pre_stop_check_path"] = appConfig.PreStopCheckPath
	releaseInputs["pre_stop_command"] = tool.NormalizeNullableText(appConfig.PreStopCommand)
	releaseInputs["domain"] = ""
	releaseInputs["domain_path"] = "/"

	// domains_list 是旧步骤的兼容输入；新执行器也可以直接消费它。
	releaseInputs["domains_list"] = "[]"

	// 多域名支持：优先读取 app_config_domains，存在则额外下发 domains（JSON 字符串）+ domains_list（聚合结构）
	if appConfig.ConfigID > 0 {
		rows, err := fetchAppConfigDomains(appConfig.ConfigID)
		if err != nil {
			return nil, nil, fmt.Errorf("查询多域名配置失败：%s", err)
		}
		// 从 app_config_domains 读取并聚合为 domains_list（同 host 合并 paths）
		{
			list := groupDomainsListFromRows(rows)
			b, err := json.Marshal(list)
			if err != nil {
				return nil, nil, fmt.Errorf("序列化 domains_list 失败：%s", err)
			}
			releaseInputs["domains_list"] = string(b)
		}
		domains := normalizeIngressDomains(rows)
		if len(domains) > 0 {
			b, err := json.Marshal(domains)
			if err != nil {
				return nil, nil, fmt.Errorf("序列化多域名配置失败：%s", err)
			}
			releaseInputs["domains"] = string(b)
		}
	}
	releaseInputs["image"] = image
	releaseInputs["dev_language"] = app.DevLanguage

	// 透传 extra_data：只要有值就合并进发布输入，且不覆盖核心字段。
	if req.ExtraData != nil {
		if err := validateReleaseExtraData(req.ExtraData, "extra_data"); err != nil {
			return nil, nil, err
		}
		// stringify 将任意值转换为 string，复杂类型优先 JSON 序列化
		stringify := func(v any) (string, bool) {
			if v == nil {
				return "", false
			}
			if s, ok := v.(string); ok {
				if s == "" {
					return "", false
				}
				return s, true
			}
			if b, err := json.Marshal(v); err == nil {
				ss := string(b)
				if ss == "" || ss == "null" {
					return "", false
				}
				return ss, true
			}
			// 兜底：转字符串
			ss := fmt.Sprint(v)
			if ss == "" {
				return "", false
			}
			return ss, true
		}

		mergeKV := func(k string, v any) {
			if k == "" {
				return
			}
			if _, exists := releaseInputs[k]; exists {
				return // 不覆盖现有 key
			}
			if s, ok := stringify(v); ok {
				releaseInputs[k] = s
			}
		}

		for k, v := range req.ExtraData {
			mergeKV(k, v)
		}
	}

	jsonStr, err := tool.ToJSON(releaseInputs)
	if err != nil {
		return nil, nil, err
	}
	taskRecord := &entity.TaskRecord{
		AppName:        app.AppName,
		RundeckAppName: app.RundeckAppName, // 现在是指针类型，可以直接赋值
		Publisher:      req.Publisher,
		Branch:         req.Branch,
		Env:            req.Env,
		PipelineParam:  json.RawMessage(jsonStr),
		Products:       image,
		Status:         workflow.TaskQueued,
		Steps:          make([]entity.TaskStepRecord, 0),
		AppletImages:   make([]entity.AppletImage, 0),
	}
	return releaseInputs, taskRecord, nil
}

func (pm *PublishManager) JobStatus() ([]*entity.TaskRecord, error) {
	var taskRecords []*entity.TaskRecord

	err := db.Engine.
		Where("status IN (?, ?, ?, ?, ?) AND deleted_at IS NULL",
			entity.StatusInit, entity.StatusPackaging, entity.StatusDeploying,
			workflow.TaskQueued, workflow.TaskRunning).
		Find(&taskRecords)

	if err != nil {
		return nil, fmt.Errorf("查询任务状态失败：%s", err)
	}

	for _, record := range taskRecords {
		normalizeTaskRecordNullableText(record)
	}

	// 添加日志记录
	slog.Info("查询任务状态",
		"count", len(taskRecords),
		"statuses", []string{entity.StatusInit, entity.StatusPackaging, entity.StatusDeploying, workflow.TaskQueued, workflow.TaskRunning})

	return taskRecords, nil
}

// GetTaskRecordDetails 获取任务详情
func (pm *PublishManager) GetTaskRecordDetails(taskID int) (*entity.TaskRecord, error) {
	var taskRecord entity.TaskRecord

	has, err := db.Engine.Where("task_id = ?", taskID).And("deleted_at IS NULL").Get(&taskRecord)
	if err != nil {
		return nil, fmt.Errorf("查询任务详情失败：%s", err)
	}
	if !has {
		return nil, fmt.Errorf("未找到任务详情，task_id: %d", taskID)
	}
	normalizeTaskRecordNullableText(&taskRecord)
	if taskRecord.EngineVersion >= 2 {
		taskRecord.Steps, err = release.Shared().Store.ListTaskSteps(context.Background(), taskID)
		if err != nil {
			return nil, fmt.Errorf("查询任务步骤失败：%s", err)
		}
	}

	// 回填任务图片（方案B：图片表），保持前端好处理：没数据返回空数组而不是 null
	imgMap, err := fetchTaskRecordImagesByTaskIDs([]int{taskID})
	if err != nil {
		return nil, fmt.Errorf("查询任务图片失败：%s", err)
	}
	if imgs, ok := imgMap[taskID]; ok {
		taskRecord.AppletImages = imgs
	} else {
		taskRecord.AppletImages = make([]entity.AppletImage, 0)
	}

	slog.Info("查询任务详情成功", "task_id", taskID)
	return &taskRecord, nil
}
