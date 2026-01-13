package app

import (
	"ares/internal/db"
	"ares/internal/entity"
	"context"
	"fmt"
	"strings"
)

// UpdateAppConfigRequest 允许更新的应用环境配置字段（指针用于 PATCH 语义）
type UpdateAppConfigRequest struct {
	CodePackageType *string `json:"code_package_type"`
	CodePackagePath *string `json:"code_package_path"`
	CodePackageName *string `json:"code_package_name"`
	BaseImage       *string `json:"base_image"`

	PodCount         *int    `json:"pod_count"`
	LimitsMemory     *int    `json:"limits_memory"`
	GpuCount         *int    `json:"gpu_count"`
	ProbeType        *string `json:"probe_type"`
	ProbeCheckPath   *string `json:"probe_check_path"`
	PreStopType      *string `json:"pre_stop_type"`
	PreStopCheckPath *string `json:"pre_stop_check_path"`
	PreStopCommand   *string `json:"pre_stop_command"`
	Domain           *string `json:"domain"`
	DomainPath       *string `json:"domain_path"`
}

// DomainItem 多域名配置项
type DomainItem struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

type UpsertDomainsRequest struct {
	Domains []DomainItem `json:"domains"`
}

// PatchDomainRequest 单条域名 patch（指针语义）
type PatchDomainRequest struct {
	Host *string `json:"host"`
	Path *string `json:"path"`
}

// ConfigManager 管理 app_configs / app_config_domains
type ConfigManager struct{}

func NewConfigManager() *ConfigManager { return &ConfigManager{} }

func (cm *ConfigManager) ListAppConfigs(ctx context.Context, appID int) ([]entity.AppConfigs, error) {
	var rows []entity.AppConfigs
	if err := db.Engine.Context(ctx).Where("app_id = ? AND deleted_at IS NULL", appID).Find(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (cm *ConfigManager) GetAppConfigByAppEnv(ctx context.Context, appID int, env string) (*entity.AppConfigs, error) {
	var row entity.AppConfigs
	has, err := db.Engine.Context(ctx).
		Where("app_id = ? AND env = ? AND deleted_at IS NULL", appID, env).
		Get(&row)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("未找到配置，app_id=%d env=%s", appID, env)
	}
	return &row, nil
}

func (cm *ConfigManager) GetAppConfigByID(ctx context.Context, configID int) (*entity.AppConfigs, error) {
	var row entity.AppConfigs
	has, err := db.Engine.Context(ctx).Where("config_id = ? AND deleted_at IS NULL", configID).Get(&row)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("未找到配置，config_id=%d", configID)
	}
	return &row, nil
}

func buildUpdateMap(req UpdateAppConfigRequest) (map[string]any, error) {
	m := make(map[string]any)

	setStr := func(key string, v *string) {
		if v != nil {
			m[key] = *v
		}
	}
	setInt := func(key string, v *int) {
		if v != nil {
			m[key] = *v
		}
	}

	setStr("code_package_type", req.CodePackageType)
	setStr("code_package_path", req.CodePackagePath)
	setStr("code_package_name", req.CodePackageName)
	setStr("base_image", req.BaseImage)

	setInt("pod_count", req.PodCount)
	setInt("limits_memory", req.LimitsMemory)
	setInt("gpu_count", req.GpuCount)
	setStr("probe_type", req.ProbeType)
	setStr("probe_check_path", req.ProbeCheckPath)
	setStr("pre_stop_type", req.PreStopType)
	setStr("pre_stop_check_path", req.PreStopCheckPath)
	setStr("pre_stop_command", req.PreStopCommand)
	setStr("domain", req.Domain)
	setStr("domain_path", req.DomainPath)

	// 轻量校验：domain_path 规范化
	if req.DomainPath != nil {
		p := strings.TrimSpace(*req.DomainPath)
		if p == "" {
			p = "/"
		}
		if !strings.HasPrefix(p, "/") {
			return nil, fmt.Errorf("domain_path 必须以 / 开头")
		}
		m["domain_path"] = p
	}

	if len(m) == 0 {
		return nil, fmt.Errorf("没有需要更新的字段")
	}
	return m, nil
}

func (cm *ConfigManager) PatchAppConfigByID(ctx context.Context, configID int, req UpdateAppConfigRequest) error {
	updates, err := buildUpdateMap(req)
	if err != nil {
		return err
	}
	affected, err := db.Engine.Context(ctx).Table("app_configs").Where("config_id = ? AND deleted_at IS NULL", configID).Update(updates)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("未更新任何记录，config_id=%d", configID)
	}
	return nil
}

func (cm *ConfigManager) PatchAppConfigByAppEnv(ctx context.Context, appID int, env string, req UpdateAppConfigRequest) error {
	updates, err := buildUpdateMap(req)
	if err != nil {
		return err
	}
	affected, err := db.Engine.Context(ctx).Table("app_configs").Where("app_id = ? AND env = ? AND deleted_at IS NULL", appID, env).Update(updates)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("未更新任何记录，app_id=%d env=%s", appID, env)
	}
	return nil
}

