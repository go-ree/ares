package db

import (
	"context"
	"fmt"
)

const (
	versionedSchemaMigrationVersion = "20260903_003_versioned_migrations"
	versionedSchemaMigrationPayload = "semantic-schema-manifest-v1|14-managed-tables|apps-auto-increment-min-10000|ordered-index-semantics|pipeline-fk-actions|exact-table-and-column-encoding"
)

func newVersionedSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 4, version: versionedSchemaMigrationVersion,
		description: "收敛版本化迁移与 schema 所有权", compatibleMin: 4, compatibleMax: 4,
		payload:          versionedSchemaMigrationPayload,
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyVersionedSchemaResumeState() },
		up:               func(session *migrationSession) error { return session.migrateVersionedSchema() },
		verify:           func(session *migrationSession) error { return session.verifyCurrentSchema() },
	}
}

func (s *migrationSession) verifyVersionedSchemaResumeState() error {
	if err := s.verifySemanticSchemaStates("版本化 schema 迁移恢复状态", epoch4ResumeSchemaStates()); err != nil {
		return err
	}
	return s.verifyEpochDataContracts(3)
}

// migrateVersionedSchema is the W04 ownership boundary: after this migration,
// schema changes belong exclusively to the explicit migrator. Setting the
// AUTO_INCREMENT floor is safe to repeat; MySQL keeps a higher value when the
// table already contains rows with larger identifiers.
func (s *migrationSession) migrateVersionedSchema() error {
	ctx, cancel := s.operationContext()
	_, err := s.executor.ExecContext(ctx, "ALTER TABLE `apps` AUTO_INCREMENT = 10000")
	cancel()
	if err != nil {
		return fmt.Errorf("set apps AUTO_INCREMENT floor to 10000: %w", err)
	}
	return s.verifyCurrentSchema()
}

// verifyCurrentSchema is the complete, immutable epoch-4 contract. Besides
// the full structural snapshot it explicitly retains the canonical text-value
// invariant from epoch 1 and normalized active-environment invariant from
// epoch 2. A future epoch must define a new verifier and manifest rather than
// changing this one.
func (s *migrationSession) verifyCurrentSchema() error {
	ctx, cancel := s.operationContext()
	diffs, err := s.epoch4SchemaDiffs(ctx)
	cancel()
	if err != nil {
		return err
	}
	if len(diffs) > 0 {
		return &SchemaStateError{Problems: diffs}
	}
	return s.verifyEpochDataContracts(4)
}

func (s *migrationSession) epoch4SchemaDiffs(ctx context.Context) ([]string, error) {
	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return nil, err
	}
	return compareSemanticSchema(snapshot, epoch4SemanticSchemaManifest), nil
}
