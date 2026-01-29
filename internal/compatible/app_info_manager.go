package compatible

import (
	"ares/internal/db"
	"ares/internal/entity"
	"context"
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

func (m *AppInfoManager) QueryServiceProjectMap(ctx context.Context, env string) ([]ServiceProjectMapItem, error) {
	e := strings.ToLower(strings.TrimSpace(env))
	if e == "" {
		e = "dev"
	}

	var rows []serviceProjectMapJoinRow
	err := db.Engine.Context(ctx).
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
