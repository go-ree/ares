package app

import (
	"ares/internal/entity"
)

// AppManager 应用管理器
type AppManager struct {
}

// NewAppManager 创建新的应用管理器
func NewAppManager() *AppManager {
	return &AppManager{}
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

// CreateApp 创建单个应用
func (am *AppManager) CreateApp(req *CreateAppRequest) (*entity.Apps, error) {

	app := &entity.Apps{
		AppName:       req.AppName,
		AppNameCn:     req.AppNameCN,
		Owner:         req.Owner,
		OwnerCN:       req.OwnerCN,
		DevLanguage:   req.DevLanguage,
		DescriptionCN: req.DescriptionCN,
		GitUrl:        req.GitUrl,
	}
	return app, nil
}

func (am *AppManager) CreateApps(req *CreateAppsRequest) (*CreateAppsResponse, error) {
	response := &CreateAppsResponse{
		Apps: make([]CreateAppResult, len(req.Apps)),
	}
	response.TotalCount = len(req.Apps)

	for i, appReq := range req.Apps {
		app, err := am.CreateApp(&appReq)
		result := CreateAppResult{
			Success: err == nil,
			App:     app,
		}
		if err != nil {
			result.Error = err.Error()
			response.FailureCount++
		} else {
			response.SuccessCount++
		}
		response.Apps[i] = result
	}

	return response, nil
}
