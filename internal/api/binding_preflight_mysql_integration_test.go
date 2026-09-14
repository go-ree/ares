package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/canonicaljson"
	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinebinding"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-ree/ares/internal/templatecatalog"
	"github.com/go-sql-driver/mysql"
)

func bindingTestChecksum(t *testing.T, raw []byte) string {
	t.Helper()
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Reuses the epoch 9 catalog fixture and grants this preflight principal SELECT
// only. A write accidentally added to the implementation must fail this test.
func checkBindingPreflightMySQL(t *testing.T, database *sql.DB, cfg *mysql.Config, ciVersion string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) sql.Result {
		t.Helper()
		r, err := database.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	exec("UPDATE application_types SET enabled=1,revision=revision+1 WHERE type_key='custom'")
	result := exec("INSERT INTO apps(app_name,app_name_cn,owner,owner_cn,dev_language,git_url) VALUES('binding-ci','绑定测试','owner','维护者','unmapped-old-language','')")
	appID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	readCfg := cfg.Clone()
	readCfg.User = cfg.User + "r"
	readCfg.Passwd = "isolated-read-only"
	exec("CREATE USER '" + readCfg.User + "'@'%' IDENTIFIED BY 'isolated-read-only'")
	defer database.Exec("DROP USER '" + readCfg.User + "'@'%'")
	for _, table := range []string{"apps", "app_configs", "env_configs", "application_types", "pipeline_templates", "pipeline_template_versions"} {
		exec("GRANT SELECT ON `" + cfg.DBName + "`." + table + " TO '" + readCfg.User + "'@'%'")
	}
	reader, err := sql.Open("mysql", readCfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	service, audit, sessions := newAuthBoundary(t)
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingPreflight: pipelinebinding.New(reader)})
	call := func(path, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleDeveloper], "POST", path, body))
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "PRIVATE-REPOSITORY") || strings.Contains(w.Body.String(), "PRIVATE-PARAM") {
			t.Fatal("value leaked")
		}
		if want == 200 && (!strings.Contains(w.Body.String(), `"persisted":false`) || !strings.Contains(w.Body.String(), `"executable":false`)) {
			t.Fatal("incorrect promise")
		}
	}
	ciPath := "/api/v1/apps/" + strconv.FormatInt(appID, 10) + "/ci-binding/preflight"
	ciBody := `{"version_id":"` + ciVersion + `","application_type":"custom"}`
	// There are no environment configurations yet. CI must not require one,
	// or infer its new type from the legacy dev_language string.
	call(ciPath, ciBody, 200)
	call(ciPath, strings.Replace(ciBody, "custom", "java", 1), 422)
	call(ciPath, `{"version_id":"`+ciVersion+`","application_type":"custom","parameters":{"unknown":"PRIVATE-PARAM"}}`, 422)
	exec("UPDATE application_types SET enabled=0,revision=revision+1 WHERE type_key='custom'")
	call(ciPath, ciBody, 409)
	exec("UPDATE application_types SET enabled=1,revision=revision+1 WHERE type_key='custom'")
	exec("UPDATE apps SET deleted_at=NOW() WHERE app_id=?", appID)
	call(ciPath, ciBody, 404)
	exec("UPDATE apps SET deleted_at=NULL WHERE app_id=?", appID)
	exec("UPDATE pipeline_template_versions SET checksum=REPEAT('0',64) WHERE version_id=?", ciVersion)
	call(ciPath, ciBody, 503)
	// Restore the original immutable checksum before checking schema integrity.
	var savedRaw []byte
	if err = database.QueryRow("SELECT spec FROM pipeline_template_versions WHERE version_id=?", ciVersion).Scan(&savedRaw); err != nil {
		t.Fatal(err)
	}
	// The fixture provides the original digest from the unchanged version.
	var publishedChecksum string
	// Canonical hashing must not depend on MySQL's JSON whitespace.
	publishedChecksum = bindingTestChecksum(t, savedRaw)
	exec("UPDATE pipeline_template_versions SET checksum=? WHERE version_id=?", publishedChecksum, ciVersion)
	var cd pipelinetemplate.Spec
	if err = json.Unmarshal([]byte(`{"schema_version":1,"kind":"cd","name":"部署","target_type":"kubernetes","inputs":{"jar":{"kind":"file","media_type":"application/java-archive","simulated":true}},"steps":[{"key":"deploy","uses":"mock.deploy@v1","inputs":{"jar":{"from":"inputs.jar","type":{"kind":"file","media_type":"application/java-archive","simulated":true}}}}]}`), &cd); err != nil {
		t.Fatal(err)
	}
	catalog := templatecatalog.New(database)
	templateID, err := catalog.CreateTemplate(ctx, "preflight-cd", cd, 1)
	if err != nil {
		t.Fatal(err)
	}
	version, err := catalog.Publish(ctx, templateID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO env_configs(env,description_cn,enabled) VALUES('custom-env','自定义环境',1)")
	result = exec("INSERT INTO app_configs(app_id,env,code_package_type) VALUES(?,'custom-env','docker')", appID)
	configID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	cdPath := "/api/v1/app-configs/" + strconv.FormatInt(configID, 10) + "/cd-binding/preflight"
	cdBody := `{"version_id":"` + strconv.FormatInt(version.ID, 10) + `","target_type":"kubernetes"}`
	call(cdPath, cdBody, 200)
	call(cdPath, strings.Replace(cdBody, "kubernetes", "ssh", 1), 422)
	call(cdPath, `{"version_id":"`+ciVersion+`","target_type":"kubernetes"}`, 422)
	exec("UPDATE env_configs SET enabled=0 WHERE env='custom-env'")
	call(cdPath, cdBody, 409)
	exec("UPDATE env_configs SET enabled=1 WHERE env='custom-env'")
	exec("UPDATE app_configs SET deleted_at=NOW() WHERE config_id=?", configID)
	call(cdPath, cdBody, 404)
	exec("UPDATE app_configs SET deleted_at=NULL WHERE config_id=?", configID)
	exec("UPDATE pipeline_templates SET enabled=0,revision=revision+1 WHERE template_id=?", templateID)
	call(cdPath, cdBody, 409)
	if err = schemadb.CheckRuntimeCompatibility(ctx, database); err != nil {
		t.Fatal(err)
	}
	var tasks, bindings int
	if err = database.QueryRow("SELECT (SELECT COUNT(*) FROM task_record),(SELECT COUNT(*) FROM app_config_workflows)").Scan(&tasks, &bindings); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || bindings != 0 {
		t.Fatal("preflight created execution or legacy binding")
	}
	raw, _ := json.Marshal(audit.audits)
	if strings.Contains(string(raw), "PRIVATE-PARAM") {
		t.Fatal("audit leaked parameters")
	}
}
