package app

import (
	"ares/internal/api/util"
	"ares/internal/db"
	"ares/internal/entity"
	"context"
	"fmt"
	"log/slog"
)

// AppManager 应用管理器
type AppManager struct {
	utilManager *util.ParamPage
}

// NewAppManager 创建新的应用管理器
func NewAppManager() *AppManager {
	return &AppManager{
		utilManager: util.NewUtilManager(),
	}
}

type CreateAppRequest struct {
	AppName       string `json:"app_name"`
	AppNameCN     string `json:"app_name_cn"`
	Owner         string `json:"owner"`
	OwnerCN       string `json:"owner_cn"`
	DevLanguage   string `json:"dev_language"`
	DescriptionCN string `json:"description_cn"`
	GitUrl        string `json:"git_url"`
}

// CreateAppsRequest 批量创建应用请求
type CreateAppsRequest struct {
	Apps []CreateAppRequest `json:"apps"`
}

// CreateAppResult 表示单个应用创建的结果
type CreateAppResult struct {
	App     *entity.Apps `json:"app"`
	Error   string       `json:"error"`
	Success bool         `json:"success"`
}

// CreateAppsResponse 批量创建应用的响应
type CreateAppsResponse struct {
	Apps         []CreateAppResult `json:"apps"`
	SuccessCount int               `json:"success_count"` // 成功数量
	FailureCount int               `json:"failure_count"` // 失败数量
	TotalCount   int               `json:"total_count"`
}

// CreateApp 创建单应用
func (am *AppManager) CreateApp(ctx context.Context, req CreateAppRequest) (*CreateAppResult, error) {
	// 使用验证器模式进行业务规则校验
	validator := NewAppValidator()
	if err := validator.ValidateCreateApp(&req); err != nil {
		return nil, NewValidationError("业务规则校验失败" + err.Error())
	}

	// 使用事务确保数据一致性
	appResult, err := am.CreateAppWithTx(ctx, &req)
	if err != nil {
		return nil, err
	}
	result := CreateAppResult{
		Success: err == nil,
		App:     appResult,
	}

	// 异步触发后续流程
	go am.TriggerPostCreateHooks(appResult)

	return &result, nil
}

// CreateApps 批量创建应用
func (am *AppManager) CreateApps(ctx context.Context, req CreateAppsRequest) (*CreateAppsResponse, error) {
	response := &CreateAppsResponse{
		TotalCount:   len(req.Apps),
		SuccessCount: 0,
		FailureCount: 0,
		Apps:         make([]CreateAppResult, len(req.Apps)),
	}

	for i, appReq := range req.Apps {
		app, err := am.CreateApp(ctx, appReq)
		if err != nil {
			response.FailureCount++
			response.Apps[i].Error = fmt.Sprintf("应用 %s 创建失败，失败原因：", appReq.AppName) + err.Error()
		}
		if app != nil {
			response.Apps[i] = *app
			response.SuccessCount++
		}
	}

	return response, nil
}

// CreateAppWithTx 使用事务创建应用
func (am *AppManager) CreateAppWithTx(ctx context.Context, req *CreateAppRequest) (*entity.Apps, error) {
	// 检查应用是否已存在
	exists, err := am.checkAppExists(ctx, req.AppName)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, NewDuplicateAppError(req.AppName)
	}
	// 开启事务
	session := db.Engine.NewSession()
	defer session.Close()

	session = session.Context(ctx)

	if err := session.Begin(); err != nil {
		return nil, err
	}
	// 创建应用
	app := &entity.Apps{
		AppName:       req.AppName,
		AppNameCn:     req.AppNameCN,
		Owner:         req.Owner,
		OwnerCN:       req.OwnerCN,
		DevLanguage:   req.DevLanguage,
		DescriptionCN: req.DescriptionCN,
		GitUrl:        req.GitUrl,
	}
	// 保存到数据库
	if _, err := session.Insert(app); err != nil {
		session.Rollback()
		return nil, err
	}

	// 提交事务
	if err := session.Commit(); err != nil {
		return nil, err
	}

	slog.Info("应用创建成功",
		"app_id", app.AppId,
		"app_name", app.AppName)

	return app, nil
}

