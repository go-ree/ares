package app

import "fmt"

// ValidationError 验证错误
type ValidationError struct {
	Message string
}

// AppNotFoundError 应用未找到错误
type AppNotFoundError struct {
	AppID   int64
	AppName string
}

// AppConfigNotFoundError 表示应用环境配置不存在。
// ConfigID 与 AppID/Env 二选一，用于保留调用方查询时的上下文。
type AppConfigNotFoundError struct {
	ConfigID int
	AppID    int
	Env      string
}

// DuplicateAppConfigError 表示同一应用和环境已经存在有效配置。
type DuplicateAppConfigError struct {
	AppID int
	Env   string
}

// DomainNotFoundError reports a missing, client-selected domain record.
type DomainNotFoundError struct {
	ConfigID int
	DomainID int64
}

// DomainConflictError reports a duplicate host/path pair without exposing a
// database constraint or driver error.
type DomainConflictError struct {
	Host string
	Path string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// NewValidationError 创建验证错误
func NewValidationError(msg string) *ValidationError {
	return &ValidationError{Message: msg}
}

// DuplicateAppError 应用重复错误
type DuplicateAppError struct {
	AppName string
}

func (e *DuplicateAppError) Error() string {
	return fmt.Sprintf("应用名称 '%s 已存在，请重新命名", e.AppName)
}

// NewDuplicateAppError 创建应用重复错误
func NewDuplicateAppError(appName string) *DuplicateAppError {
	return &DuplicateAppError{
		AppName: appName,
	}
}

func (e *AppNotFoundError) Error() string {
	if e.AppID > 0 {
		return fmt.Sprintf("应用ID %d 不存在", e.AppID)
	}
	if e.AppName != "" {
		return fmt.Sprintf("应用名称 '%s' 不存在", e.AppName)
	}
	return "应用不存在"
}

// NewAppNotFoundError 应用未找到错误
func NewAppNotFoundError(appID int64, appName string) *AppNotFoundError {
	return &AppNotFoundError{
		AppID:   appID,
		AppName: appName,
	}
}

func (e *AppConfigNotFoundError) Error() string {
	if e.ConfigID > 0 {
		return fmt.Sprintf("未找到配置，config_id=%d", e.ConfigID)
	}
	return fmt.Sprintf("未找到配置，app_id=%d env=%s", e.AppID, e.Env)
}

// NewAppConfigNotFoundErrorByID 创建按配置 ID 查询失败的错误。
func NewAppConfigNotFoundErrorByID(configID int) *AppConfigNotFoundError {
	return &AppConfigNotFoundError{ConfigID: configID}
}

// NewAppConfigNotFoundErrorByAppEnv 创建按应用和环境查询失败的错误。
func NewAppConfigNotFoundErrorByAppEnv(appID int, env string) *AppConfigNotFoundError {
	return &AppConfigNotFoundError{AppID: appID, Env: env}
}

func (e *DuplicateAppConfigError) Error() string {
	return fmt.Sprintf("配置已存在：app_id=%d env=%s", e.AppID, e.Env)
}

// NewDuplicateAppConfigError 创建应用环境配置冲突错误。
func NewDuplicateAppConfigError(appID int, env string) *DuplicateAppConfigError {
	return &DuplicateAppConfigError{AppID: appID, Env: env}
}

func (e *DomainNotFoundError) Error() string {
	return fmt.Sprintf("未找到域名记录：config_id=%d domain_id=%d", e.ConfigID, e.DomainID)
}

func NewDomainNotFoundError(configID int, domainID int64) *DomainNotFoundError {
	return &DomainNotFoundError{ConfigID: configID, DomainID: domainID}
}

func (e *DomainConflictError) Error() string {
	return fmt.Sprintf("多域名配置冲突：host=%s path=%s", e.Host, e.Path)
}

func NewDomainConflictError(host, path string) *DomainConflictError {
	return &DomainConflictError{Host: host, Path: path}
}
