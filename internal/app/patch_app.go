package app

import (
	"ares/internal/db"
	"ares/internal/entity"
	"context"
	"fmt"
	"strings"
)

// PatchAppRequest 应用基本信息变更（PATCH：只更新传入字段）
type PatchAppRequest struct {
	// 允许更新的字段（不允许修改 app_name / app_id）
	AppNameCN      *string `json:"app_name_cn"`
	Owner          *string `json:"owner"`
	OwnerCN        *string `json:"owner_cn"`
	DevLanguage    *string `json:"dev_language"`
	DescriptionCN  *string `json:"description_cn"`
	GitUrl         *string `json:"git_url"`
	RundeckAppName *string `json:"rundeck_app_name"`
}

func buildPatchAppMap(req PatchAppRequest) (map[string]any, error) {
	m := make(map[string]any)

	setStrTrim := func(key string, v *string, allowEmpty bool) error {
		if v == nil {
			return nil
		}
		s := strings.TrimSpace(*v)
		if !allowEmpty && s == "" {
			return fmt.Errorf("%s 不能为空", key)
		}
		m[key] = s
		return nil
	}

	// 这里约定：除 description_cn / rundeck_app_name 之外，其它字段不允许置空
	if err := setStrTrim("app_name_cn", req.AppNameCN, false); err != nil {
		return nil, err
	}
	if err := setStrTrim("owner", req.Owner, false); err != nil {
		return nil, err
	}
	if err := setStrTrim("owner_cn", req.OwnerCN, false); err != nil {
		return nil, err
	}
	if err := setStrTrim("dev_language", req.DevLanguage, false); err != nil {
		return nil, err
	}
	if err := setStrTrim("git_url", req.GitUrl, false); err != nil {
		return nil, err
	}
	// 允许置空：用于清理描述/解绑 rundeck 名称（置空会写入空字符串）
	if err := setStrTrim("description_cn", req.DescriptionCN, true); err != nil {
		return nil, err
	}
	if err := setStrTrim("rundeck_app_name", req.RundeckAppName, true); err != nil {
		return nil, err
	}

	if len(m) == 0 {
		return nil, fmt.Errorf("没有需要更新的字段")
	}
	return m, nil
}

// PatchAppByID 更新应用基本信息（只更新传入字段），并返回更新后的记录
func (am *AppManager) PatchAppByID(ctx context.Context, appID int64, req PatchAppRequest) (*entity.Apps, error) {
	validator := NewAppValidator()
	if err := validator.ValidateAppID(appID); err != nil {
		return nil, NewValidationError(err.Error())
	}
	if err := validator.ValidatePatchApp(&req); err != nil {
		return nil, NewValidationError("业务规则校验失败" + err.Error())
	}

	updates, err := buildPatchAppMap(req)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}

	affected, err := db.Engine.Context(ctx).
		Table(entity.TableApps).
		Where("app_id = ? AND deleted_at IS NULL", appID).
		Update(updates)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, NewAppNotFoundError(appID, "")
	}

	// 返回最新数据
	return am.GetAppByID(ctx, appID)
}
