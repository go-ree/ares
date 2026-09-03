package environment

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"regexp"
	"strings"
)

var (
	ErrNotFound = errors.New("环境不存在")
	ErrDisabled = errors.New("环境已停用")

	environmentCodePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,62}$`)
)

// Service 管理环境目录。环境身份只由 Env 表达，Kubernetes、Harbor 等集成配置均为可选能力。
type Service struct{}

type CreateRequest struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Enabled   *bool  `json:"enabled,omitempty"`
	SortOrder int    `json:"sort_order"`
}

type UpdateRequest struct {
	Name      *string `json:"name,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
}

type View struct {
	ID          int    `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	SortOrder   int    `json:"sort_order"`
}

func NewService() *Service { return &Service{} }

// NormalizeCode returns the canonical environment code used by every domain boundary.
func NormalizeCode(value string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(value))
	if !environmentCodePattern.MatchString(code) {
		return "", fmt.Errorf("环境代码必须匹配 %s", environmentCodePattern.String())
	}
	return code, nil
}

func (s *Service) List(ctx context.Context, includeDisabled bool) ([]View, error) {
	query := db.Engine.Context(ctx).Where("deleted_at IS NULL")
	if !includeDisabled {
		query = query.And("enabled = ?", true)
	}
	var rows []entity.EnvConfigs
	if err := query.Asc("sort_order", "env").Find(&rows); err != nil {
		return nil, err
	}
	views := make([]View, 0, len(rows))
	for _, row := range rows {
		views = append(views, toView(row))
	}
	return views, nil
}

func (s *Service) Get(ctx context.Context, code string) (*entity.EnvConfigs, error) {
	normalized, err := NormalizeCode(code)
	if err != nil {
		return nil, err
	}
	var row entity.EnvConfigs
	has, err := db.Engine.Context(ctx).
		Where("env = ? AND deleted_at IS NULL", normalized).
		Get(&row)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("%w：%s", ErrNotFound, normalized)
	}
	return &row, nil
}

func (s *Service) RequireEnabled(ctx context.Context, code string) (*entity.EnvConfigs, error) {
	row, err := s.Get(ctx, code)
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, fmt.Errorf("%w：%s", ErrDisabled, row.Env)
	}
	return row, nil
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (*View, error) {
	code, err := NormalizeCode(req.Code)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("环境名称不能为空")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := entity.EnvConfigs{
		Env:           code,
		DescriptionCN: name,
		Enabled:       enabled,
		SortOrder:     req.SortOrder,
	}
	if _, err := db.Engine.Context(ctx).Nullable(
		"cluster_name", "harbor_url", "harbor_project_name", "node_version", "maven_version",
	).Insert(&row); err != nil {
		return nil, fmt.Errorf("创建环境 %s 失败: %w", code, err)
	}
	view := toView(row)
	return &view, nil
}

func (s *Service) Update(ctx context.Context, code string, req UpdateRequest) (*View, error) {
	row, err := s.Get(ctx, code)
	if err != nil {
		return nil, err
	}
	updates := make(map[string]any)
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("环境名称不能为空")
		}
		updates["description_cn"] = name
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if len(updates) > 0 {
		if _, err := db.Engine.Context(ctx).Table(new(entity.EnvConfigs)).
			Where("id = ? AND deleted_at IS NULL", row.ID).
			Update(updates); err != nil {
			return nil, fmt.Errorf("更新环境 %s 失败: %w", row.Env, err)
		}
	}
	updated, err := s.Get(ctx, row.Env)
	if err != nil {
		return nil, err
	}
	view := toView(*updated)
	return &view, nil
}

func toView(row entity.EnvConfigs) View {
	return View{
		ID:        row.ID,
		Code:      row.Env,
		Name:      row.DescriptionCN,
		Enabled:   row.Enabled,
		SortOrder: row.SortOrder,
	}
}
