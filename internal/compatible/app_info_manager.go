package compatible

import (
	"context"
	"fmt"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/environment"
	"strings"
)

// AppInfoManager
type AppInfoManager struct{}

func NewAppInfoManager() *AppInfoManager { return &AppInfoManager{} }

type serviceProjectMapJoinRow struct {
	GitUrl          string `xorm:"'git_url'"`
	AppName         string `xorm:"'app_name'"`
	CodePackageType string `xorm:"'code_package_type'"`
}

// ServiceProjectMapItem 兼容老接口 getServiceProjectMap 的单条结果
type ServiceProjectMapItem struct {
	GitAddress         string `json:"gitAddress"`
	ProjectName        string `json:"projectName"`
	ProjectServiceName string `json:"projectServiceName"`
	ProjectType        string `json:"projectType"`
}

// LegacyServiceProjectMapResponse 兼容老接口响应结构（code 为字符串）
// 形如：
// {"code":"200","message":"","result":[...]}
type LegacyServiceProjectMapResponse struct {
	Code    string                  `json:"code"`
	Message string                  `json:"message"`
	Result  []ServiceProjectMapItem `json:"result"`
}

func (m *AppInfoManager) QueryServiceProjectMap(ctx context.Context, env string) ([]ServiceProjectMapItem, error) {
	catalog, err := environment.NewService().List(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("查询环境目录失败：%w", err)
	}
	e, err := resolveCompatibleEnvironment(env, catalog)
	if err != nil {
		return nil, err
	}

	var rows []serviceProjectMapJoinRow
	err = db.Engine.Context(ctx).
		Table(entity.TableApps).
		Join("INNER", entity.TableAppConfigs, "apps.app_id = app_configs.app_id").
		Where("apps.deleted_at IS NULL").
		And("app_configs.deleted_at IS NULL").
		And("app_configs.env = ?", e).
		OrderBy("apps.app_id ASC").
		Find(&rows)
	if err != nil {
		return nil, err
	}

	out := make([]ServiceProjectMapItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, ServiceProjectMapItem{
			GitAddress:         r.GitUrl,
			ProjectName:        parseProjectNameFromGitURL(r.GitUrl),
			ProjectServiceName: r.AppName,
			ProjectType:        r.CodePackageType,
		})
	}
	return out, nil
}

// resolveCompatibleEnvironment keeps the historical endpoint convenient
// without baking a particular environment code into the compatibility layer.
// An omitted value selects the first enabled catalog entry; callers may also
// use either the stable code or the display name.
func resolveCompatibleEnvironment(value string, catalog []environment.View) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if len(catalog) == 0 {
			return "", fmt.Errorf("没有已启用的环境")
		}
		return catalog[0].Code, nil
	}
	for _, item := range catalog {
		if raw == item.Name || strings.EqualFold(raw, item.Code) {
			return item.Code, nil
		}
	}
	code, err := environment.NormalizeCode(raw)
	if err != nil {
		return "", fmt.Errorf("环境 %q 不存在或已停用", raw)
	}
	return "", fmt.Errorf("环境 %q 不存在或已停用", code)
}

func parseProjectNameFromGitURL(gitURL string) string {
	s := strings.TrimSpace(gitURL)
	if s == "" {
		return ""
	}

	// 去掉末尾的 "/" 与 ".git"
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")

	// 兼容 git@host:group/repo / https://host/group/repo 等形式
	lastSlash := strings.LastIndex(s, "/")
	lastColon := strings.LastIndex(s, ":")
	cut := lastSlash
	if lastColon > cut {
		cut = lastColon
	}
	if cut >= 0 && cut+1 < len(s) {
		return s[cut+1:]
	}
	return s
}
