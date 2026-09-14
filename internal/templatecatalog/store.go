// Package templatecatalog stores definitions only; publishing never executes a pipeline.
// Callers must authorize actors before using this internal service.
package templatecatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-sql-driver/mysql"
)

var (
	ErrInvalid  = errors.New("invalid template catalog request")
	ErrNotFound = errors.New("template catalog resource not found")
	ErrConflict = errors.New("template catalog revision or key conflict")
	ErrDisabled = errors.New("template catalog resource disabled")
	ErrStorage  = errors.New("template catalog storage unavailable")
	keyPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

type ApplicationType struct {
	Key, Name string
	Enabled   bool
	Revision  uint64
}
type Template struct {
	ID                                     int64
	Key, Kind, ApplicationType, TargetType string
	Enabled                                bool
	Revision                               uint64
	Draft                                  json.RawMessage
}
type Version struct {
	ID, TemplateID         int64
	Number, SourceRevision uint64
	Spec                   json.RawMessage
	Checksum               string
	CreatedBy              int64
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
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
func validRevision(r uint64) bool { return r > 0 && r < math.MaxUint64 }
func validType(key, name string) bool {
	return keyPattern.MatchString(key) && utf8.ValidString(name) && strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 120
}
func (s *Store) CreateType(ctx context.Context, key, name string) error {
	if !validType(key, name) {
		return ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO application_types(type_key,name) VALUES(?,?)", key, name)
	return storageError(err)
}
func (s *Store) GetType(ctx context.Context, key string) (ApplicationType, error) {
	var v ApplicationType
	if !keyPattern.MatchString(key) {
		return v, ErrInvalid
	}
	err := s.db.QueryRowContext(ctx, "SELECT type_key,name,enabled,revision FROM application_types WHERE type_key=?", key).Scan(&v.Key, &v.Name, &v.Enabled, &v.Revision)
	return v, storageError(err)
}
func (s *Store) UpdateType(ctx context.Context, key, name string, enabled bool, expected uint64) error {
	if !validType(key, name) || !validRevision(expected) {
		return ErrInvalid
	}
	// This UPDATE takes the same type row lock as publish, with no child locks.
	result, err := s.db.ExecContext(ctx, "UPDATE application_types SET name=?,enabled=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE type_key=? AND revision=?", name, enabled, key, expected)
	return changed(result, err)
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return storageError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return storageError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func encode(spec pipelinetemplate.Spec) ([]byte, error) {
	if len(pipelinetemplate.Validate(spec)) != 0 {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(spec)
	if err != nil || len(raw) > pipelinetemplate.MaxRequestBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}
func lockType(ctx context.Context, tx *sql.Tx, key string, requireEnabled bool) error {
	if key == "" {
		return nil
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM application_types WHERE type_key=? FOR UPDATE", key).Scan(&enabled); err != nil {
		return storageError(err)
	}
	if requireEnabled && !enabled {
		return ErrDisabled
	}
	return nil
}
func (s *Store) CreateTemplate(ctx context.Context, key string, spec pipelinetemplate.Spec, actor int64) (int64, error) {
	raw, err := encode(spec)
	if err != nil || !keyPattern.MatchString(key) || actor <= 0 {
		return 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, storageError(err)
	}
	defer tx.Rollback()
	if err = lockType(ctx, tx, spec.ApplicationType, true); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO pipeline_templates(template_key,kind,application_type,target_type,draft,created_by_user_id) VALUES(?,?,NULLIF(?,''),NULLIF(?,''),?,?)`, key, spec.Kind, spec.ApplicationType, spec.TargetType, raw, actor)
	if err != nil {
		return 0, storageError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, storageError(err)
	}
	if err = checkStoredDraft(ctx, tx, id); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, storageError(err)
	}
	return id, nil
}

// MySQL expands JSON whitespace. Enforce the schema's stored-byte limit, not
// just the compact request size, before committing a draft. The frozen v1
// validator also bounds nested RawMessage fields using their stored encoding.
func checkStoredDraft(ctx context.Context, tx *sql.Tx, id int64) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT draft FROM pipeline_templates WHERE template_id=?", id).Scan(&raw); err != nil {
		return storageError(err)
	}
	var spec pipelinetemplate.Spec
	if len(raw) > pipelinetemplate.MaxRequestBytes || json.Unmarshal(raw, &spec) != nil || len(pipelinetemplate.Validate(spec)) != 0 {
		return ErrInvalid
	}
	return nil
}

type scanner interface{ Scan(...any) error }

const templateColumns = "template_id,template_key,kind,COALESCE(application_type,''),COALESCE(target_type,''),enabled,revision,draft"

func scanTemplate(row scanner) (Template, error) {
	var v Template
	err := row.Scan(&v.ID, &v.Key, &v.Kind, &v.ApplicationType, &v.TargetType, &v.Enabled, &v.Revision, &v.Draft)
	return v, storageError(err)
}
func (s *Store) GetTemplate(ctx context.Context, id int64) (Template, error) {
	if id <= 0 {
		return Template{}, ErrInvalid
	}
	return scanTemplate(s.db.QueryRowContext(ctx, "SELECT "+templateColumns+" FROM pipeline_templates WHERE template_id=?", id))
}

// lockedTemplate reads immutable ownership before acquiring type -> template locks.
func (s *Store) lockedTemplate(ctx context.Context, tx *sql.Tx, id int64, requireEnabled bool) (Template, error) {
	var owner string
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(application_type,'') FROM pipeline_templates WHERE template_id=?", id).Scan(&owner); err != nil {
		return Template{}, storageError(err)
	}
	if err := lockType(ctx, tx, owner, requireEnabled); err != nil {
		return Template{}, err
	}
	v, err := scanTemplate(tx.QueryRowContext(ctx, "SELECT "+templateColumns+" FROM pipeline_templates WHERE template_id=? FOR UPDATE", id))
	if err == nil && v.ApplicationType != owner {
		return Template{}, ErrConflict
	}
	return v, err
}
func (s *Store) UpdateTemplate(ctx context.Context, id int64, expected uint64, spec pipelinetemplate.Spec, enabled bool) error {
	raw, err := encode(spec)
	if err != nil || id <= 0 || !validRevision(expected) {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	v, err := s.lockedTemplate(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if v.Revision != expected {
		return ErrConflict
	}
	if v.Kind != spec.Kind || v.ApplicationType != spec.ApplicationType || v.TargetType != spec.TargetType {
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, "UPDATE pipeline_templates SET draft=?,enabled=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE template_id=? AND revision=?", raw, enabled, id, expected)
	if err = changed(result, err); err != nil {
		return err
	}
	if err = checkStoredDraft(ctx, tx, id); err != nil {
		return err
	}
	return storageError(tx.Commit())
}

// Publish consumes expected revision. Retrying a consumed revision returns conflict,
// never a second version. A caller can retrieve the immutable version separately.
func (s *Store) Publish(ctx context.Context, id int64, expected uint64, actor int64) (Version, error) {
	var out Version
	if id <= 0 || actor <= 0 || !validRevision(expected) {
		return out, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return out, storageError(err)
	}
	defer tx.Rollback()
	v, err := s.lockedTemplate(ctx, tx, id, true)
	if err != nil {
		return out, err
	}
	if v.Revision != expected {
		return out, ErrConflict
	}
	if !v.Enabled {
		return out, ErrDisabled
	}
	var spec pipelinetemplate.Spec
	decoder := json.NewDecoder(strings.NewReader(string(v.Draft)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&spec) != nil {
		return out, ErrInvalid
	}
	raw, err := encode(spec)
	if err != nil || spec.Kind != v.Kind || spec.ApplicationType != v.ApplicationType || spec.TargetType != v.TargetType {
		return out, ErrInvalid
	}
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return out, ErrInvalid
	}
	digest := sha256.Sum256(canonical)
	out = Version{TemplateID: id, SourceRevision: expected, Spec: raw, Checksum: hex.EncodeToString(digest[:]), CreatedBy: actor}
	// Parent lock serializes numbering. No locking read on immutable tables: the
	// runtime principal intentionally has no UPDATE privilege there.
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_number),0) FROM pipeline_template_versions WHERE template_id=?", id).Scan(&out.Number); err != nil {
		return Version{}, storageError(err)
	}
	if out.Number == math.MaxUint64 {
		return Version{}, ErrConflict
	}
	out.Number++
	result, err := tx.ExecContext(ctx, "INSERT INTO pipeline_template_versions(template_id,version_number,source_revision,spec,checksum,created_by_user_id) VALUES(?,?,?,?,?,?)", id, out.Number, expected, raw, out.Checksum, actor)
	if err != nil {
		return Version{}, storageError(err)
	}
	out.ID, err = result.LastInsertId()
	if err != nil {
		return Version{}, storageError(err)
	}
	result, err = tx.ExecContext(ctx, "UPDATE pipeline_templates SET revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE template_id=? AND revision=?", id, expected)
	if err = changed(result, err); err != nil {
		return Version{}, err
	}
	if err = tx.Commit(); err != nil {
		return Version{}, storageError(err)
	}
	return out, nil
}
func (s *Store) GetVersion(ctx context.Context, id int64, number uint64) (Version, error) {
	var v Version
	if id <= 0 || number == 0 {
		return v, ErrInvalid
	}
	err := s.db.QueryRowContext(ctx, "SELECT version_id,template_id,version_number,source_revision,spec,checksum,created_by_user_id FROM pipeline_template_versions WHERE template_id=? AND version_number=?", id, number).Scan(&v.ID, &v.TemplateID, &v.Number, &v.SourceRevision, &v.Spec, &v.Checksum, &v.CreatedBy)
	return v, storageError(err)
}
