package db

import "testing"

func TestMigrationManagedTablesPreserveExternalIndexes(t *testing.T) {
	if !migrationManagedTableSyncOptions.IgnoreDropIndices {
		t.Fatal("migration-managed non-unique indexes must survive Xorm schema sync")
	}
	if !migrationManagedTableSyncOptions.IgnoreConstrains {
		t.Fatal("migration-managed unique constraints must survive Xorm schema sync")
	}
}
