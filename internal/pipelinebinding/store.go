// Package pipelinebinding persists fixed-version CI/CD bindings. A binding
// stores the caller's own parameter layer plus a template version reference; it
// never copies step definitions, resolves secrets or authorizes execution.
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
	"math"
	"strings"
	"time"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-sql-driver/mysql"
)

// MaxStoredParameterBytes bounds the MySQL-normalized binding document. MySQL
// expands JSON whitespace on write, so the compact request bound is not enough.
const MaxStoredParameterBytes = 64 * 1024

var ErrConflict = errors.New("binding revision or uniqueness conflict")

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Binding is metadata only. Parameters are never part of a default read.
type Binding struct {
	ID            int64
	Kind          string
	TargetID      int64
	VersionID     int64
	TemplateID    int64
	VersionNumber uint64
	Checksum      string
	Enabled       bool
	Revision      uint64
	CreatedBy     int64
	UpdatedBy     int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SaveIntent describes one complete binding replacement. Expected zero means
// "no binding may exist yet"; a positive value is the revision being replaced.
type SaveIntent struct {
	Kind       string
	TargetID   int64
	VersionID  int64
	Ownership  string
	Parameters map[string]json.RawMessage
	Enabled    bool
	Expected   uint64
	Actor      int64
}

// bindingTarget resolves a kind to its dedicated table. Both names are fixed
// literals, never caller input.
func bindingTarget(kind string) (string, string, error) {
	switch kind {
	case "ci":
		return "application_ci_bindings", "app_id", nil
	case "cd":
		return "app_config_cd_bindings", "config_id", nil
	}
	return "", "", ErrMismatch
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTarget
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && (me.Number == 1062 || me.Number == 1213 || me.Number == 1205) {
		return ErrConflict
	}
	return ErrStorage
}

// encodeParameters stores only the caller's declared binding layer. Merged
// defaults stay in the immutable version, so a later read cannot mistake a
// template default for an explicitly bound value.
func encodeParameters(parameters map[string]json.RawMessage) ([]byte, error) {
	if len(parameters) > pipelinetemplate.MaxParameters {
		return nil, ErrParameters
	}
	if len(parameters) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(parameters)
	if err != nil || len(raw) > MaxParameterBytes {
		return nil, ErrParameters
	}
	return raw, nil
}

func decodeDefinition(raw []byte) (pipelinetemplate.Spec, error) {
	var spec pipelinetemplate.Spec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > pipelinetemplate.MaxRequestBytes || decoder.Decode(&spec) != nil || decoder.Decode(new(any)) != io.EOF {
		return spec, ErrDefinition
	}
	if len(pipelinetemplate.Validate(spec)) != 0 {
		return spec, ErrDefinition
	}
	return spec, nil
}

type versionOwner struct {
	templateID      int64
	kind            string
	applicationType string
	targetType      string
	templateEnabled bool
	number          uint64
	checksum        string
	spec            pipelinetemplate.Spec
}

// loadVersion reads the immutable version plus its template ownership without
// locking: a published version and a template's list identity never change.
// The stored canonical checksum is re-derived here, so a corrupted row is
// rejected instead of being copied into a binding.
func loadVersion(ctx context.Context, tx *sql.Tx, versionID int64) (versionOwner, error) {
	var out versionOwner
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT t.template_id,t.kind,COALESCE(t.application_type,''),COALESCE(t.target_type,''),t.enabled,v.version_number,v.spec,v.checksum
 FROM pipeline_template_versions v JOIN pipeline_templates t ON t.template_id=v.template_id WHERE v.version_id=?`, versionID).
		Scan(&out.templateID, &out.kind, &out.applicationType, &out.targetType, &out.templateEnabled, &out.number, &raw, &out.checksum)
	if err != nil {
		return out, storageError(err)
	}
	spec, err := decodeDefinition(raw)
	if err != nil {
		return out, err
	}
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return out, ErrDefinition
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != out.checksum || spec.Kind != out.kind ||
		spec.ApplicationType != out.applicationType || spec.TargetType != out.targetType {
		return out, ErrDefinition
	}
	out.spec = spec
	return out, nil
}

// lockOwnership takes locks in the published catalog order (type, then
// template) so a binding write serializes with publish and disable instead of
// racing them. CD templates are not language bound and skip the type lock.
func lockOwnership(ctx context.Context, tx *sql.Tx, owner versionOwner) error {
	if owner.kind == "ci" {
		var enabled bool
		if err := tx.QueryRowContext(ctx, "SELECT enabled FROM application_types WHERE type_key=? FOR UPDATE", owner.applicationType).Scan(&enabled); err != nil {
			return storageError(err)
		}
		if !enabled {
			return ErrDisabled
		}
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM pipeline_templates WHERE template_id=? FOR UPDATE", owner.templateID).Scan(&enabled); err != nil {
		return storageError(err)
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}

// verifyTarget re-reads the binding target inside the write transaction. A
// preflight response or an earlier read is never trusted on its own.
func verifyTarget(ctx context.Context, tx *sql.Tx, kind string, targetID int64) error {
	if kind == "ci" {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM apps WHERE app_id=? AND deleted_at IS NULL", targetID).Scan(&exists); err != nil {
			return storageError(err)
		}
		return nil
	}
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT e.enabled FROM app_configs c JOIN apps a ON a.app_id=c.app_id JOIN env_configs e ON e.env=c.env
 WHERE c.config_id=? AND c.deleted_at IS NULL AND a.deleted_at IS NULL AND e.deleted_at IS NULL`, targetID).Scan(&enabled)
	if err != nil {
		return storageError(err)
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}

func validateIntent(in SaveIntent) (string, string, error) {
	table, target, err := bindingTarget(in.Kind)
	if err != nil {
		return "", "", err
	}
	if in.TargetID <= 0 || in.VersionID <= 0 || in.Actor <= 0 || in.Expected == math.MaxUint64 || !identifier.MatchString(in.Ownership) {
		return "", "", ErrMismatch
	}
	return table, target, nil
}

// Save creates (Expected zero) or replaces a binding under revision CAS. The
// ownership revalidation, the parameter check and the write are one transaction.
func (s *Store) Save(ctx context.Context, in SaveIntent) (Binding, error) {
	table, target, err := validateIntent(in)
	if err != nil {
		return Binding{}, err
	}
	stored, err := encodeParameters(in.Parameters)
	if err != nil {
		return Binding{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Binding{}, storageError(err)
	}
	defer tx.Rollback()
	owner, err := loadVersion(ctx, tx, in.VersionID)
	if err != nil {
		return Binding{}, err
	}
	if owner.kind != in.Kind ||
		(owner.kind == "ci" && owner.applicationType != in.Ownership) ||
		(owner.kind == "cd" && owner.targetType != in.Ownership) {
		return Binding{}, ErrMismatch
	}
	if !owner.templateEnabled {
		return Binding{}, ErrDisabled
	}
	// Validation is deliberately before the lock so invalid parameters cannot
	// hold type or template locks while they are rejected.
	if _, err = ResolveParameters(owner.spec, in.Parameters, nil); err != nil {
		return Binding{}, err
	}
	if err = lockOwnership(ctx, tx, owner); err != nil {
		return Binding{}, err
	}
	if err = verifyTarget(ctx, tx, in.Kind, in.TargetID); err != nil {
		return Binding{}, err
	}
	out := Binding{
		Kind: in.Kind, TargetID: in.TargetID, VersionID: in.VersionID, TemplateID: owner.templateID,
		VersionNumber: owner.number, Checksum: owner.checksum, Enabled: in.Enabled,
		CreatedBy: in.Actor, UpdatedBy: in.Actor,
	}
	var id, current int64
	err = tx.QueryRowContext(ctx, "SELECT binding_id,revision FROM "+table+" WHERE "+target+"=? FOR UPDATE", in.TargetID).Scan(&id, &current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if in.Expected != 0 {
			return Binding{}, ErrTarget
		}
		result, insertErr := tx.ExecContext(ctx, "INSERT INTO "+table+"("+target+",version_id,parameters,enabled,created_by_user_id,updated_by_user_id) VALUES(?,?,?,?,?,?)",
			in.TargetID, in.VersionID, stored, in.Enabled, in.Actor, in.Actor)
		if insertErr != nil {
			// The unique target key, not a prior read, decides concurrent creates.
			return Binding{}, storageError(insertErr)
		}
		if out.ID, err = result.LastInsertId(); err != nil {
			return Binding{}, storageError(err)
		}
		out.Revision = 1
	case err != nil:
		return Binding{}, storageError(err)
	default:
		if in.Expected == 0 || uint64(current) != in.Expected {
			return Binding{}, ErrConflict
		}
		result, updateErr := tx.ExecContext(ctx, "UPDATE "+table+" SET version_id=?,parameters=?,enabled=?,revision=revision+1,updated_by_user_id=?,updated_at=UTC_TIMESTAMP(6) WHERE "+target+"=? AND revision=?",
			in.VersionID, stored, in.Enabled, in.Actor, in.TargetID, in.Expected)
		if updateErr != nil {
			return Binding{}, storageError(updateErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return Binding{}, storageError(affectedErr)
		}
		if affected != 1 {
			return Binding{}, ErrConflict
		}
		out.ID = id
		out.Revision = in.Expected + 1
	}
	if err = checkStoredParameters(ctx, tx, table, target, in.TargetID); err != nil {
		return Binding{}, err
	}
	if err = tx.Commit(); err != nil {
		return Binding{}, storageError(err)
	}
	return out, nil
}

// MySQL expands JSON whitespace on write. Re-read the stored document in the
// same transaction so a document that only exceeds the bound after
// normalization cannot commit.
func checkStoredParameters(ctx context.Context, tx *sql.Tx, table, target string, targetID int64) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT parameters FROM "+table+" WHERE "+target+"=?", targetID).Scan(&raw); err != nil {
		return storageError(err)
	}
	if len(raw) > MaxStoredParameterBytes {
		return ErrParameters
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil || document == nil || len(document) > pipelinetemplate.MaxParameters {
		return ErrParameters
	}
	return nil
}

// bindingColumns qualifies every column: a binding read joins the immutable
// version, whose table also has version_id, enabled and created_at.
func bindingColumns(alias string) string {
	columns := []string{"binding_id", "version_id", "enabled", "revision", "created_by_user_id", "updated_by_user_id", "created_at", "updated_at"}
	for index, column := range columns {
		columns[index] = alias + "." + column
	}
	return strings.Join(columns, ",")
}

// Get reads binding metadata for one target. It does not merge template
// defaults, and it deliberately still reports a binding whose target was later
// soft-deleted so that decision stays auditable.
func (s *Store) Get(ctx context.Context, kind string, targetID int64) (Binding, error) {
	table, target, err := bindingTarget(kind)
	if err != nil {
		return Binding{}, err
	}
	if targetID <= 0 {
		return Binding{}, ErrTarget
	}
	out := Binding{Kind: kind, TargetID: targetID}
	err = s.db.QueryRowContext(ctx, "SELECT "+bindingColumns("b")+",v.template_id,v.version_number,v.checksum FROM "+table+
		" b JOIN pipeline_template_versions v ON v.version_id=b.version_id WHERE b."+target+"=?", targetID).
		Scan(&out.ID, &out.VersionID, &out.Enabled, &out.Revision, &out.CreatedBy, &out.UpdatedBy, &out.CreatedAt, &out.UpdatedAt, &out.TemplateID, &out.VersionNumber, &out.Checksum)
	return out, storageError(err)
}

// StoredParameters is the persisted parameter layer with the revision it was
// read at, so one read cannot report a document from a different revision.
type StoredParameters struct {
	BindingID int64
	Revision  uint64
	Document  map[string]json.RawMessage
}

// ReadParameters returns only the stored binding layer. The document is not
// re-resolved against the pinned version here: run creation must revalidate
// ownership, enablement and parameters inside its own transaction.
func (s *Store) ReadParameters(ctx context.Context, kind string, targetID int64) (StoredParameters, error) {
	table, target, err := bindingTarget(kind)
	if err != nil {
		return StoredParameters{}, err
	}
	if targetID <= 0 {
		return StoredParameters{}, ErrTarget
	}
	var out StoredParameters
	var raw []byte
	if err := s.db.QueryRowContext(ctx, "SELECT binding_id,revision,parameters FROM "+table+" WHERE "+target+"=?", targetID).
		Scan(&out.BindingID, &out.Revision, &raw); err != nil {
		return StoredParameters{}, storageError(err)
	}
	if len(raw) > MaxStoredParameterBytes {
		return StoredParameters{}, ErrStorage
	}
	if err := json.Unmarshal(raw, &out.Document); err != nil || out.Document == nil || len(out.Document) > pipelinetemplate.MaxParameters {
		return StoredParameters{}, ErrStorage
	}
	return out, nil
}
