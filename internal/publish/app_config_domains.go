package publish

import (
	"ares/internal/db"
	"ares/internal/entity"
	"fmt"
	"strings"
)

// IngressDomain Jenkins 透传的多域名结构
type IngressDomain struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

func fetchAppConfigDomains(configID int) ([]entity.AppConfigDomain, error) {
	if configID <= 0 {
		return nil, fmt.Errorf("无效的 config_id")
	}
	var rows []entity.AppConfigDomain
	if err := db.Engine.Where("config_id = ? AND deleted_at IS NULL", configID).Find(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func normalizeIngressDomains(rows []entity.AppConfigDomain) []IngressDomain {
	out := make([]IngressDomain, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		host := strings.TrimSpace(r.Host)
		path := strings.TrimSpace(r.Path)
		if host == "" {
			continue
		}
		if path == "" {
			path = "/"
		}
		key := host + " " + path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, IngressDomain{Host: host, Path: path})
	}
	return out
}
