package publish

import (
	"ares/internal/api/util"
	"ares/internal/db"
	"ares/internal/entity"
	"context"
	"fmt"
	"log/slog"
	"time"
	"xorm.io/xorm"
)

type PublishQuery struct {
	AppName   string `json:"app_name" form:"app_name"`
	IsRundeck bool   `json:"is_rundeck"`
	Env       string `json:"env" form:"env"`
	Publisher string `json:"publisher" form:"publisher"`
	Branch    string `json:"branch" form:"branch"`
	Status    string `json:"status" form:"status"`
	util.ParamPage
	StartTime string `json:"start_time" form:"start_time"` // 开始时间（字符串格式）
	EndTime   string `json:"end_time" form:"end_time"`     // 结束时间（字符串格式）
	// 解析后的时间字段
	parsedStartTime *time.Time
	parsedEndTime   *time.Time
}

// PublishQueryResult
type PublishQueryResult struct {
	Total      int64               `json:"total"`       // 总记录数
	PageNum    int                 `json:"page_num"`    // 当前页码
	PageSize   int                 `json:"page_size"`   // 每页大小
	TotalPages int                 `json:"total_pages"` // 总页数
	TaskRecord []entity.TaskRecord `json:"task_record"` // 构建任务列表
}

// buildTimeRangeQuery 在查询构建时使用
// 查询时间范围为：查询的开始时间要比创建时间小，查询的结束时间要比更新时间大
func (pm *PublishManager) buildTimeRangeQuery(session *xorm.Session, query *PublishQuery) *xorm.Session {
	if query.parsedStartTime != nil {
		session = session.And("created_at >= ?", *query.parsedStartTime)
	}
	if query.parsedEndTime != nil {
		session = session.And("updated_at <= ?", *query.parsedEndTime)
	}
	return session
}

// buildPublishQuery 构建构建任务查询Session
func (pm *PublishManager) buildPublishQuery(ctx context.Context, params PublishQuery) *xorm.Session {
	session := db.Engine.Context(ctx)

	// 只查询未删除的
	session = session.Where("deleted_at IS NULL")

	// 构建查询条件
	if params.AppName != "" {
		if params.IsRundeck {
			session = session.Where("rundeck_app_name IS ?", "%"+params.AppName+"%")
		} else {
			session = session.Where("app_name IS ?", "%"+params.AppName+"%")
		}
	}
	if params.Env != "" {
		session = session.Where("env = ?", params.Env)
	}
	if params.Publisher != "" {
		session = session.Where("publisher = ?", params.Publisher)
	}
	if params.Branch != "" {
		session = session.Where("branch LIKE ?", "%"+params.Branch+"%")
	}
	if params.Status != "" {
		session = session.Where("status = ?", params.Status)
	}

	// 添加时间范围查询
	session = pm.buildTimeRangeQuery(session, &params)

	// 应用排序
	session = pm.taskSorting(session, &params)

	return session
}

// taskSorting 构建任务排序条件
func (pm *PublishManager) taskSorting(session *xorm.Session, params *PublishQuery) *xorm.Session {
	if params.Sort == nil {
		// 默认排序
		return session.OrderBy("created_at DESC")
	}

	// 验证排序字段是否合法
	validFields := map[string]string{
		"app_name":   "app_name",
		"env":        "env",
		"publisher":  "publisher",
		"branch":     "branch",
		"created_at": "created_at",
		"updated_at": "updated_at",
	}

	if field, ok := validFields[params.Sort.Field]; ok {
		direction := "ASC"
		if params.Sort.Direction == "desc" {
			direction = "DESC"
		}
		return session.OrderBy(fmt.Sprintf("%s %s", field, direction))
	}
	// 如果排序字段不合法，使用默认排序
	return session.OrderBy("created_at DESC")
}

