package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/environment"
	"github.com/go-ree/ares/internal/tool"

	"github.com/go-sql-driver/mysql"
)

type devLanguageRulesJSON struct {
	Allowed []string `json:"allowed"`
	Default string   `json:"default"`
}

func loadDevLanguageRules(ctx context.Context, devLanguage string) (*devLanguageRulesJSON, error) {
	lang := strings.ToLower(strings.TrimSpace(devLanguage))
	if lang == "" {
		return nil, fmt.Errorf("dev_language 不能为空")
	}
	var row entity.DevLanguageRule
	has, err := db.Engine.Context(ctx).
		Where("dev_language = ? AND deleted_at IS NULL", lang).
		Get(&row)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("未配置 dev_language 规则：%s", lang)
	}
	var rules devLanguageRulesJSON
	if err := json.Unmarshal(row.Rules, &rules); err != nil {
		return nil, fmt.Errorf("dev_language 规则解析失败：%s", err)
	}
	if len(rules.Allowed) == 0 || tool.IsEmptyLikeText(rules.Default) {
		return nil, fmt.Errorf("dev_language 规则不完整：%s", lang)
	}
	// normalize allowed/default
	seen := make(map[string]struct{}, len(rules.Allowed))
	allowed := make([]string, 0, len(rules.Allowed))
	for _, a := range rules.Allowed {
		v := tool.NormalizeNullableText(a)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		allowed = append(allowed, v)
	}
	rules.Allowed = allowed
	rules.Default = tool.NormalizeNullableText(rules.Default)
	if _, ok := seen[rules.Default]; !ok {
		return nil, fmt.Errorf("dev_language 规则 default 不在 allowed 中：%s default=%s", lang, rules.Default)
	}
	return &rules, nil
}

func validateCodePackageTypeForApp(ctx context.Context, appID int, codePackageType string) error {
	var appRow entity.Apps
	has, err := db.Engine.Context(ctx).
		Where("app_id = ? AND deleted_at IS NULL", appID).
		Get(&appRow)
	if err != nil {
		return err
	}
	if !has {
		return NewAppNotFoundError(int64(appID), "")
	}
	rules, err := loadDevLanguageRules(ctx, appRow.DevLanguage)
	if err != nil {
		return err
	}
	cpt := strings.TrimSpace(codePackageType)
	if cpt == "" || strings.EqualFold(cpt, "NULL") {
		return NewValidationError("code_package_type 不能为空")
	}
	for _, a := range rules.Allowed {
		if a == cpt {
			return nil
		}
	}
	return NewValidationError(fmt.Sprintf("code_package_type=%s 不允许用于 dev_language=%s（允许：%s）", cpt, strings.ToLower(appRow.DevLanguage), strings.Join(rules.Allowed, ",")))
}

// CreateAppConfigRequest 创建应用环境配置（app_id + env）
// 说明：不包含 domain/domain_path（已废弃），多域名请用 app_config_domains。
type CreateAppConfigRequest struct {
	Env string `json:"env"`

	CodePackageType *string `json:"code_package_type"`
	CodePackagePath *string `json:"code_package_path"`
	CodePackageName *string `json:"code_package_name"`
	BaseImage       *string `json:"base_image"`

	PodCount               *int    `json:"pod_count"`
	LimitsMemory           *int    `json:"limits_memory"`
	GpuCount               *int    `json:"gpu_count"`
	ProbeType              *string `json:"probe_type"`
	ProbeCheckPath         *string `json:"probe_check_path"`
	ProbeCheckTcpPort      *int    `json:"probe_check_tcp_port"`
	ProbeCheckHttpPort     *int    `json:"probe_check_http_port"`
	ProbeStopCheckHttpPort *int    `json:"probe_stop_check_http_port"`
	ContainerPort          *int    `json:"container_port"`
	PreStopType            *string `json:"pre_stop_type"`
	PreStopCheckPath       *string `json:"pre_stop_check_path"`
	PreStopCommand         *string `json:"pre_stop_command"`
}