func (cm *ConfigManager) ListDomainsByConfigID(ctx context.Context, configID int) ([]entity.AppConfigDomain, error) {
	var rows []entity.AppConfigDomain
	if err := db.Engine.Context(ctx).Where("config_id = ? AND deleted_at IS NULL", configID).Find(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func normalizeDomainHostPath(hostRaw, pathRaw string) (host string, path string, err error) {
	host = strings.ToLower(strings.TrimSpace(hostRaw))
	path = strings.TrimSpace(pathRaw)

	if host == "" {
		return "", "", fmt.Errorf("host 不能为空")
	}

	// 兼容历史默认值
	if host == "null" || host == "NULL" {
		return "", "", fmt.Errorf("host 不能为 NULL")
	}

	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("path 必须以 / 开头：%s", path)
	}
	// 压缩重复斜杠：/a//b -> /a/b
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	// 非根路径去掉末尾斜杠：/foo/ -> /foo
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}
	return host, path, nil
}

// OverwriteDomainsByConfigID 覆盖写入 domains（硬删除旧数据，避免 unique(config_id,host,path) 与软删除冲突）
func (cm *ConfigManager) OverwriteDomainsByConfigID(ctx context.Context, configID int, domains []DomainItem) error {
	if configID <= 0 {
		return fmt.Errorf("无效的 config_id")
	}

	// 规范化 + 判重（同 host + 同 path 直接报错，避免 ingress 同域名同 location 冲突）
	seen := make(map[string]struct{}, len(domains))
	rows := make([]entity.AppConfigDomain, 0, len(domains))
	for _, d := range domains {
		host, path, err := normalizeDomainHostPath(d.Host, d.Path)
		if err != nil {
			return err
		}
		key := host + " " + path
		if _, ok := seen[key]; ok {
			return fmt.Errorf("多域名配置重复：host=%s path=%s", host, path)
		}
		seen[key] = struct{}{}
		rows = append(rows, entity.AppConfigDomain{
			ConfigID: configID,
			Host:     host,
			Path:     path,
		})
	}

	s := db.Engine.NewSession()
	defer s.Close()
	s = s.Context(ctx)
	if err := s.Begin(); err != nil {
		return err
	}

	// 硬删除：避免软删除残留导致唯一键冲突
	if _, err := s.Exec("DELETE FROM app_config_domains WHERE config_id = ?", configID); err != nil {
		_ = s.Rollback()
		return err
	}

	if len(rows) == 0 {
		return s.Commit()
	}
	if _, err := s.Insert(&rows); err != nil {
		_ = s.Rollback()
		return err
	}
	return s.Commit()
}

func (cm *ConfigManager) CreateDomain(ctx context.Context, configID int, d DomainItem) (*entity.AppConfigDomain, error) {
	host, path, err := normalizeDomainHostPath(d.Host, d.Path)
	if err != nil {
		return nil, err
	}

	// 先查冲突，返回更友好的错误
	cnt, err := db.Engine.Context(ctx).
		Where("config_id = ? AND host = ? AND path = ?", configID, host, path).
		Count(&entity.AppConfigDomain{})
	if err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, fmt.Errorf("多域名配置已存在：host=%s path=%s", host, path)
	}

	row := &entity.AppConfigDomain{
		ConfigID: configID,
		Host:     host,
		Path:     path,
	}
	if _, err := db.Engine.Context(ctx).Insert(row); err != nil {
		return nil, err
	}
	return row, nil
}

// DeleteDomainByID 硬删除单条域名（避免软删除导致唯一键冲突）
func (cm *ConfigManager) DeleteDomainByID(ctx context.Context, configID int, domainID int64) error {
	if configID <= 0 || domainID <= 0 {
		return fmt.Errorf("无效的参数")
	}
	_, err := db.Engine.Context(ctx).Exec("DELETE FROM app_config_domains WHERE config_id = ? AND id = ?", configID, domainID)
	if err != nil {
		return err
	}
	return nil
}

func (cm *ConfigManager) PatchDomainByID(ctx context.Context, configID int, domainID int64, req PatchDomainRequest) (*entity.AppConfigDomain, error) {
	if configID <= 0 || domainID <= 0 {
		return nil, fmt.Errorf("无效的参数")
	}

	// 取当前值
	var cur entity.AppConfigDomain
	has, err := db.Engine.Context(ctx).Where("config_id = ? AND id = ?", configID, domainID).Get(&cur)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("未找到域名记录：config_id=%d domain_id=%d", configID, domainID)
	}

	newHost := cur.Host
	newPath := cur.Path
	if req.Host != nil {
		newHost = *req.Host
	}
	if req.Path != nil {
		newPath = *req.Path
	}

	host, path, err := normalizeDomainHostPath(newHost, newPath)
	if err != nil {
		return nil, err
	}

	// 冲突检查：排除自身
	cnt, err := db.Engine.Context(ctx).
		Where("config_id = ? AND host = ? AND path = ? AND id <> ?", configID, host, path, domainID).
		Count(&entity.AppConfigDomain{})
	if err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, fmt.Errorf("多域名配置冲突：host=%s path=%s", host, path)
	}

	// 更新（硬更新，不触发软删除机制）
	if _, err := db.Engine.Context(ctx).Exec("UPDATE app_config_domains SET host = ?, path = ? WHERE config_id = ? AND id = ?", host, path, configID, domainID); err != nil {
		return nil, err
	}

	cur.Host = host
	cur.Path = path
	return &cur, nil
}
