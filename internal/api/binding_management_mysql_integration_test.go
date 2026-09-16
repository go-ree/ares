package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinebinding"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-ree/ares/internal/templatecatalog"
	"github.com/go-sql-driver/mysql"
)

// TestMySQLBindingManagementAPI drives the management endpoints through the
// real router with the composed least-privilege runtime grants. A privilege the
// new endpoints need but the grant script omits fails here.
func TestMySQLBindingManagementAPI(t *testing.T) {
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
	name := "bindingapi_" + hex.EncodeToString(suffix)
	// adminExec covers statements that must run without a selected database;
	// exec runs data statements against the isolated schema.
	adminExec := func(q string, args ...any) sql.Result {
		t.Helper()
		result, err := admin.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	adminExec("CREATE DATABASE `" + name + "`")
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	cfg.ParseTime = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := schemadb.MigrateUp(ctx, cfg.FormatDSN(), "", 45*time.Second, 10*time.Second)
	if err != nil || !status.Compatible() {
		t.Fatalf("migration: %v", err)
	}
	database, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	exec := func(q string, args ...any) sql.Result {
		t.Helper()
		result, err := database.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// Exactly the table privileges deploy/compose/mysql/01-create-users.sh
	// grants the runtime principal for these tables.
	runtimeCfg := cfg.Clone()
	runtimeCfg.User = name + "rt"
	runtimeCfg.Passwd = "isolated-runtime-only"
	adminExec("CREATE USER '" + runtimeCfg.User + "'@'%' IDENTIFIED BY 'isolated-runtime-only'")
	defer admin.Exec("DROP USER '" + runtimeCfg.User + "'@'%'")
	for _, grant := range []string{
		"GRANT SELECT ON `" + name + "`.* TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.apps TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.app_configs TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.env_configs TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.application_types TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.pipeline_templates TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT ON `" + name + "`.pipeline_template_versions TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.application_ci_bindings TO '" + runtimeCfg.User + "'@'%'",
		"GRANT INSERT, UPDATE ON `" + name + "`.app_config_cd_bindings TO '" + runtimeCfg.User + "'@'%'",
	} {
		adminExec(grant)
	}
	runtime, err := sql.Open("mysql", runtimeCfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	// Seed through the real catalog service so versions carry a valid checksum.
	catalog := templatecatalog.New(database)
	fileType := pipelinetemplate.ArtifactType{Kind: "file", MediaType: "application/java-archive", Simulated: true}
	ciSpec := pipelinetemplate.Spec{
		SchemaVersion: 1, Kind: "ci", Name: "Java 构建", ApplicationType: "java",
		Parameters: map[string]pipelinetemplate.Parameter{
			"repo":    {Type: "string", Required: true},
			"profile": {Type: "string", AllowOverride: true, Default: json.RawMessage(`"release"`)},
		},
		Steps:   []pipelinetemplate.Step{{Key: "build", Uses: "mock.build@v1", Outputs: map[string]pipelinetemplate.ArtifactType{"jar": fileType}}},
		Outputs: map[string]pipelinetemplate.Input{"jar": {From: "steps.build.outputs.jar", Type: fileType}},
	}
	ciTemplateID, err := catalog.CreateTemplate(ctx, "api-ci", ciSpec, 1)
	if err != nil {
		t.Fatal(err)
	}
	ciVersion, err := catalog.Publish(ctx, ciTemplateID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	cdSpec := pipelinetemplate.Spec{
		SchemaVersion: 1, Kind: "cd", Name: "部署", TargetType: "kubernetes",
		Inputs: map[string]pipelinetemplate.ArtifactType{"jar": fileType},
		Steps:  []pipelinetemplate.Step{{Key: "deploy", Uses: "mock.deploy@v1", Inputs: map[string]pipelinetemplate.Input{"jar": {From: "inputs.jar", Type: fileType}}}},
	}
	cdTemplateID, err := catalog.CreateTemplate(ctx, "api-cd", cdSpec, 1)
	if err != nil {
		t.Fatal(err)
	}
	cdVersion, err := catalog.Publish(ctx, cdTemplateID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	exec("INSERT INTO env_configs (env, description_cn, enabled) VALUES ('api-env','接口环境',1)")
	appResult := exec(`INSERT INTO apps (app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
		VALUES ('api-binding-app','接口绑定应用','owner','维护者','legacy-unmapped','')`)
	appID, err := appResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	configResult := exec("INSERT INTO app_configs (app_id, env, code_package_type) VALUES (?,'api-env','java')", appID)
	configID, err := configResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	service, audit, sessions := newAuthBoundary(t)
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: pipelinebinding.NewStore(runtime)})
	call := func(role auth.Role, method, path, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		request := authenticatedRequest(t, service, sessions[role], method, path, body)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		r.ServeHTTP(w, request)
		if w.Code != want {
			t.Fatalf("%s %s %s => %d %s", role, method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	ciPath := "/api/v1/apps/" + strconv.FormatInt(appID, 10) + "/ci-binding"
	cdPath := "/api/v1/app-configs/" + strconv.FormatInt(configID, 10) + "/cd-binding"
	ciBody := func(version int64, extra string) string {
		return `{"version_id":"` + strconv.FormatInt(version, 10) + `","application_type":"java","parameters":{"repo":"https://example.invalid/PRIVATE-REPOSITORY.git"}` + extra + `}`
	}

	// Every role may read metadata; only editors may change or read parameters.
	call(auth.RoleViewer, "GET", ciPath, "", 404)
	call(auth.RoleReleaser, "GET", ciPath, "", 404)
	call(auth.RoleViewer, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true`), 403)
	call(auth.RoleViewer, "GET", ciPath+"/parameters", "", 403)
	call(auth.RoleReleaser, "GET", cdPath+"/parameters", "", 403)

	created := call(auth.RoleDeveloper, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true`), 201)
	if !strings.Contains(created, `"persisted":true`) || !strings.Contains(created, `"executable":false`) {
		t.Fatalf("create response: %s", created)
	}
	if strings.Contains(created, "PRIVATE-REPOSITORY") {
		t.Fatal("binding metadata leaked parameters")
	}
	// Create is a CAS operation: the same request must not fork a second binding.
	call(auth.RoleDeveloper, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true`), 409)
	// A stale revision cannot overwrite the committed binding.
	call(auth.RoleDeveloper, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":false,"expected_revision":"7"`), 409)
	// Rebinding to another application type is refused.
	call(auth.RoleDeveloper, "PUT", ciPath, strings.Replace(ciBody(ciVersion.ID, `,"enabled":true,"expected_revision":"1"`), `"java"`, `"python"`, 1), 422)
	// The required parameter must still be satisfiable on every write.
	call(auth.RoleDeveloper, "PUT", ciPath, `{"version_id":"`+strconv.FormatInt(ciVersion.ID, 10)+`","application_type":"java","parameters":{},"enabled":true,"expected_revision":"1"}`, 422)

	meta := call(auth.RoleViewer, "GET", ciPath, "", 200)
	if !strings.Contains(meta, `"revision":"1"`) || !strings.Contains(meta, `"checksum"`) {
		t.Fatalf("metadata read: %s", meta)
	}
	if strings.Contains(meta, "PRIVATE-REPOSITORY") {
		t.Fatal("viewer metadata leaked parameters")
	}
	params := call(auth.RoleDeveloper, "GET", ciPath+"/parameters", "", 200)
	if !strings.Contains(params, "PRIVATE-REPOSITORY") || !strings.Contains(params, `"repo"`) {
		t.Fatalf("parameter read: %s", params)
	}
	// The stored document is the binding layer only: the template default for
	// profile must not be merged into the response.
	if strings.Contains(params, "release") {
		t.Fatalf("parameter read merged template defaults: %s", params)
	}

	// Disable and enable are CAS updates that keep one binding row.
	disabled := call(auth.RoleAdmin, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":false,"expected_revision":"1"`), 200)
	if !strings.Contains(disabled, `"enabled":false`) || !strings.Contains(disabled, `"revision":"2"`) {
		t.Fatalf("disable response: %s", disabled)
	}
	enabled := call(auth.RoleAdmin, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true,"expected_revision":"2"`), 200)
	if !strings.Contains(enabled, `"revision":"3"`) {
		t.Fatalf("enable response: %s", enabled)
	}

	// A disabled type blocks new bindings and reads stay available.
	exec("UPDATE application_types SET enabled=0,revision=revision+1 WHERE type_key='java'")
	call(auth.RoleDeveloper, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true,"expected_revision":"3"`), 409)
	call(auth.RoleViewer, "GET", ciPath, "", 200)
	exec("UPDATE application_types SET enabled=1,revision=revision+1 WHERE type_key='java'")
	// A version that cannot satisfy the binding layer is rejected as a mismatch.
	call(auth.RoleDeveloper, "PUT", ciPath, ciBody(cdVersion.ID, `,"enabled":true,"expected_revision":"3"`), 422)
	// A soft-deleted target is refused without deleting the stored binding.
	exec("UPDATE apps SET deleted_at=UTC_TIMESTAMP() WHERE app_id=?", appID)
	call(auth.RoleDeveloper, "PUT", ciPath, ciBody(ciVersion.ID, `,"enabled":true,"expected_revision":"3"`), 404)
	exec("UPDATE apps SET deleted_at=NULL WHERE app_id=?", appID)

	// CD bindings are independent per application configuration.
	cdCreated := call(auth.RoleDeveloper, "PUT", cdPath, `{"version_id":"`+strconv.FormatInt(cdVersion.ID, 10)+`","target_type":"kubernetes","parameters":{},"enabled":true}`, 201)
	if !strings.Contains(cdCreated, `"kind":"cd"`) || !strings.Contains(cdCreated, `"persisted":true`) {
		t.Fatalf("cd create response: %s", cdCreated)
	}
	call(auth.RoleDeveloper, "PUT", cdPath, `{"version_id":"`+strconv.FormatInt(ciVersion.ID, 10)+`","target_type":"kubernetes","enabled":true,"expected_revision":"1"}`, 422)
	call(auth.RoleViewer, "GET", cdPath, "", 200)
	call(auth.RoleDeveloper, "GET", cdPath+"/parameters", "", 200)
	// A missing binding for an existing configuration is a normal not-found.
	exec("INSERT INTO env_configs (env, description_cn, enabled) VALUES ('api-env-2','接口环境二',1)")
	emptyConfig := exec("INSERT INTO app_configs (app_id, env, code_package_type) VALUES (?,'api-env-2','java')", appID)
	emptyConfigID, err := emptyConfig.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	call(auth.RoleDeveloper, "GET", "/api/v1/app-configs/"+strconv.FormatInt(emptyConfigID, 10)+"/cd-binding", "", 404)

	// No request may have started a run, created a legacy binding or registered
	// an artifact: B2 stores intent only.
	var tasks, legacyBindings, legacyWorkflows int
	if err = database.QueryRow(`SELECT (SELECT COUNT(*) FROM task_record),(SELECT COUNT(*) FROM app_config_workflows),(SELECT COUNT(*) FROM release_workflows)`).
		Scan(&tasks, &legacyBindings, &legacyWorkflows); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || legacyBindings != 0 || legacyWorkflows != 0 {
		t.Fatal("binding management created execution or legacy state")
	}
	if err = schemadb.CheckRuntimeCompatibility(ctx, database); err != nil {
		t.Fatalf("stored bindings violate the schema contract: %v", err)
	}
	raw, _ := json.Marshal(audit.audits)
	if strings.Contains(string(raw), "PRIVATE-REPOSITORY") {
		t.Fatal("audit leaked binding parameters")
	}
	// The stored parameter layer is exactly what the caller declared.
	var stored []byte
	if err = database.QueryRow("SELECT parameters FROM application_ci_bindings WHERE app_id=?", appID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(stored, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 1 || !strings.Contains(string(document["repo"]), "PRIVATE-REPOSITORY") {
		t.Fatalf("stored binding layer wrong: %s", stored)
	}
}