// UpdateAppConfigRequest 允许更新的应用环境配置字段（指针用于 PATCH 语义）
type UpdateAppConfigRequest struct {
	CodePackageType *string `json:"code_package_type"`
	CodePackagePath *string `json:"code_package_path"`
	CodePackageName *string `json:"code_package_name"`
	BaseImage       *string `json:"base_image"`

	PodCount               *int    `json:"pod_count"`
	LimitsMemory           *int    `json:"limits_memory"`
	GpuCount               *int    `json:"gpu_count"`
	ProbeType              *string `json:"probe_type"`
	ProbeCheckPath         *string `json:"probe_check_path"`
	ProbeCheckTcpPort      *int    `json:"probe_check_tcp_port"`
	ProbeCheckHttpPort     *int    `json:"probe_check_http_port"`
	ProbeStopCheckHttpPort *int    `json:"probe_stop_check_http_port"`
	ContainerPort          *int    `json:"container_port"`
	PreStopType            *string `json:"pre_stop_type"`
	PreStopCheckPath       *string `json:"pre_stop_check_path"`
	PreStopCommand         *string `json:"pre_stop_command"`
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

func (cm *ConfigManager) CreateAppConfigByAppEnv(ctx context.Context, appID int, req CreateAppConfigRequest) (*entity.AppConfigs, error) {
	if appID <= 0 {
		return nil, NewValidationError("无效的 app_id")
	}
	normalizedEnv, err := environment.NormalizeCode(req.Env)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	envRow, err := environment.NewService().RequireEnabled(ctx, normalizedEnv)
	if err != nil {
		return nil, err
	}
	env := envRow.Env

	// 校验 app 是否存在
	var appRow entity.Apps
	has, err := db.Engine.Context(ctx).
		Where("app_id = ? AND deleted_at IS NULL", appID).
		Get(&appRow)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, NewAppNotFoundError(int64(appID), "")
	}

	// 读取语言规则（用于 default 与 code_package_type 校验）
	rules, err := loadDevLanguageRules(ctx, appRow.DevLanguage)
	if err != nil {
		return nil, err
	}

	// 判重：同 app_id + env 只能一条
	cnt, err := db.Engine.Context(ctx).
		Where("app_id = ? AND env = ? AND deleted_at IS NULL", appID, env).
		Count(&entity.AppConfigs{})
	if err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, NewDuplicateAppConfigError(appID, env)
	}

	// 默认值：新配置显式创建，使用应用语言规则提供的包类型默认值。
	row := &entity.AppConfigs{
		AppID:                  appID,
		Env:                    env,
		CodePackageType:        rules.Default,
		CodePackageName:        "",
		CodePackagePath:        "",
		BaseImage:              "",
		PodCount:               1,
		LimitsMemory:           2,
		GpuCount:               0,
		ProbeType:              "HTTP",
		ProbeCheckPath:         "/inside/checkup",
		ProbeCheckTcpPort:      8080,
		ProbeCheckHttpPort:     8080,
		ProbeStopCheckHttpPort: 8080,
		ContainerPort:          8080,
		PreStopType:            "HTTP",
		PreStopCheckPath:       "/inside/prestop",
		PreStopCommand:         "",
	}

	// 覆盖写入（如有传值）
	if req.CodePackageType != nil {
		cpt := strings.TrimSpace(*req.CodePackageType)
		ok := false
		for _, a := range rules.Allowed {
			if a == cpt {
				ok = true
				break
			}
		}
		if !ok {
			return nil, NewValidationError(fmt.Sprintf("code_package_type=%s 不允许用于 dev_language=%s（允许：%s）", cpt, strings.ToLower(appRow.DevLanguage), strings.Join(rules.Allowed, ",")))
		}
		row.CodePackageType = cpt
	}
	if req.CodePackagePath != nil {
		row.CodePackagePath = tool.NormalizeNullableText(*req.CodePackagePath)
	}
	if req.CodePackageName != nil {
		row.CodePackageName = tool.NormalizeNullableText(*req.CodePackageName)
	}
	if req.BaseImage != nil {
		row.BaseImage = tool.NormalizeNullableText(*req.BaseImage)
	}
	if req.PodCount != nil {
		row.PodCount = *req.PodCount
	}
	if req.LimitsMemory != nil {
		row.LimitsMemory = *req.LimitsMemory
	}
	if req.GpuCount != nil {
		row.GpuCount = *req.GpuCount
	}
	if req.ProbeType != nil {
		row.ProbeType = strings.TrimSpace(*req.ProbeType)
	}
	if req.ProbeCheckPath != nil {
		p := strings.TrimSpace(*req.ProbeCheckPath)
		if p != "" && !strings.HasPrefix(p, "/") {
			return nil, NewValidationError("probe_check_path 必须以 / 开头")
		}
		if p != "" {
			row.ProbeCheckPath = p
		}
	}
	if req.ProbeCheckTcpPort != nil {
		port := *req.ProbeCheckTcpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_check_tcp_port 必须在 1-65535 之间")
		}
		row.ProbeCheckTcpPort = port
	}
	if req.ProbeCheckHttpPort != nil {
		port := *req.ProbeCheckHttpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_check_http_port 必须在 1-65535 之间")
		}
		row.ProbeCheckHttpPort = port
	}
	if req.ProbeStopCheckHttpPort != nil {
		port := *req.ProbeStopCheckHttpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_stop_check_http_port 必须在 1-65535 之间")
		}
		row.ProbeStopCheckHttpPort = port
	}
	if req.ContainerPort != nil {
		port := *req.ContainerPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("container_port 必须在 1-65535 之间")
		}
		row.ContainerPort = port
	}
	if req.PreStopType != nil {
		row.PreStopType = strings.TrimSpace(*req.PreStopType)
	}
	if req.PreStopCheckPath != nil {
		p := strings.TrimSpace(*req.PreStopCheckPath)
		if p != "" && !strings.HasPrefix(p, "/") {
			return nil, NewValidationError("pre_stop_check_path 必须以 / 开头")
		}
		if p != "" {
			row.PreStopCheckPath = p
		}
	}
	if req.PreStopCommand != nil {
		row.PreStopCommand = tool.NormalizeNullableText(*req.PreStopCommand)
	}

	if _, err := db.Engine.Context(ctx).Nullable(
		"code_package_path",
		"code_package_name",
		"base_image",
		"pre_stop_command",
	).Insert(row); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return nil, NewDuplicateAppConfigError(appID, env)
		}
		return nil, err
	}
	return row, nil
}

