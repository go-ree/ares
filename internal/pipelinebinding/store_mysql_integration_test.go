package pipelinebinding_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/canonicaljson"
	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinebinding"
	"github.com/go-sql-driver/mysql"
)

const (
	bindingTestCISpec     = `{"schema_version":1,"kind":"ci","name":"Java 构建","application_type":"java","parameters":{"repo":{"type":"string","required":true},"profile":{"type":"string","default":"release","allow_override":true},"tests":{"type":"boolean","default":false}},"steps":[{"key":"build","uses":"mock.build@v1","outputs":{"jar":{"kind":"file","media_type":"application/java-archive","simulated":true}}}],"outputs":{"jar":{"from":"steps.build.outputs.jar","type":{"kind":"file","media_type":"application/java-archive","simulated":true}}}}`
	bindingTestCDSpec     = `{"schema_version":1,"kind":"cd","name":"部署","target_type":"kubernetes","inputs":{"image":{"kind":"oci_image","media_type":"application/vnd.oci.image.manifest.v1+json","simulated":true}},"steps":[{"key":"deploy","uses":"mock.deploy@v1","inputs":{"image":{"from":"inputs.image","type":{"kind":"oci_image","media_type":"application/vnd.oci.image.manifest.v1+json","simulated":true}}}}]}`
	bindingTestPythonSpec = `{"schema_version":1,"kind":"ci","name":"Python 构建","application_type":"python","parameters":{"repo":{"type":"string","required":true}},"steps":[{"key":"build","uses":"mock.build@v1","outputs":{"wheel":{"kind":"file","media_type":"application/zip","simulated":true}}}],"outputs":{"wheel":{"from":"steps.build.outputs.wheel","type":{"kind":"file","media_type":"application/zip","simulated":true}}}}`
)

type bindingFixture struct {
	admin        *sql.DB
	runtime      *sql.DB
	store        *pipelinebinding.Store
	appID        int64
	configID     int64
	otherAppID   int64
	otherConfig  int64
	ciVersion    int64
	ciDisabledID int64
	pythonCIID   int64
	cdVersion    int64
	cdDisabledID int64
}

