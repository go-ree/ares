package app

import (
	"ares/internal/api/util"
	"ares/internal/db"
	"ares/internal/entity"
	"context"
	"fmt"
	"log/slog"
	"xorm.io/xorm"
)

// AppQuery 应用查询参数
type AppQuery struct {
	AppID       int64  `json:"app_id" form:"app_id"`
	AppName     string `json:"app_name" form:"app_name"`
	DevLanguage string `json:"dev_language" form:"dev_language"`
	Owner       string `json:"owner" form:"owner"`
	OwnerCn     string `json:"owner_cn" form:"owner_cn"`
	util.ParamPage
}

// AppQueryResult 应用查询结果
type AppQueryResult struct {
	Total      int64         `json:"total"`       // 总记录数
	PageNum    int           `json:"page_num"`    // 当前页码
	PageSize   int           `json:"page_size"`   // 每页大小
	TotalPages int           `json:"total_pages"` // 总页数
	Apps       []entity.Apps `json:"apps"`        // 应用列表

}

// buildAppQuery 构建应用查询Session
func (am *AppManager) buildAppQuery(ctx context.Context, params AppQuery) *xorm.Session {
	session := db.Engine.Context(ctx)

	// 只查询未删除的应用
	session = session.Where("deleted_at IS NULL")

	// 构建查询条件
	if params.AppID > 0 {
		session = session.Where("app_id = ?", params.AppID)
	}
	if params.AppName != "" {
		session = session.Where("app_name LIKE ?", "%"+params.AppName+"%")
	}
	if params.DevLanguage != "" {
		session = session.Where("dev_language = ?", params.DevLanguage)
	}
	if params.Owner != "" {
		session = session.Where("owner = ?", params.Owner)
	}
	if params.OwnerCn != "" {
		session = session.Where("owner_cn = ?", params.OwnerCn)
	}

	// 应用排序
	session = am.applySorting(session, &params)

	return session
}

// applySorting 应用排序条件
func (am *AppManager) applySorting(session *xorm.Session, params *AppQuery) *xorm.Session {
	if params.Sort == nil {
		// 默认排序
		return session.OrderBy("app_id DESC")
	}

	// 验证排序字段是否合法
	validFields := map[string]string{
		"app_id":       "app_id",
		"app_name":     "app_name",
		"created_at":   "created_at",
		"updated_at":   "updated_at",
		"dev_language": "dev_language",
		"owner":        "owner",
		"owner_cn":     "owner_cn",
	}

	if field, ok := validFields[params.Sort.Field]; ok {
		direction := "ASC"
		if params.Sort.Direction == "desc" {
			direction = "DESC"
		}
		return session.OrderBy(fmt.Sprintf("%s %s", field, direction))
	}
	// 如果排序字段不合法，使用默认排序
	return session.OrderBy("app_id DESC")
}

// QueryApps 查询应用列表
func (am *AppManager) QueryApps(ctx context.Context, params AppQuery) (*AppQueryResult, error) {
	slog.Info("查询应用列表",
		"app_id", params.AppID,
		"app_name", params.AppName,
		"dev_language", params.DevLanguage,
		"owner", params.Owner,
		"owner_cn", params.OwnerCn)

	// 构建查询条件
	query := am.buildAppQuery(ctx, params)

	// 先获取总数
	total, err := query.Count(&entity.Apps{})
	if err != nil {
		return nil, err
	}

	// 规范化分页参数
	pageSize, offset := am.utilManager.NormalizePagination(&params.ParamPage)

	// 计算总页数
	totalPages := am.utilManager.CalculateTotalPages(total, pageSize)

	// 验证页码是否合法
	if totalPages > 0 && params.PageNum > totalPages {
		return nil, fmt.Errorf("请求的页码 %d 超出总页数 %d", params.PageNum, totalPages)
	}

	// 如果有数据，再进行分页查询
	var apps []entity.Apps
	if total > 0 {
		// 重新构建查询，因为Count会消耗query
		dataQuery := am.buildAppQuery(ctx, params)

		// 执行分页查询
		err = dataQuery.Limit(pageSize, offset).Find(&apps)
		if err != nil {
			return nil, err
		}

		// 添加日志
		slog.Info("查询到应用数据",
			"total", total,
			"actual_count", len(apps),
			"page_num", params.PageNum,
			"page_size", pageSize,
			"offset", offset)
	}

	// 构建返回结果
	result := &AppQueryResult{
		Total:      total,
		Apps:       apps,
		PageNum:    params.PageNum,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}

	return result, nil
}

// GetAppByID 根据ID获取单个应用
func (am *AppManager) GetAppByID(ctx context.Context, appID int64) (*entity.Apps, error) {
	if appID <= 0 {
		return nil, NewValidationError("无效的应用ID")
	}

	var app entity.Apps
	exists, err := db.Engine.Context(ctx).
		Where("app_id = ? AND deleted_at IS NULL", appID).
		Get(&app)

	if err != nil {
		return nil, err
	}

	if !exists {
		return nil, NewAppNotFoundError(appID)
	}

	return &app, nil
}

// GetAppByName 根据应用名称获取单个应用
func (am *AppManager) GetAppByName(ctx context.Context, appName string) (*entity.Apps, error) {
	if appName == "" {
		return nil, NewValidationError("应用名称不能为空")
	}

	var app entity.Apps
	exists, err := db.Engine.Context(ctx).
		Where("app_name = ? AND deleted_at IS NULL", appName).
		Get(&app)

	slog.Info("", exists, err)

	if err != nil {
		return nil, err
	}

	if !exists {
		return nil, NewAppNotFoundError(0)
	}

	return &app, nil
}