func (cm *ConfigManager) GetAppConfigByAppEnv(ctx context.Context, appID int, env string) (*entity.AppConfigs, error) {
	normalizedEnv, err := environment.NormalizeCode(env)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	var row entity.AppConfigs
	has, err := db.Engine.Context(ctx).
		Where("app_id = ? AND env = ? AND deleted_at IS NULL", appID, normalizedEnv).
		Get(&row)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, NewAppConfigNotFoundErrorByAppEnv(appID, normalizedEnv)
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
		return nil, NewAppConfigNotFoundErrorByID(configID)
	}
	return &row, nil
}

func buildUpdateMap(req UpdateAppConfigRequest) (map[string]any, error) {
	m := make(map[string]any)

	setStr := func(key string, v *string) {
		if v != nil {
			m[key] = strings.TrimSpace(*v)
		}
	}
	setNullableText := func(key string, v *string) {
		if v == nil {
			return
		}
		normalized := tool.NormalizeNullableText(*v)
		if normalized == "" {
			m[key] = nil
		} else {
			m[key] = normalized
		}
	}
	setInt := func(key string, v *int) {
		if v != nil {
			m[key] = *v
		}
	}

	if req.CodePackageType != nil {
		codePackageType := tool.NormalizeNullableText(*req.CodePackageType)
		if codePackageType == "" {
			return nil, NewValidationError("code_package_type 不能为空")
		}
		m["code_package_type"] = codePackageType
	}
	setNullableText("code_package_path", req.CodePackagePath)
	setNullableText("code_package_name", req.CodePackageName)
	setNullableText("base_image", req.BaseImage)

	setInt("pod_count", req.PodCount)
	setInt("limits_memory", req.LimitsMemory)
	setInt("gpu_count", req.GpuCount)
	setStr("probe_type", req.ProbeType)
	setStr("probe_check_path", req.ProbeCheckPath)
	if req.ProbeCheckTcpPort != nil {
		port := *req.ProbeCheckTcpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_check_tcp_port 必须在 1-65535 之间")
		}
		setInt("probe_check_tcp_port", req.ProbeCheckTcpPort)
	}
	if req.ProbeCheckHttpPort != nil {
		port := *req.ProbeCheckHttpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_check_http_port 必须在 1-65535 之间")
		}
		setInt("probe_check_http_port", req.ProbeCheckHttpPort)
	}
	if req.ProbeStopCheckHttpPort != nil {
		port := *req.ProbeStopCheckHttpPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("probe_stop_check_http_port 必须在 1-65535 之间")
		}
		setInt("probe_stop_check_http_port", req.ProbeStopCheckHttpPort)
	}
	if req.ContainerPort != nil {
		port := *req.ContainerPort
		if port <= 0 || port > 65535 {
			return nil, NewValidationError("container_port 必须在 1-65535 之间")
		}
		setInt("container_port", req.ContainerPort)
	}
	setStr("pre_stop_type", req.PreStopType)
	setStr("pre_stop_check_path", req.PreStopCheckPath)
	setNullableText("pre_stop_command", req.PreStopCommand)

	if len(m) == 0 {
		return nil, NewValidationError("没有需要更新的字段")
	}
	return m, nil
}

func (cm *ConfigManager) PatchAppConfigByID(ctx context.Context, configID int, req UpdateAppConfigRequest) error {
	cur, err := cm.GetAppConfigByID(ctx, configID)
	if err != nil {
		return err
	}
	if _, err := environment.NewService().RequireEnabled(ctx, cur.Env); err != nil {
		return err
	}

	// 若更新 code_package_type，则需要按 app.dev_language 规则校验
	if req.CodePackageType != nil {
		if err := validateCodePackageTypeForApp(ctx, cur.AppID, strings.TrimSpace(*req.CodePackageType)); err != nil {
			return err
		}
	}
	updates, err := buildUpdateMap(req)
	if err != nil {
		return err
	}
	affected, err := db.Engine.Context(ctx).Table("app_configs").Where("config_id = ? AND deleted_at IS NULL", configID).Update(updates)
	if err != nil {
		return err
	}
	if affected == 0 {
		// MySQL 默认只统计实际发生变化的行；相同值 PATCH 也应保持幂等。
		_, err := cm.GetAppConfigByID(ctx, configID)
		return err
	}
	return nil
}

