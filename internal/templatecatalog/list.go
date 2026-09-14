package templatecatalog

import "context"

// Lists use bounded keyset pagination; no draft/spec blobs are selected.
// The API requests limit+1 to determine has_more without a COUNT scan.
func (s *Store) ListTypes(ctx context.Context, after string, limit int) ([]ApplicationType, error) {
	if limit < 1 || limit > 101 || (after != "" && !keyPattern.MatchString(after)) {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT type_key,name,enabled,revision FROM application_types WHERE type_key>? ORDER BY type_key LIMIT ?", after, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make([]ApplicationType, 0)
	for rows.Next() {
		var v ApplicationType
		if err = rows.Scan(&v.Key, &v.Name, &v.Enabled, &v.Revision); err != nil {
			return nil, storageError(err)
		}
		out = append(out, v)
	}
	return out, storageError(rows.Err())
}
func (s *Store) ListTemplates(ctx context.Context, after int64, limit int) ([]Template, error) {
	if after < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT template_id,template_key,kind,COALESCE(application_type,''),COALESCE(target_type,''),enabled,revision FROM pipeline_templates WHERE template_id>? ORDER BY template_id LIMIT ?", after, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make([]Template, 0)
	for rows.Next() {
		var v Template
		if err = rows.Scan(&v.ID, &v.Key, &v.Kind, &v.ApplicationType, &v.TargetType, &v.Enabled, &v.Revision); err != nil {
			return nil, storageError(err)
		}
		out = append(out, v)
	}
	return out, storageError(rows.Err())
}
func (s *Store) ListVersions(ctx context.Context, id int64, after uint64, limit int) ([]Version, error) {
	if id <= 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM pipeline_templates WHERE template_id=?", id).Scan(&exists); err != nil {
		return nil, storageError(err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version_id,template_id,version_number,source_revision,checksum,created_by_user_id FROM pipeline_template_versions WHERE template_id=? AND version_number>? ORDER BY version_number LIMIT ?", id, after, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	out := make([]Version, 0)
	for rows.Next() {
		var v Version
		if err = rows.Scan(&v.ID, &v.TemplateID, &v.Number, &v.SourceRevision, &v.Checksum, &v.CreatedBy); err != nil {
			return nil, storageError(err)
		}
		out = append(out, v)
	}
	return out, storageError(rows.Err())
}
