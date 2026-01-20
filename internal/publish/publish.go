package publish

import (
	"ares/internal/api/util"
	"ares/internal/db"
	"ares/internal/entity"
	"ares/internal/jenkins"
	"ares/internal/tool"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// PublishManager 发布管理器
type PublishManager struct {
	utilManager *util.ParamPage
}

// NewPublishManager 创建新的发布管理器
func NewPublishManager() *PublishManager {
	return &PublishManager{
		utilManager: util.NewUtilManager(),
	}
}

// CreatePublishRequest 触发发布动作所需的请求参数
type CreatePublishRequest struct {
	AppName   string `json:"app_name"`
	Branch    string `json:"branch"`
	Env       string `json:"env"`
	Publisher string `json:"publisher"`
	IsRundeck bool   `json:"is_rundeck"`
	ExtraData any    `json:"extra_data,omitempty"` // 新增：接口类型
}

// PublishRequest 实际发布需要用到的参数
type PublishRequest struct {
	AppName         string  `json:"app_name"`
	RundeckAppName  *string `json:"rundeck_app_name"`
	Branch          string  `json:"branch"`
	Env             string  `json:"env"`
	Publisher       string  `json:"publisher"`
	AppId           int     `json:"app_id"`
	CodePackageType string  `json:"code_package_type"`
	ExtraData       any     `json:"extra_data,omitempty"` // 新增：接口类型
}

// CreateBatchPublishRequest 批量触发发布动作请求
type CreateBatchPublishRequest struct {
	BatchPublish []CreatePublishRequest `json:"batch_publish"`
}

// CreatePublishResult 表示单次应用发布动作的结果
type CreatePublishResult struct {
	TaskRecord *entity.TaskRecord `json:"task_record"`
	Error      string             `json:"error"`
	Success    bool               `json:"success"`
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

// CreatePublish 创建单次发布动作
func (pm *PublishManager) CreatePublish(creatReq *CreatePublishRequest) (*entity.TaskRecord, error) {
	// 验证所需的参数信息是否完成且不为空
	err := tool.ValidateStruct(creatReq)
	if err != nil {
		return nil, err
	}

	req := &PublishRequest{
		AppName:   creatReq.AppName,
		Branch:    creatReq.Branch,
		Env:       creatReq.Env,
		Publisher: creatReq.Publisher,
		ExtraData: creatReq.ExtraData,
		// RundeckAppName 将在验证后设置
	}
	// 这里打个补丁，为了兼容非标准的 ceshi 环境，将其转换为test环境
	if req.Env == "ceshi" {
		req.Env = "test"
	}
	var app *entity.Apps

	if creatReq.IsRundeck {
		app, err = pm.VerifyRunDeckApp(req)
		// 设置 RundeckAppName
		if app != nil && app.RundeckAppName != nil {
			req.RundeckAppName = app.RundeckAppName
		}
	} else {
		app, err = pm.VerifyApp(req)
	}

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
	jenkinsParam, taskRecordResult, err := pm.ComposePublishData(req, app, appConfig, envConfigs)
	if err != nil {
		return nil, err
	}

	// 这里需要传入一个任务id，因为上面已经拿到返回值了，直接在这里传入即可
	jenkinsParam["task_id"] = strconv.Itoa(taskRecordResult.TaskId)

	// 对相应的管线创建新的构建任务
	jobBuildId, _, err := jenkins.CreateBuildTask(pipelines.CiJobName, jenkinsParam)
	if err != nil {
		return nil, err
	}
	var taskRecord entity.TaskRecord
	// 回写数据库中的数据，将编译阶段产生的taskId回写到表中
	taskRecord.TaskId = taskRecordResult.TaskId
	taskRecord.CiBuildId = jobBuildId
	taskRecord.Status = "packaging"
	taskRecord.CiJobName = pipelines.CiJobName
	taskRecord.CdJobName = pipelines.CdJobName
	// 更新指定id的数据
	affected, err := db.Engine.ID(taskRecord.TaskId).Update(&taskRecord)
	if err != nil {
		return nil, fmt.Errorf("ci阶段taskId回写失败：%s", err)
	}
	// 检查更新结果
	if affected == 0 {
		return nil, fmt.Errorf("任务记录未更新，可能记录不存在，ID: %d", taskRecord.TaskId)
	}
	slog.Info("ci阶段taskId回写成功",
		"task_id", taskRecord.TaskId,
		"affected_rows", affected,
		"taskRecord", taskRecord)

	// 如果需要获取最新的记录（包括可能的数据库触发器更新等）
	var updatedRecord entity.TaskRecord
	exists, err := db.Engine.ID(taskRecord.TaskId).Get(&updatedRecord)
	if err != nil {
		return nil, fmt.Errorf("获取更新后的记录失败：%v", err)
	}
	if !exists {
		return nil, fmt.Errorf("更新后的记录未找到，ID: %d", taskRecord.TaskId)
	}
	return &updatedRecord, nil
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
func (pm *PublishManager) ComposePublishData(req *PublishRequest, app *entity.Apps, appConfig *entity.AppConfigs, envConfig *entity.EnvConfigs) (map[string]string, *entity.TaskRecord, error) {
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
	JenkinsParam["gpu_count"] = strconv.Itoa(appConfig.GpuCount)
	JenkinsParam["probe_type"] = appConfig.ProbeType
	JenkinsParam["probe_check_path"] = appConfig.ProbeCheckPath
	JenkinsParam["pre_stop_type"] = appConfig.PreStopType
	JenkinsParam["pre_stop_check_path"] = appConfig.PreStopCheckPath
	JenkinsParam["pre_stop_command"] = appConfig.PreStopCommand
	JenkinsParam["domain"] = "NULL"
	JenkinsParam["domain_path"] = "/"

	// domains_list（Jenkins 透传）：仅使用 app_config_domains（domain/domain_path 已废弃）
	JenkinsParam["domains_list"] = "[]"

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
			JenkinsParam["domains_list"] = string(b)
		}
		domains := normalizeIngressDomains(rows)
		if len(domains) > 0 {
			b, err := json.Marshal(domains)
			if err != nil {
				return nil, nil, fmt.Errorf("序列化多域名配置失败：%s", err)
			}
			JenkinsParam["domains"] = string(b)
		}
	}
	JenkinsParam["image"] = image
	JenkinsParam["dev_language"] = app.DevLanguage

	// 透传 extra_data：只要有值就合并进 JenkinsParam，且不覆盖现有 key
	if req.ExtraData != nil {
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
			if _, exists := JenkinsParam[k]; exists {
				return // 不覆盖现有 key
			}
			if s, ok := stringify(v); ok {
				JenkinsParam[k] = s
			}
		}

		switch extra := req.ExtraData.(type) {
		case map[string]any:
			for k, v := range extra {
				mergeKV(k, v)
			}
		case map[string]string:
			for k, v := range extra {
				mergeKV(k, v)
			}
		default:
			// 非 map 类型无法展开为多个键：整体透传为 extra_data（同样不覆盖）
			mergeKV("extra_data", extra)
		}
	}

	jsonStr, err := tool.ToJSON(JenkinsParam)
	if err != nil {
		return nil, nil, err
	}
	//fmt.Println(jsonStr)
	// 先直接在数据库中写入数据，此时记录状态信息
	// 然后携带相关的发布信息，对jenkins发送api请求，jenkins会返回对应的build_number，这个用于查询执行状态和获取日志信息。
	//
	taskRecord := &entity.TaskRecord{
		AppName:        app.AppName,
		RundeckAppName: app.RundeckAppName, // 现在是指针类型，可以直接赋值
		Publisher:      req.Publisher,
		Branch:         req.Branch,
		Env:            req.Env,
		PipelineParam:  json.RawMessage(jsonStr),
		Products:       image,
		Status:         "init",
	}
	// 需要在这里显式的把一些有默认值的给排除掉，golang会将未使用的值赋值为零值，而不是使用默认值
	_, err = db.Engine.Omit("ci_build_id", "cd_build_id", "message", "ci_job_name", "cd_job_name", "auto_deploy").Insert(taskRecord)
	if err != nil {
		return nil, nil, fmt.Errorf("任务记录创建失败: %s", err)
	}
	slog.Info("Jenkins构建任务记录创建成功",
		"task_id", taskRecord.TaskId,
		"taskRecord", taskRecord)
	return JenkinsParam, taskRecord, nil
}

func (pm *PublishManager) JobStatus() ([]*entity.TaskRecord, error) {
	var taskRecords []*entity.TaskRecord

	// 计算3小时前的时间点
	threeHoursAgo := time.Now().Add(-3 * time.Hour)

	err := db.Engine.
		Where("status IN (?, ?, ?)", entity.StatusInit, entity.StatusPackaging, entity.StatusDeploying).
		And("created_at > ?", threeHoursAgo).
		Find(&taskRecords)

	if err != nil {
		return nil, fmt.Errorf("查询任务状态失败：%s", err)
	}

	// 检查 taskRecords 是否为 nil
	if taskRecords != nil {
		// 使用有效的 JSON 字符串
		hiddenMessage := json.RawMessage(`{"message": "隐藏详情，减少数据量"}`)
		// 清空 PipelineParam 字段
		for _, record := range taskRecords {
			record.PipelineParam = hiddenMessage
		}
	}

	// 添加日志记录
	slog.Info("查询任务状态",
		"count", len(taskRecords),
		"time_range", fmt.Sprintf("获取最近3小时数据，构建状态为init、packaging、deploying的数据(%s)", threeHoursAgo.Format("2006-01-02 15:04:05")))

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