func (cm *ConfigManager) PatchAppConfigByAppEnv(ctx context.Context, appID int, env string, req UpdateAppConfigRequest) error {
	normalizedEnv, err := environment.NormalizeCode(env)
	if err != nil {
		return NewValidationError(err.Error())
	}
	if _, err := environment.NewService().RequireEnabled(ctx, normalizedEnv); err != nil {
		return err
	}
	cur, err := cm.GetAppConfigByAppEnv(ctx, appID, normalizedEnv)
	if err != nil {
		return err
	}
	// 若更新 code_package_type，则需要按 app.dev_language 规则校验
	if req.CodePackageType != nil {
		if err := validateCodePackageTypeForApp(ctx, cur.AppID, strings.TrimSpace(*req.CodePackageType)); err != nil {
			return err
		}
	}
	updates, err := buildUpdateMap(req)
	if err != nil {
		return err
	}
	affected, err := db.Engine.Context(ctx).Table("app_configs").Where("config_id = ? AND deleted_at IS NULL", cur.ConfigID).Update(updates)
	if err != nil {
		return err
	}
	if affected == 0 {
		_, err := cm.GetAppConfigByAppEnv(ctx, appID, normalizedEnv)
		return err
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

	// 严格 host 校验：
	// - 允许：普通域名（a.example.com）
	// - 禁止：通配符（*.example.com）、IPv4/IPv6、带协议/端口/路径/空白/非法字符
	// - 禁止：带协议/端口/路径/空白/非法字符
	if err := validateIngressHost(host); err != nil {
		return "", "", err
	}

	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("path 必须以 / 开头：%s", path)
	}
	// 严格 path 校验：禁止空白、?、#；禁止 /./ 与 /../ 段
	if strings.ContainsAny(path, " \t\r\n?#") {
		return "", "", fmt.Errorf("path 不能包含空白或 ? #：%s", path)
	}
	// 禁止 "." 与 ".." 段（避免异常/歧义）
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return "", "", fmt.Errorf("path 不能包含 . 或 .. 段：%s", path)
		}
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

func validateIngressHost(host string) error {
	// quick reject
	if strings.ContainsAny(host, " \t\r\n/") {
		return fmt.Errorf("host 不能包含空白或 /：%s", host)
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("host 不能包含协议：%s", host)
	}
	// 禁止端口（也自然禁止 IPv6 形式）
	if strings.Contains(host, ":") {
		return fmt.Errorf("host 不能包含端口或 IPv6：%s", host)
	}
	// 禁止通配符
	if strings.Contains(host, "*") {
		return fmt.Errorf("host 不允许通配符：%s", host)
	}
	// 禁止 IPv4
	isIPv4 := true
	for _, ch := range host {
		if (ch >= '0' && ch <= '9') || ch == '.' {
			continue
		}
		isIPv4 = false
		break
	}
	if isIPv4 {
		parts := strings.Split(host, ".")
		if len(parts) == 4 {
			allNum := true
			for _, p := range parts {
				if p == "" {
					allNum = false
					break
				}
				for _, c := range p {
					if c < '0' || c > '9' {
						allNum = false
						break
					}
				}
				if !allNum {
					break
				}
			}
			if allNum {
				return fmt.Errorf("host 不允许使用 IP：%s", host)
			}
		}
	}

	return validateDNSName(host, true)
}

func validateDNSName(name string, noSingleLabel bool) error {
	if name == "" {
		return fmt.Errorf("host 不能为空")
	}
	if len(name) > 253 {
		return fmt.Errorf("host 长度不能超过 253：%s", name)
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return fmt.Errorf("host 格式错误：%s", name)
	}
	labels := strings.Split(name, ".")
	if noSingleLabel && len(labels) < 2 {
		return fmt.Errorf("host 必须是完整域名（至少包含一个点）：%s", name)
	}
	for _, lab := range labels {
		if lab == "" {
			return fmt.Errorf("host 格式错误：%s", name)
		}
		if len(lab) > 63 {
			return fmt.Errorf("host 标签长度不能超过 63：%s", name)
		}
		if strings.HasPrefix(lab, "-") || strings.HasSuffix(lab, "-") {
			return fmt.Errorf("host 标签不能以 - 开头或结尾：%s", name)
		}
		for _, ch := range lab {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return fmt.Errorf("host 包含非法字符：%s", name)
		}
	}
	return nil
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
