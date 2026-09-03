package publish

import (
	"ares/internal/security"
	"fmt"
	"strings"
	"time"
)

func validateReleaseExtraData(value any, path string) error {
	if err := security.ValidateNoSensitiveKeys(value, path); err != nil {
		return fmt.Errorf("%w；发布输入会持久化，请改用外部凭据库或后续 Secret Resolver", err)
	}
	return nil
}

// Validate 验证查询参数
func (q *PublishQuery) Validate() error {
	// 验证时间范围
	if err := q.validateTimeRange(); err != nil {
		return err
	}

	// 验证排序参数
	if err := q.validateSort(); err != nil {
		return err
	}

	return nil
}

// validateTimeRange 验证时间范围
func (q *PublishQuery) validateTimeRange() error {
	if q.parsedStartTime != nil {
		if q.parsedEndTime == nil {
			return fmt.Errorf("结束时间不能为空")
		}
		if q.parsedEndTime.Before(*q.parsedStartTime) {
			return fmt.Errorf("结束时间不能早于开始时间")
		}
		// 可选：添加时间范围限制
		if q.parsedEndTime.Sub(*q.parsedStartTime) > 30*24*time.Hour {
			return fmt.Errorf("时间范围不能超过30天")
		}
	}
	return nil
}

// validateSort 验证排序参数
func (q *PublishQuery) validateSort() error {
	if q.Sort == nil {
		return nil
	}

	validFields := map[string]bool{
		"app_name":   true,
		"env":        true,
		"publisher":  true,
		"branch":     true,
		"created_at": true,
		"updated_at": true,
	}

	if !validFields[q.Sort.Field] {
		return fmt.Errorf("无效的排序字段: %s", q.Sort.Field)
	}

	direction := strings.ToLower(q.Sort.Direction)
	if direction != "asc" && direction != "desc" {
		return fmt.Errorf("无效的排序方向: %s", q.Sort.Direction)
	}

	return nil
}
