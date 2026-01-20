package publish

import "ares/internal/entity"

// groupDomainsListFromRows 将 app_config_domains(host/path) 聚合为 [{host,paths[]},...]（同 host 合并 paths，去重且保序）。
func groupDomainsListFromRows(rows []entity.AppConfigDomain) []DomainsListItem {
	hostOrder := make([]string, 0, len(rows))
	hostSeen := make(map[string]struct{}, len(rows))
	pathsOrder := make(map[string][]string, len(rows))
	pathsSeen := make(map[string]map[string]struct{}, len(rows))

	for _, r := range rows {
		host := normalizeDomainHost(r.Host)
		if host == "" {
			continue
		}
		path := normalizeDomainPath(r.Path)

		if _, ok := hostSeen[host]; !ok {
			hostSeen[host] = struct{}{}
			hostOrder = append(hostOrder, host)
			pathsOrder[host] = make([]string, 0, 4)
			pathsSeen[host] = make(map[string]struct{}, 8)
		}
		if _, ok := pathsSeen[host][path]; ok {
			continue
		}
		pathsSeen[host][path] = struct{}{}
		pathsOrder[host] = append(pathsOrder[host], path)
	}

	out := make([]DomainsListItem, 0, len(hostOrder))
	for _, h := range hostOrder {
		out = append(out, DomainsListItem{Host: h, Paths: pathsOrder[h]})
	}
	return out
}
