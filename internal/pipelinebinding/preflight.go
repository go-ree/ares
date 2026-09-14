package pipelinebinding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/pipelinetemplate"
)

var (
	ErrTarget   = errors.New("binding target or version not found")
	ErrDisabled = errors.New("binding resource disabled")
	ErrMismatch = errors.New("binding template ownership mismatch")
	ErrStorage  = errors.New("binding preflight unavailable")
	identifier  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
)

type Intent struct {
	Kind       string
	TargetID   int64
	VersionID  int64
	Ownership  string
	Parameters map[string]json.RawMessage
}
type Preflight struct {
	Valid           bool   `json:"valid"`
	Executable      bool   `json:"executable"`
	Persisted       bool   `json:"persisted"`
	ValidationScope string `json:"validation_scope"`
	TemplateID      string `json:"template_id"`
	VersionID       string `json:"version_id"`
	VersionNumber   string `json:"version_number"`
	Checksum        string `json:"checksum"`
}
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }

// Check takes a consistent read-only snapshot. It cannot reserve eligibility:
// a subsequent binding write/run creation MUST repeat validation atomically.
func (s *Service) Check(ctx context.Context, in Intent) (Preflight, error) {
	if (in.Kind != "ci" && in.Kind != "cd") || in.TargetID <= 0 || in.VersionID <= 0 || !identifier.MatchString(in.Ownership) {
		return Preflight{}, ErrMismatch
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Preflight{}, safeError(err)
	}
	defer tx.Rollback()
	if in.Kind == "ci" {
		var exists int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM apps WHERE app_id=? AND deleted_at IS NULL", in.TargetID).Scan(&exists)
	} else {
		var enabled bool
		err = tx.QueryRowContext(ctx, `SELECT e.enabled FROM app_configs c JOIN apps a ON a.app_id=c.app_id JOIN env_configs e ON e.env=c.env WHERE c.config_id=? AND c.deleted_at IS NULL AND a.deleted_at IS NULL AND e.deleted_at IS NULL`, in.TargetID).Scan(&enabled)
		if err == nil && !enabled {
			return Preflight{}, ErrDisabled
		}
	}
	if err != nil {
		return Preflight{}, safeError(err)
	}
	var templateID int64
	var number uint64
	var kind, appType, targetType, checksum string
	var enabled bool
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT t.template_id,t.kind,COALESCE(t.application_type,''),COALESCE(t.target_type,''),t.enabled,v.version_number,v.spec,v.checksum FROM pipeline_template_versions v JOIN pipeline_templates t ON t.template_id=v.template_id WHERE v.version_id=?`, in.VersionID).Scan(&templateID, &kind, &appType, &targetType, &enabled, &number, &raw, &checksum)
	if err != nil {
		return Preflight{}, safeError(err)
	}
	if kind != in.Kind || (kind == "ci" && appType != in.Ownership) || (kind == "cd" && targetType != in.Ownership) {
		return Preflight{}, ErrMismatch
	}
	if !enabled {
		return Preflight{}, ErrDisabled
	}
	if kind == "ci" {
		if err = tx.QueryRowContext(ctx, "SELECT enabled FROM application_types WHERE type_key=?", appType).Scan(&enabled); err != nil {
			return Preflight{}, safeError(err)
		}
		if !enabled {
			return Preflight{}, ErrDisabled
		}
	}
	// Always inspect the selected immutable version, never the current draft.
	var spec pipelinetemplate.Spec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > pipelinetemplate.MaxRequestBytes || decoder.Decode(&spec) != nil || decoder.Decode(new(any)) != io.EOF || spec.Kind != kind || spec.ApplicationType != appType || spec.TargetType != targetType {
		return Preflight{}, ErrDefinition
	}
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return Preflight{}, ErrDefinition
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != checksum {
		return Preflight{}, ErrDefinition
	}
	if _, err = ResolveParameters(spec, in.Parameters, nil); err != nil {
		return Preflight{}, err
	}
	if err = tx.Commit(); err != nil {
		return Preflight{}, safeError(err)
	}
	return Preflight{Valid: true, ValidationScope: "binding_intent_only", TemplateID: strconv.FormatInt(templateID, 10), VersionID: strconv.FormatInt(in.VersionID, 10), VersionNumber: strconv.FormatUint(number, 10), Checksum: checksum}, nil
}
func safeError(err error) error {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrTarget
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	return ErrStorage
}