// checkAppExists 检查应用是否已存在
func (am *AppManager) checkAppExists(ctx context.Context, appName string) (bool, error) {
	session := db.Engine.Context(ctx)
	count, err := session.Where("app_name = ? AND deleted_at IS NULL", appName).Count(&entity.Apps{})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// TriggerPostCreateHooks 触发应用创建后的钩子函数
func (am *AppManager) TriggerPostCreateHooks(app *entity.Apps) {
	slog.Info("触发应用创建后处理", "app_id", app.AppId, "app_name", app.AppName)

	// 可以在这里实现各种后续处理逻辑
	// 1. 创建默认配置
	am.createDefaultConfig(app)

	// 2. 初始化CI/CD流水线
	//am.initCIPipeline(app)

	// 3. 发送通知
	//am.sendNotification(app)
}

// 创建默认配置
func (am *AppManager) createDefaultConfig(app *entity.Apps) {
	slog.Info("为应用创建默认配置", "app_id", app.AppId, "app_name", app.AppName)
	// 实现创建默认配置的逻辑
	environments := []string{"dev", "test", "moni"}

	// 优先使用 DB 规则表的 default；若缺失则回退老逻辑
	defaultCodePackageType := getDefaultPackageType(app.DevLanguage)
	if rules, err := loadDevLanguageRules(context.Background(), app.DevLanguage); err == nil && rules != nil {
		defaultCodePackageType = rules.Default
	}

	// 为每个环境创建默认配置
	for _, env := range environments {
		// 创建应用环境配置
		appConfig := &entity.AppConfigs{
			AppID:                  app.AppId,
			Env:                    env,
			CodePackageType:        defaultCodePackageType,
			CodePackageName:        "NULL",
			CodePackagePath:        "NULL",
			BaseImage:              "NULL",
			PodCount:               1,
			LimitsMemory:           2,
			GpuCount:               0,
			ProbeType:              "HTTP",
			ProbeCheckPath:         "/ttpai/inside/checkup",
			ProbeCheckTcpPort:      8080,
			ProbeCheckHttpPort:     8080,
			ProbeStopCheckHttpPort: 8080,
			ContainerPort:          8080,
			PreStopType:            "HTTP",
			PreStopCheckPath:       "/ttpai/inside/prestop",
			PreStopCommand:         "NULL",
		}

		session := db.Engine.NewSession()
		defer session.Close()

		// 开启事务
		if err := session.Begin(); err != nil {
			slog.Error("开启事务失败", "error", err)
			continue
		}

		// 创建应用配置
		if _, err := session.Insert(appConfig); err != nil {
			slog.Error("创建应用配置失败",
				"app_id", app.AppId,
				"env", env,
				"error", err)
			session.Rollback()
			continue
		}

		if err := session.Commit(); err != nil {
			slog.Error("提交事务失败", "error", err)
			continue
		}

		slog.Info("创建应用环境配置成功",
			"app_id", app.AppId,
			"app_name", app.AppName,
			"env", env)
	}
}

// initCIPipeline 初始化CI流水线
func (am *AppManager) initCIPipeline(app *entity.Apps) {
	slog.Info("为应用初始化CI流水线", "app_id", app.AppId)
	// 实现初始化CI流水线的逻辑
}

// sendNotification 发送通知
func (am *AppManager) sendNotification(app *entity.Apps) {
	slog.Info("发送应用创建通知", "app_id", app.AppId)
	// 实现发送通知的逻辑
}

// 根据开发语言获取默认的包类型
func getDefaultPackageType(language string) string {
	switch language {
	case "java":
		return "jar"
	case "golang":
		return "golang"
	case "python":
		return "python"
	case "node.js":
		return "node.js"
	default:
		return "NULL"
	}
}
