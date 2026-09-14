package templatecatalog_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-ree/ares/internal/templatecatalog"
	"github.com/go-sql-driver/mysql"
)

func TestMySQLTemplateCatalog(t *testing.T) {
	dsn := os.Getenv("ARES_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("ARES_TEST_MYSQL_DSN not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "catalog_" + hex.EncodeToString(suffix)
	user := name
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(admin, "CREATE DATABASE `"+name+"`")
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	cfg.ParseTime = true
	database, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := schemadb.MigrateUp(ctx, cfg.FormatDSN(), "", 45*time.Second, 10*time.Second)
	if err != nil || !status.Compatible() {
		t.Fatalf("migration: %v", err)
	}
	exec(admin, "CREATE USER '"+user+"'@'%' IDENTIFIED BY 'isolated-test-only'")
	defer admin.Exec("DROP USER '" + user + "'@'%'")
	for _, table := range []string{"application_types", "pipeline_templates"} {
		exec(admin, "GRANT SELECT,INSERT,UPDATE ON `"+name+"`."+table+" TO '"+user+"'@'%'")
	}
	exec(admin, "GRANT SELECT,INSERT ON `"+name+"`.pipeline_template_versions TO '"+user+"'@'%'")
	cfg.User = user
	cfg.Passwd = "isolated-test-only"
	runtime, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := templatecatalog.New(runtime)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	require(store.CreateType(ctx, "custom-java", "自定义 Java"))
	if store.CreateType(ctx, "custom-java", "重复") != templatecatalog.ErrConflict {
		t.Fatal("duplicate key")
	}
	artifact := pipelinetemplate.ArtifactType{Kind: "file", MediaType: "application/java-archive", Simulated: true}
	spec := pipelinetemplate.Spec{SchemaVersion: 1, Kind: "ci", Name: "Java 示例", ApplicationType: "custom-java", Steps: []pipelinetemplate.Step{{Key: "build", Uses: "mock.build@v1", Outputs: map[string]pipelinetemplate.ArtifactType{"jar": artifact}}}, Outputs: map[string]pipelinetemplate.Input{"jar": {From: "steps.build.outputs.jar", Type: artifact}}}
	id, err := store.CreateTemplate(ctx, "java-ci", spec, 1)
	require(err)
	// All concurrent writers share an expected revision: exactly one may win.
	race := func(call func() error) {
		t.Helper()
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- call() }()
		}
		close(start)
		wg.Wait()
		close(results)
		wins := 0
		for e := range results {
			if e == nil {
				wins++
			} else if !errors.Is(e, templatecatalog.ErrConflict) {
				t.Fatal(e)
			}
		}
		if wins != 1 {
			t.Fatalf("wins=%d", wins)
		}
	}
	race(func() error { return store.UpdateTemplate(ctx, id, 1, spec, true) })
	race(func() error { _, e := store.Publish(ctx, id, 2, 1); return e })
	first, err := store.GetVersion(ctx, id, 1)
	require(err)
	if first.SourceRevision != 2 || first.Number != 1 || first.CreatedBy != 1 {
		t.Fatalf("wrong version: %+v", first)
	}
	current, err := store.GetTemplate(ctx, id)
	require(err)
	if current.Revision != 3 {
		t.Fatal("publish did not consume revision")
	}
	spec.Name = "新草稿"
	require(store.UpdateTemplate(ctx, id, 3, spec, true))
	second, err := store.Publish(ctx, id, 4, 2)
	require(err)
	if second.Number != 2 || second.Checksum == first.Checksum {
		t.Fatal("new immutable version")
	}
	old, err := store.GetVersion(ctx, id, 1)
	require(err)
	if old.Checksum != first.Checksum {
		t.Fatal("historical version changed")
	}
	spec.ApplicationType = "java"
	if store.UpdateTemplate(ctx, id, 5, spec, true) != templatecatalog.ErrInvalid {
		t.Fatal("ownership changed")
	}
	spec.ApplicationType = "custom-java"
	race(func() error { return store.UpdateType(ctx, "custom-java", "自定义 Java", false, 1) })
	if _, err = store.Publish(ctx, id, 5, 1); err != templatecatalog.ErrDisabled {
		t.Fatal("disabled type published")
	}
	if _, err = store.CreateTemplate(ctx, "disabled-ci", spec, 1); err != templatecatalog.ErrDisabled {
		t.Fatal("disabled type accepted new template")
	}
	require(store.UpdateType(ctx, "custom-java", "自定义 Java", true, 2))
	require(store.UpdateTemplate(ctx, id, 5, spec, false))
	if _, err = store.Publish(ctx, id, 6, 1); err != templatecatalog.ErrDisabled {
		t.Fatal("disabled template published")
	}
	require(store.UpdateTemplate(ctx, id, 6, spec, true))
	// A failure after INSERT must roll back both publication and revision.
	exec(database, "CREATE TRIGGER catalog_fail_update BEFORE UPDATE ON pipeline_templates FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='private failure'")
	if _, err = store.Publish(ctx, id, 7, 1); err != templatecatalog.ErrStorage {
		t.Fatalf("rollback failure: %v", err)
	}
	exec(database, "DROP TRIGGER catalog_fail_update")
	current, err = store.GetTemplate(ctx, id)
	require(err)
	if current.Revision != 7 {
		t.Fatal("failed publish consumed revision")
	}
	if _, err = store.GetVersion(ctx, id, 3); err != templatecatalog.ErrNotFound {
		t.Fatal("failed publish left version")
	}
	third, err := store.Publish(ctx, id, 7, 1)
	require(err)
	if third.Number != 3 {
		t.Fatal("version sequence gap")
	}
	// Deterministic type-disable ordering: publish cannot pass an uncommitted
	// disable, and must re-read its committed enabled value after waiting.
	tx, err := database.BeginTx(ctx, nil)
	require(err)
	_, err = tx.ExecContext(ctx, "UPDATE application_types SET enabled=0,revision=revision+1 WHERE type_key='custom-java'")
	require(err)
	waitCtx, waitCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = store.Publish(waitCtx, id, 8, 1)
	waitCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		tx.Rollback()
		t.Fatalf("publish bypassed type lock: %v", err)
	}
	require(tx.Commit())
	if _, err = store.Publish(ctx, id, 8, 1); err != templatecatalog.ErrDisabled {
		t.Fatal("publish ignored committed disable")
	}
	// Compact JSON can fit while MySQL's spaced representation exceeds 64 KiB.
	large := spec
	large.ApplicationType = "java"
	large.Steps = nil
	for i := 0; i < 8; i++ {
		large.Steps = append(large.Steps, pipelinetemplate.Step{Key: string(rune('a' + i)), Uses: "mock.build@v1", With: json.RawMessage(`{"items":[` + strings.Repeat(`0,`, 3999) + `0]}`), Outputs: map[string]pipelinetemplate.ArtifactType{"jar": artifact}})
	}
	large.Outputs = map[string]pipelinetemplate.Input{"jar": {From: "steps.a.outputs.jar", Type: artifact}}
	if _, err = store.CreateTemplate(ctx, "oversized", large, 1); err != templatecatalog.ErrInvalid {
		t.Fatalf("stored size accepted: %v", err)
	}
	var count int
	require(database.QueryRow("SELECT COUNT(*) FROM pipeline_templates WHERE template_key='oversized'").Scan(&count))
	if count != 0 {
		t.Fatal("oversized insert was not rolled back")
	}
	large.Steps = large.Steps[:1]
	if len(pipelinetemplate.Validate(large)) != 0 {
		t.Fatal("test input must be valid before MySQL expansion")
	}
	if _, err = store.CreateTemplate(ctx, "oversized-with", large, 1); err != templatecatalog.ErrInvalid {
		t.Fatalf("stored nested limit accepted: %v", err)
	}
	for _, q := range []string{"UPDATE pipeline_template_versions SET checksum=checksum", "DELETE FROM pipeline_template_versions", "DELETE FROM pipeline_templates", "DELETE FROM application_types"} {
		if _, err = runtime.Exec(q); err == nil {
			t.Fatal("runtime immutable privilege violation")
		}
	}
	// CD is target-bound, without an application type dependency.
	cd := pipelinetemplate.Spec{SchemaVersion: 1, Kind: "cd", Name: "部署", TargetType: "kubernetes", Inputs: map[string]pipelinetemplate.ArtifactType{"jar": artifact}, Steps: []pipelinetemplate.Step{{Key: "deploy", Uses: "mock.deploy@v1", Inputs: map[string]pipelinetemplate.Input{"jar": {From: "inputs.jar", Type: artifact}}}}}
	cdID, err := store.CreateTemplate(ctx, "cd-test", cd, 1)
	require(err)
	_, err = store.Publish(ctx, cdID, 1, 1)
	require(err)
	if err = schemadb.CheckRuntimeCompatibility(ctx, database); err != nil {
		t.Fatalf("stored data violates schema: %v", err)
	}
}