// QueryBuildPublish 查询构建任务列表
func (pm *PublishManager) QueryBuildPublish(ctx context.Context, params PublishQuery) (*PublishQueryResult, error) {
	// 时间格式兼容处理
	if err := pm.parseTimeFormats(&params); err != nil {
		return nil, fmt.Errorf("时间格式解析失败: %w", err)
	}

	// 参数验证
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("参数验证失败: %w", err)
	}

	slog.Info("查询构建任务列表",
		"app_name", params.AppName,
		"is_rundeck", params.IsRundeck,
		"env", params.Env,
		"publisher", params.Publisher,
		"branch", params.Branch,
		"status", params.Status,
		"start_time", params.StartTime,
		"end_time", params.EndTime,
		"parsed_start_time", func() string {
			if params.parsedStartTime != nil {
				return params.parsedStartTime.Format("2006-01-02 15:04:05")
			}
			return "nil"
		}(),
		"parsed_end_time", func() string {
			if params.parsedEndTime != nil {
				return params.parsedEndTime.Format("2006-01-02 15:04:05")
			}
			return "nil"
		}())

	// 构建查询条件
	query := pm.buildPublishQuery(ctx, params)

	// 先获取总数
	total, err := query.Count(&entity.TaskRecord{})
	if err != nil {
		return nil, err
	}

	// 规范化分页参数
	pageSize, offset := pm.utilManager.NormalizePagination(&params.ParamPage)

	// 计算总页数
	totalPages := pm.utilManager.CalculateTotalPages(total, pageSize)

	// 验证页码是否合法
	pageNum := params.GetPageNum()
	if totalPages > 0 && pageNum > totalPages {
		return nil, fmt.Errorf("请求的页码 %d 超出总页数 %d", pageNum, totalPages)
	}

	// 如果有数据，再进行分页查询
	var taskRecord []entity.TaskRecord
	if total > 0 {
		// 重新构建查询，因为Count会消耗query
		dataQuery := pm.buildPublishQuery(ctx, params)

		// 执行分页查询
		err = dataQuery.Limit(pageSize, offset).Find(&taskRecord)
		if err != nil {
			return nil, err
		}

		// 添加日志
		slog.Info("查询到构建历史数据",
			"total", total,
			"actual_count", len(taskRecord),
			"page_num", params.GetPageNum(),
			"page_size", pageSize,
			"offset", offset)

		// 批量回填任务图片：仅新增字段 applet_images，不影响原有字段
		taskIDs := make([]int, 0, len(taskRecord))
		for i := range taskRecord {
			taskIDs = append(taskIDs, taskRecord[i].TaskId)
		}
		imgMap, err := fetchTaskRecordImagesByTaskIDs(taskIDs)
		if err != nil {
			return nil, err
		}
		for i := range taskRecord {
			if imgs, ok := imgMap[taskRecord[i].TaskId]; ok {
				taskRecord[i].AppletImages = imgs
			} else {
				// 保持前端好处理：没数据返回空数组而不是 null
				taskRecord[i].AppletImages = make([]entity.AppletImage, 0)
			}
		}
	}

	// 构建返回结果
	result := &PublishQueryResult{
		Total:      total,
		TaskRecord: taskRecord,
		PageNum:    params.GetPageNum(),
		PageSize:   pageSize,
		TotalPages: totalPages,
	}

	return result, nil
}

// parseTimeSmart 智能解析时间，支持多种格式
func (pm *PublishManager) parseTimeSmart(timeStr string) (*time.Time, error) {
	if timeStr == "" {
		return nil, nil
	}

	// 支持的时间格式（按优先级排序）
	timeFormats := []string{
		time.RFC3339,                    // "2025-07-23T16:00:00Z"
		"2006-01-02T15:04:05.000Z",      // "2025-07-23T16:00:00.000Z"
		"2006-01-02T15:04:05Z",          // "2025-07-23T16:00:00Z"
		"2006-01-02T15:04:05.000-07:00", // "2025-07-23T16:00:00.000+08:00"
		"2006-01-02T15:04:05-07:00",     // "2025-07-23T16:00:00+08:00"
		"2006-01-02 15:04:05",           // "2025-07-23 16:00:00"
		"2006-01-02 15:04",              // "2025-07-23 16:00"
		"2006-01-02",                    // "2025-07-23"
	}

	// 尝试解析
	for _, format := range timeFormats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return &t, nil
		}
	}

	return nil, fmt.Errorf("无法解析时间格式: %s", timeStr)
}

// parseTimeFormats 解析时间格式，支持多种格式兼容
func (pm *PublishManager) parseTimeFormats(params *PublishQuery) error {
	// 解析开始时间
	if params.StartTime != "" {
		parsedTime, err := pm.parseTimeSmart(params.StartTime)
		if err != nil {
			return fmt.Errorf("解析开始时间失败: %w", err)
		}
		params.parsedStartTime = parsedTime
	}

	// 解析结束时间
	if params.EndTime != "" {
		parsedTime, err := pm.parseTimeSmart(params.EndTime)
		if err != nil {
			return fmt.Errorf("解析结束时间失败: %w", err)
		}
		params.parsedEndTime = parsedTime
	}

	return nil
}
