package app

import (
	"fmt"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/entity"
	"reflect"
	"strings"
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

func (am *AppManager) CreateApp(req *CreateAppRequest) (*entity.Apps, error) {
	// 添加输入验证
	if err := validateStruct(req); err != nil {
		return nil, err
	}
	app := &entity.Apps{
		AppName:       req.AppName,
		AppNameCn:     req.AppNameCN,
		Owner:         req.Owner,
		OwnerCN:       req.OwnerCN,
		DevLanguage:   req.DevLanguage,
		DescriptionCN: req.DescriptionCN,
		GitUrl:        req.GitUrl,
	}
	if req.AppName == "张三" {
		return nil, fmt.Errorf("张三有问题")
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

// validateStruct 通用的结构体验证函数
func validateStruct(s interface{}) error {
	if s == nil {
		return fmt.Errorf("请求不能为空")
	}

	v := reflect.ValueOf(s)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()
	var emptyFields []string
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() == reflect.String && field.String() == "" {
			// 获取json标签
			jsonTag := t.Field(i).Tag.Get("json")
			// 处理json标签，去除可能存在的选项（如 omitempty）
			jsonField := strings.Split(jsonTag, ",")[0]
			if jsonField != "" {
				emptyFields = append(emptyFields, jsonField)
			}
		}
	}

	if len(emptyFields) > 0 {
		return fmt.Errorf("以下字段不能为空: %s", strings.Join(emptyFields, ", "))
	}

	return nil
}

// isValidGitURL 验证Git URL的格式
func isValidGitURL(url string) bool {
	// 这里可以根据实际需求添加更复杂的URL验证逻辑
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "git@")
}
