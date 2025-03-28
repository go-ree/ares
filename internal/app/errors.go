package app

import "fmt"

// ValidationError 验证错误
type ValidationError struct {
	Message string
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
	return fmt.Sprintf("应用 '%s' 在环境 '%s' 中已存在", e.AppName)
}

// NewDuplicateAppError 创建应用重复错误
func NewDuplicateAppError(appName string) *DuplicateAppError {
	return &DuplicateAppError{
		AppName: appName,
	}
}
