package db

import (
	"database/sql"
	"strings"
	"testing"
)

func TestPreservedTextColumnDefinition(t *testing.T) {
	state := textColumnState{
		columnType:   "varchar(512)",
		characterSet: sql.NullString{String: "utf8mb4", Valid: true},
		collation:    sql.NullString{String: "utf8mb4_bin", Valid: true},
		comment:      `operator's \ path`,
		extra:        "INVISIBLE",
	}

	definition, err := preservedTextColumnDefinition(state, false, false)
	if err != nil {
		t.Fatalf("preservedTextColumnDefinition() error = %v", err)
	}
	for _, expected := range []string{
		"varchar(512)",
		"CHARACTER SET utf8mb4",
		"COLLATE utf8mb4_bin",
		"NOT NULL",
		`COMMENT 'operator''s \\ path'`,
		"INVISIBLE",
	} {
		if !strings.Contains(definition, expected) {
			t.Fatalf("definition %q does not preserve %q", definition, expected)
		}
	}
}

func TestPreservedTextColumnDefinitionRejectsUnsafeMetadata(t *testing.T) {
	tests := []textColumnState{
		{columnType: "text"},
		{columnType: "varchar(255); DROP TABLE apps"},
		{columnType: "varchar(255)", characterSet: sql.NullString{String: "utf8mb4;", Valid: true}},
		{columnType: "varchar(255)", collation: sql.NullString{String: "utf8mb4-bin", Valid: true}},
	}
	for _, state := range tests {
		if _, err := preservedTextColumnDefinition(state, true, false); err == nil {
			t.Fatalf("preservedTextColumnDefinition(%#v) accepted unsafe metadata", state)
		}
	}
}

func TestQuoteMigrationSQLStringHonorsSQLMode(t *testing.T) {
	value := `operator's \ path`
	if got := quoteMigrationSQLString(value, false); got != `'operator''s \\ path'` {
		t.Fatalf("default SQL mode quote = %q", got)
	}
	if got := quoteMigrationSQLString(value, true); got != `'operator''s \ path'` {
		t.Fatalf("NO_BACKSLASH_ESCAPES quote = %q", got)
	}
}

func TestLegacyNullPredicatesAreScoped(t *testing.T) {
	nullish := legacyNullishSQL("value")
	nonNull := legacyNonNullTextSQL("value")
	for _, fragment := range []string{"IS NULL", "= ''", "[[:space:]]*null"} {
		if !strings.Contains(nullish, fragment) {
			t.Fatalf("legacyNullishSQL() = %q, missing %q", nullish, fragment)
		}
	}
	if !strings.Contains(nonNull, "IS NOT NULL") {
		t.Fatalf("legacyNonNullTextSQL() = %q, want explicit non-null guard", nonNull)
	}
}

func TestPlanPipelineCombinationCollationAlignment(t *testing.T) {
	source := characterColumnDefinition{
		characterSet: "utf8mb4",
		collation:    "utf8mb4_unicode_ci",
	}
	matching := source
	newTableDefault := characterColumnDefinition{
		characterSet: "utf8mb4",
		collation:    "utf8mb4_0900_ai_ci",
	}
	wantDDL := "ALTER TABLE pipelines_job_combination CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"

	tests := []struct {
		name          string
		targets       []characterColumnDefinition
		hasRows       bool
		wantDDL       string
		wantPopulated bool
	}{
		{
			name:    "matching definitions are idempotent",
			targets: []characterColumnDefinition{matching, matching},
		},
		{
			name:    "new empty table inherits a different MySQL 8 default",
			targets: []characterColumnDefinition{newTableDefault, newTableDefault},
			wantDDL: wantDDL,
		},
		{
			name:    "interrupted boot resumes with one unmatched empty column",
			targets: []characterColumnDefinition{matching, newTableDefault},
			wantDDL: wantDDL,
		},
		{
			name:          "populated table is not changed",
			targets:       []characterColumnDefinition{newTableDefault, newTableDefault},
			hasRows:       true,
			wantPopulated: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planPipelineCombinationCollationAlignment(source, test.targets, test.hasRows)
			if err != nil {
				t.Fatalf("planPipelineCombinationCollationAlignment() error = %v", err)
			}
			if plan.alterTableSQL != test.wantDDL {
				t.Fatalf("alterTableSQL = %q, want %q", plan.alterTableSQL, test.wantDDL)
			}
			if plan.skipPopulated != test.wantPopulated {
				t.Fatalf("skipPopulated = %v, want %v", plan.skipPopulated, test.wantPopulated)
			}
		})
	}
}

func TestPlanPipelineCombinationCollationAlignmentRejectsUnsafeIdentifiers(t *testing.T) {
	valid := characterColumnDefinition{
		characterSet: "utf8mb4",
		collation:    "utf8mb4_unicode_ci",
	}
	tests := []struct {
		name    string
		source  characterColumnDefinition
		targets []characterColumnDefinition
	}{
		{
			name: "unsafe source character set",
			source: characterColumnDefinition{
				characterSet: "utf8mb4; DROP TABLE pipelines",
				collation:    valid.collation,
			},
			targets: []characterColumnDefinition{valid},
		},
		{
			name: "unsafe source collation",
			source: characterColumnDefinition{
				characterSet: valid.characterSet,
				collation:    "utf8mb4_unicode_ci --",
			},
			targets: []characterColumnDefinition{valid},
		},
		{
			name:   "unsafe target metadata",
			source: valid,
			targets: []characterColumnDefinition{{
				characterSet: valid.characterSet,
				collation:    "utf8mb4-unicode-ci",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := planPipelineCombinationCollationAlignment(test.source, test.targets, false); err == nil {
				t.Fatal("planPipelineCombinationCollationAlignment() accepted unsafe metadata")
			}
		})
	}
}
