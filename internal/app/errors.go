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
