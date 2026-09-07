package app

import (
	"context"
	"fmt"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/tool"
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
		s := tool.NormalizeNullableText(*v)
		if !allowEmpty && s == "" {
			return fmt.Errorf("%s 不能为空", key)
		}
		if allowEmpty && s == "" {
			m[key] = nil
		} else {
			m[key] = s
		}
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
	// 允许置空：用于清理描述/解绑 rundeck 名称（置空会写入 SQL NULL）
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

	// 若更新 dev_language，需要保证现存各环境配置的 code_package_type 仍匹配新规则
	if req.DevLanguage != nil {
		newLang := strings.ToLower(strings.TrimSpace(*req.DevLanguage))
		rules, err := loadDevLanguageRules(ctx, newLang)
		if err != nil {
			return nil, err
		}
		var cfgs []entity.AppConfigs
		if err := db.Engine.Context(ctx).
			Where("app_id = ? AND deleted_at IS NULL", appID).
			Find(&cfgs); err != nil {
			return nil, err
		}
		allowed := make(map[string]struct{}, len(rules.Allowed))
		for _, a := range rules.Allowed {
			allowed[a] = struct{}{}
		}
		conflicts := make([]string, 0, len(cfgs))
		for _, c := range cfgs {
			cpt := strings.TrimSpace(c.CodePackageType)
			if cpt == "" || strings.EqualFold(cpt, "NULL") {
				conflicts = append(conflicts, fmt.Sprintf("env=%s code_package_type=%s", c.Env, c.CodePackageType))
				continue
			}
			if _, ok := allowed[cpt]; !ok {
				conflicts = append(conflicts, fmt.Sprintf("env=%s code_package_type=%s", c.Env, c.CodePackageType))
			}
		}
		if len(conflicts) > 0 {
			return nil, NewValidationError(fmt.Sprintf("dev_language=%s 与现有环境配置不匹配，请先调整配置：%s", newLang, strings.Join(conflicts, "; ")))
		}
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
