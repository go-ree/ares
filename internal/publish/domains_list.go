package publish

import (
	"strings"
)

// DomainsListItem Jenkins 透传的发布时域名结构：[{host:"",paths:["/a","/b"]},...]
type DomainsListItem struct {
	Host  string   `json:"host"`
	Paths []string `json:"paths"`
}

func normalizeDomainHost(hostRaw string) string {
	h := strings.ToLower(strings.TrimSpace(hostRaw))
	if h == "" || h == "null" {
		return ""
	}
	return h
}

func normalizeDomainPath(pathRaw string) string {
	p := strings.TrimSpace(pathRaw)
	if p == "" || strings.EqualFold(p, "null") {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
