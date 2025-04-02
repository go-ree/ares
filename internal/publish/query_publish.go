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
	Env       string `json:"env" form:"env"`
	Publisher string `json:"publisher" form:"publisher"`
	Branch    string `json:"branch" form:"branch"`
	util.ParamPage
	StartTime time.Time `json:"start_time" form:"start_time" time_format:"2006-01-02 15:04:05"` // 开始时间
	EndTime   time.Time `json:"end_time" form:"end_time" time_format:"2006-01-02 15:04:05"`     // 结束时间
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
	if !query.StartTime.IsZero() {
		session = session.And("created_at >= ?", query.StartTime)
	}
	if !query.EndTime.IsZero() {
		session = session.And("updated_at <= ?", query.EndTime)
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
		session = session.Where("app_name LIKE ?", "%"+params.AppName+"%")
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
	// 参数验证
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("参数验证失败: %w", err)
	}

	slog.Info("查询构建任务列表",
		"app_name", params.AppName,
		"env", params.Env,
		"publisher", params.Publisher,
		"branch", params.Branch,
		"start_time", params.StartTime.Format("2006-01-02 15:04:05"),
		"end_time", params.EndTime.Format("2006-01-02 15:04:05"))

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
	if totalPages > 0 && params.PageNum > totalPages {
		return nil, fmt.Errorf("请求的页码 %d 超出总页数 %d", params.PageNum, totalPages)
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
			"page_num", params.PageNum,
			"page_size", pageSize,
			"offset", offset)
	}

	// 构建返回结果
	result := &PublishQueryResult{
		Total:      total,
		TaskRecord: taskRecord,
		PageNum:    params.PageNum,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}

	return result, nil
}