func insertTypedVersion(t *testing.T, database *sql.DB, key, kind, applicationType, targetType, spec string, enabled bool) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO pipeline_templates
		(template_key,kind,application_type,target_type,enabled,revision,draft,created_by_user_id)
		VALUES (?,?,NULLIF(?,''),NULLIF(?,''),?,2,?,1)`, key, kind, applicationType, targetType, enabled, spec)
	if err != nil {
		t.Fatal(err)
	}
	templateID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.Canonicalize([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	versionResult, err := database.Exec(`INSERT INTO pipeline_template_versions
		(template_id,version_number,source_revision,spec,checksum,created_by_user_id) VALUES (?,1,1,?,?,1)`,
		templateID, spec, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	versionID, err := versionResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return versionID
}

func insertApp(t *testing.T, database *sql.DB, name string) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO apps (app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
		VALUES (?, ?, 'integration-test', '集成测试', 'java', ?)`, name, name, "https://example.invalid/"+name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertConfig(t *testing.T, database *sql.DB, appID int64, env string) int64 {
	t.Helper()
	result, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type) VALUES (?, ?, 'java')`, appID, env)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func newBindingFixture(t *testing.T) *bindingFixture {
	t.Helper()
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
	t.Cleanup(func() { admin.Close() })
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "binding_" + hex.EncodeToString(suffix)
	user := name
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(admin, "CREATE DATABASE `"+name+"`")
	t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	cfg.ParseTime = true
	database, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	status, err := schemadb.MigrateUp(ctx, cfg.FormatDSN(), "", 45*time.Second, 10*time.Second)
	if err != nil || !status.Compatible() {
		t.Fatalf("migration: %v", err)
	}
	exec(admin, "CREATE USER '"+user+"'@'%' IDENTIFIED BY 'isolated-test-only'")
	t.Cleanup(func() { admin.Exec("DROP USER '" + user + "'@'%'") })
	for _, grant := range []string{
		"GRANT SELECT,INSERT,UPDATE ON `" + name + "`.apps TO '" + user + "'@'%'",
		"GRANT SELECT,INSERT,UPDATE ON `" + name + "`.app_configs TO '" + user + "'@'%'",
		"GRANT SELECT,INSERT,UPDATE ON `" + name + "`.env_configs TO '" + user + "'@'%'",
		"GRANT SELECT,INSERT,UPDATE ON `" + name + "`.application_types TO '" + user + "'@'%'",
		"GRANT SELECT,INSERT,UPDATE ON `" + name + "`.pipeline_templates TO '" + user + "'@'%'",
		"GRANT SELECT,INSERT ON `" + name + "`.pipeline_template_versions TO '" + user + "'@'%'",
		"GRANT SELECT ON `" + name + "`.* TO '" + user + "'@'%'",
		"GRANT INSERT,UPDATE ON `" + name + "`.application_ci_bindings TO '" + user + "'@'%'",
		"GRANT INSERT,UPDATE ON `" + name + "`.app_config_cd_bindings TO '" + user + "'@'%'",
	} {
		exec(admin, grant)
	}
	cfg.User = user
	cfg.Passwd = "isolated-test-only"
	runtime, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	fixture := &bindingFixture{admin: database, runtime: runtime, store: pipelinebinding.NewStore(runtime)}
	exec(database, `INSERT INTO env_configs (env, description_cn, enabled)
		SELECT 'dev','绑定测试环境',1 WHERE NOT EXISTS (SELECT 1 FROM env_configs WHERE env='dev')`)
	exec(database, `INSERT INTO env_configs (env, description_cn, enabled)
		SELECT 'prod-off','停用环境',0 WHERE NOT EXISTS (SELECT 1 FROM env_configs WHERE env='prod-off')`)
	fixture.appID = insertApp(t, database, "binding-app")
	fixture.otherAppID = insertApp(t, database, "binding-other-app")
	fixture.configID = insertConfig(t, database, fixture.appID, "dev")
	fixture.otherConfig = insertConfig(t, database, fixture.otherAppID, "dev")
	fixture.ciVersion = insertTypedVersion(t, database, "binding-ci", "ci", "java", "", bindingTestCISpec, true)
	fixture.ciDisabledID = insertTypedVersion(t, database, "binding-ci-off", "ci", "java", "", bindingTestCISpec, false)
	fixture.pythonCIID = insertTypedVersion(t, database, "binding-python-ci", "ci", "python", "", bindingTestPythonSpec, true)
	fixture.cdVersion = insertTypedVersion(t, database, "binding-cd", "cd", "", "kubernetes", bindingTestCDSpec, true)
	fixture.cdDisabledID = insertTypedVersion(t, database, "binding-cd-off", "cd", "", "kubernetes", bindingTestCDSpec, false)
	return fixture
}

func (f *bindingFixture) ciIntent(versionID int64, parameters map[string]json.RawMessage, expected uint64) pipelinebinding.SaveIntent {
	return pipelinebinding.SaveIntent{
		Kind: "ci", TargetID: f.appID, VersionID: versionID, Ownership: "java",
		Parameters: parameters, Enabled: true, Expected: expected, Actor: 1,
	}
}

func rawJSON(value string) json.RawMessage { return json.RawMessage(value) }

func TestMySQLPipelineBindingStore(t *testing.T) {
	f := newBindingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Create requires every required parameter to be satisfiable from the
	// binding layer, because B1 froze "binding intent" as defaults plus binding.
	if _, err := f.store.Save(ctx, f.ciIntent(f.ciVersion, nil, 0)); !errors.Is(err, pipelinebinding.ErrParameters) {
		t.Fatalf("missing required parameter accepted: %v", err)
	}
	if _, err := f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"unknown": rawJSON(`"x"`)}, 0)); !errors.Is(err, pipelinebinding.ErrParameters) {
		t.Fatalf("unknown parameter accepted: %v", err)
	}
	if _, err := f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`1`)}, 0)); !errors.Is(err, pipelinebinding.ErrParameters) {
		t.Fatalf("wrong parameter type accepted: %v", err)
	}
	var count int
	require(f.admin.QueryRow("SELECT COUNT(*) FROM application_ci_bindings").Scan(&count))
	if count != 0 {
		t.Fatal("rejected parameters wrote a binding row")
	}

	created, err := f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"https://example.invalid/repo.git"`)}, 0))
	require(err)
	if created.Revision != 1 || created.CreatedBy != 1 || created.UpdatedBy != 1 || !created.Enabled || created.VersionNumber != 1 || len(created.Checksum) != 64 {
		t.Fatalf("unexpected created binding: %+v", created)
	}
	if created.ID <= 0 || created.TemplateID <= 0 || created.Kind != "ci" {
		t.Fatalf("created binding lacks identity: %+v", created)
	}

	// A second create for the same application must conflict, not fork a second
	// active binding.
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"x"`)}, 0)); !errors.Is(err, pipelinebinding.ErrConflict) {
		t.Fatalf("duplicate create accepted: %v", err)
	}
	// A stale or wrong revision must conflict in both directions.
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"x"`)}, 7)); !errors.Is(err, pipelinebinding.ErrConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}

	// Exactly one of many writers sharing an expected revision may win.
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
			} else if !errors.Is(e, pipelinebinding.ErrConflict) {
				t.Fatal(e)
			}
		}
		if wins != 1 {
			t.Fatalf("wins=%d", wins)
		}
	}
	race(func() error {
		_, e := f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 1))
		return e
	})
	// Creating concurrently where no row exists yet is resolved by the unique
	// target key rather than by a racy existence check.
	otherCI := f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"b"`)}, 0)
	otherCI.TargetID = f.otherAppID
	race(func() error { _, e := f.store.Save(ctx, otherCI); return e })

	current, err := f.store.Get(ctx, "ci", f.appID)
	require(err)
	if current.Revision != 2 || current.VersionID != f.ciVersion {
		t.Fatalf("unexpected binding after CAS: %+v", current)
	}
	stored, err := f.store.ReadParameters(ctx, "ci", f.appID)
	require(err)
	if string(stored.Document["repo"]) != `"a"` || len(stored.Document) != 1 {
		t.Fatalf("stored binding layer wrong: %v", stored.Document)
	}
	if stored.BindingID != current.ID || stored.Revision != current.Revision {
		t.Fatalf("parameter read disagrees with metadata: %+v vs %+v", stored, current)
	}
	if _, err = f.store.ReadParameters(ctx, "ci", f.otherAppID+1000); !errors.Is(err, pipelinebinding.ErrTarget) {
		t.Fatalf("missing binding accepted: %v", err)
	}

	// Rebind across kinds or owners is rejected, including when the caller
	// supplies a real published version of the wrong kind.
	if _, err = f.store.Save(ctx, f.ciIntent(f.cdVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 2)); !errors.Is(err, pipelinebinding.ErrMismatch) {
		t.Fatalf("CD version bound as CI accepted: %v", err)
	}
	if _, err = f.store.Save(ctx, f.ciIntent(f.pythonCIID, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 2)); !errors.Is(err, pipelinebinding.ErrMismatch) {
		t.Fatalf("foreign application type accepted: %v", err)
	}
	wrongOwner := f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 2)
	wrongOwner.Ownership = "python"
	if _, err = f.store.Save(ctx, wrongOwner); !errors.Is(err, pipelinebinding.ErrMismatch) {
		t.Fatalf("declared ownership mismatch accepted: %v", err)
	}
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion+999, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 2)); !errors.Is(err, pipelinebinding.ErrTarget) {
		t.Fatalf("unknown version accepted: %v", err)
	}
	missingApp := f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 0)
	missingApp.TargetID = f.appID + 100000
	if _, err = f.store.Save(ctx, missingApp); !errors.Is(err, pipelinebinding.ErrTarget) {
		t.Fatalf("unknown application accepted: %v", err)
	}

	// Disable and enable are the same CAS update.
	disabled := f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 2)
	disabled.Enabled = false
	updated, err := f.store.Save(ctx, disabled)
	require(err)
	if updated.Enabled || updated.Revision != 3 {
		t.Fatalf("disable did not commit: %+v", updated)
	}
	rebound, err := f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 3))
	require(err)
	if !rebound.Enabled || rebound.Revision != 4 || rebound.ID != updated.ID {
		t.Fatalf("enable did not reuse the binding row: %+v", rebound)
	}

	// A disabled template blocks new bindings but leaves existing rows readable.
	if _, err = f.admin.Exec("UPDATE pipeline_templates SET enabled=0 WHERE template_id=(SELECT template_id FROM pipeline_template_versions WHERE version_id=?)", f.ciVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 4)); !errors.Is(err, pipelinebinding.ErrDisabled) {
		t.Fatalf("disabled template accepted: %v", err)
	}
	if _, err = f.store.Get(ctx, "ci", f.appID); err != nil {
		t.Fatalf("disabled template hid the binding: %v", err)
	}
	if _, err = f.admin.Exec("UPDATE pipeline_templates SET enabled=1 WHERE template_id=(SELECT template_id FROM pipeline_template_versions WHERE version_id=?)", f.ciVersion); err != nil {
		t.Fatal(err)
	}
	// A newly created binding against a disabled version is rejected outright.
	offIntent := f.ciIntent(f.ciDisabledID, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 0)
	offIntent.TargetID = f.otherAppID + 1
	if _, err = f.store.Save(ctx, offIntent); !errors.Is(err, pipelinebinding.ErrDisabled) {
		t.Fatalf("disabled version accepted: %v", err)
	}

	// Deterministic lock order: a binding write cannot pass an uncommitted type
	// disable, and must observe the committed disable after waiting.
	tx, err := f.admin.BeginTx(ctx, nil)
	require(err)
	if _, err = tx.ExecContext(ctx, "UPDATE application_types SET enabled=0,revision=revision+1 WHERE type_key='java'"); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = f.store.Save(waitCtx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 4))
	waitCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		tx.Rollback()
		t.Fatalf("binding write bypassed the type lock: %v", err)
	}
	require(tx.Commit())
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 4)); !errors.Is(err, pipelinebinding.ErrDisabled) {
		t.Fatalf("binding write ignored the committed disable: %v", err)
	}
	if _, err = f.admin.Exec("UPDATE application_types SET enabled=1,revision=revision+1 WHERE type_key='java'"); err != nil {
		t.Fatal(err)
	}

	// A failure after the write must roll the whole CAS back.
	if _, err = f.admin.Exec("CREATE TRIGGER binding_fail_update BEFORE UPDATE ON application_ci_bindings FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='private failure'"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 4))
	if !errors.Is(err, pipelinebinding.ErrStorage) {
		t.Fatalf("rollback failure: %v", err)
	}
	if strings.Contains(err.Error(), "private failure") {
		t.Fatal("storage error leaked database diagnostics")
	}
	if _, err = f.admin.Exec("DROP TRIGGER binding_fail_update"); err != nil {
		t.Fatal(err)
	}
	current, err = f.store.Get(ctx, "ci", f.appID)
	require(err)
	if current.Revision != 4 {
		t.Fatal("failed update consumed the revision")
	}
	// The failing transaction must not have leaked a binding for the other app.
	if _, err = f.store.Save(ctx, f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 4)); err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}

	// CD bindings are environment scoped and independent from CI.
	if _, err = f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: f.configID, VersionID: f.ciVersion, Ownership: "kubernetes", Enabled: true, Actor: 1}); !errors.Is(err, pipelinebinding.ErrMismatch) {
		t.Fatalf("CI version bound as CD accepted: %v", err)
	}
	cdCreated, err := f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: f.configID, VersionID: f.cdVersion, Ownership: "kubernetes", Enabled: true, Actor: 1})
	require(err)
	if cdCreated.Kind != "cd" || cdCreated.TargetID != f.configID || cdCreated.Revision != 1 {
		t.Fatalf("unexpected CD binding: %+v", cdCreated)
	}
	if _, err = f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: f.configID, VersionID: f.cdDisabledID, Ownership: "kubernetes", Enabled: true, Expected: 1, Actor: 1}); !errors.Is(err, pipelinebinding.ErrDisabled) {
		t.Fatalf("disabled CD version accepted: %v", err)
	}
	// One environment can hold a CD binding while another environment of the
	// same application holds none: bindings never depend on sibling contexts.
	offEnvConfig := insertConfig(t, f.admin, f.appID, "prod-off")
	if _, err = f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: offEnvConfig, VersionID: f.cdVersion, Ownership: "kubernetes", Enabled: true, Actor: 1}); !errors.Is(err, pipelinebinding.ErrDisabled) {
		t.Fatalf("disabled environment accepted a CD binding: %v", err)
	}
	// CI does not require any environment at all.
	if _, err = f.store.Get(ctx, "ci", f.otherAppID); err != nil {
		t.Fatalf("CI binding read depends on environments: %v", err)
	}

	// A soft-deleted target is refused on write but never silently deleted on
	// read, so an operator can still audit which version was bound.
	softAppID := insertApp(t, f.admin, "binding-soft-deleted")
	softConfigID := insertConfig(t, f.admin, softAppID, "dev")
	if _, err = f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: softConfigID, VersionID: f.cdVersion, Ownership: "kubernetes", Enabled: true, Actor: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.admin.Exec("UPDATE apps SET deleted_at=UTC_TIMESTAMP() WHERE app_id=?", softAppID); err != nil {
		t.Fatal(err)
	}
	softIntent := f.ciIntent(f.ciVersion, map[string]json.RawMessage{"repo": rawJSON(`"a"`)}, 0)
	softIntent.TargetID = softAppID
	if _, err = f.store.Save(ctx, softIntent); !errors.Is(err, pipelinebinding.ErrTarget) {
		t.Fatalf("soft-deleted application accepted a binding: %v", err)
	}
	if _, err = f.store.Save(ctx, pipelinebinding.SaveIntent{Kind: "cd", TargetID: softConfigID, VersionID: f.cdVersion, Ownership: "kubernetes", Enabled: false, Expected: 1, Actor: 1}); !errors.Is(err, pipelinebinding.ErrTarget) {
		t.Fatalf("soft-deleted configuration accepted a rebind: %v", err)
	}
	stale, err := f.store.Get(ctx, "cd", softConfigID)
	require(err)
	if stale.Revision != 1 || stale.VersionID != f.cdVersion {
		t.Fatalf("soft delete rewrote the stored binding: %+v", stale)
	}

	// Least privilege: bindings are replaceable but never deletable through the
	// runtime principal, and immutable versions stay immutable.
	for _, q := range []string{
		"DELETE FROM application_ci_bindings",
		"DELETE FROM app_config_cd_bindings",
		"UPDATE pipeline_template_versions SET checksum=REPEAT('a',64)",
		"DELETE FROM pipeline_template_versions",
	} {
		if _, err = f.runtime.Exec(q); err == nil {
			t.Fatalf("runtime privilege violation: %s", q)
		}
	}
	if err = schemadb.CheckRuntimeCompatibility(ctx, f.admin); err != nil {
		t.Fatalf("stored binding data violates the schema contract: %v", err)
	}
}
