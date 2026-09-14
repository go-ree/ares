package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/templatecatalog"
	"github.com/go-sql-driver/mysql"
)

func TestMySQLTemplateCatalogAPI(t *testing.T) {
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
	name := "catalogapi_" + hex.EncodeToString(suffix)
	exec := func(db *sql.DB, q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(admin, "CREATE DATABASE `"+name+"`")
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	cfg.ParseTime = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	exec(admin, "CREATE USER '"+name+"'@'%' IDENTIFIED BY 'isolated-api-test'")
	defer admin.Exec("DROP USER '" + name + "'@'%'")
	for _, table := range []string{"application_types", "pipeline_templates"} {
		exec(admin, "GRANT SELECT,INSERT,UPDATE ON `"+name+"`."+table+" TO '"+name+"'@'%'")
	}
	exec(admin, "GRANT SELECT,INSERT ON `"+name+"`.pipeline_template_versions TO '"+name+"'@'%'")
	cfg.User = name
	cfg.Passwd = "isolated-api-test"
	runtimeDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeDB.Close()
	service, audit, sessions := newAuthBoundary(t)
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: templatecatalog.New(runtimeDB)})
	call := func(role auth.Role, method, path, body string, want int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[role], method, path, body))
		if w.Code != want {
			t.Fatalf("%s %s = %d %s", method, path, w.Code, w.Body.String())
		}
		var response struct {
			Result map[string]any `json:"result"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Result
	}
	call(auth.RoleAdmin, "POST", "/api/v1/application-types", `{"key":"custom","name":"自定义类型"}`, 201)
	call(auth.RoleAdmin, "POST", "/api/v1/application-types", `{"key":"custom","name":"重复"}`, 409)
	spec := `{"schema_version":1,"kind":"ci","name":"首个模板","application_type":"custom","steps":[{"key":"build","uses":"mock.build@v1","with":{"repository":"PRIVATE-REPOSITORY"},"outputs":{"jar":{"kind":"file","media_type":"application/java-archive","simulated":true}}}],"outputs":{"jar":{"from":"steps.build.outputs.jar","type":{"kind":"file","media_type":"application/java-archive","simulated":true}}}}`
	result := call(auth.RoleAdmin, "POST", "/api/v1/pipeline-templates", `{"key":"custom-ci","spec":`+spec+`}`, 201)
	id := result["id"].(string)
	path := "/api/v1/pipeline-templates/" + id
	call(auth.RoleAdmin, "PUT", path, `{"enabled":true,"expected_revision":"1","spec":`+spec+`}`, 200)
	v := call(auth.RoleAdmin, "POST", path+"/versions", `{"expected_revision":"2"}`, 201)
	if v["number"] != "1" || v["source_revision"] != "2" || v["executable"] != false {
		t.Fatal(v)
	}
	call(auth.RoleAdmin, "POST", path+"/versions", `{"expected_revision":"2"}`, 409)
	call(auth.RoleAdmin, "PUT", path, `{"enabled":true,"expected_revision":"1","spec":`+spec+`}`, 409)
	spec2 := strings.Replace(spec, "首个模板", "修改后的草稿", 1)
	call(auth.RoleAdmin, "PUT", path, `{"enabled":true,"expected_revision":"3","spec":`+spec2+`}`, 200)
	old := call(auth.RoleAdmin, "GET", path+"/versions/1", "", 200)
	raw, _ := json.Marshal(old)
	if !strings.Contains(string(raw), "首个模板") || strings.Contains(string(raw), "修改后的草稿") {
		t.Fatal("immutable spec changed")
	}
	for _, p := range []string{"/api/v1/pipeline-templates", path + "/versions"} {
		v := call(auth.RoleViewer, "GET", p, "", 200)
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "PRIVATE-REPOSITORY") {
			t.Fatal("private metadata leak")
		}
	}
	call(auth.RoleViewer, "GET", path, "", 403)
	call(auth.RoleAdmin, "PUT", "/api/v1/application-types/custom", `{"name":"停用","enabled":false,"expected_revision":"1"}`, 200)
	call(auth.RoleAdmin, "POST", path+"/versions", `{"expected_revision":"4"}`, 409)
	page := call(auth.RoleViewer, "GET", "/api/v1/application-types?limit=1", "", 200)
	if page["has_more"] != true || page["next_cursor"] != "custom" {
		t.Fatal(page)
	}
	page = call(auth.RoleViewer, "GET", "/api/v1/application-types?limit=1&after=custom", "", 200)
	if page["next_cursor"] != "java" {
		t.Fatal(page)
	}
	if err = schemadb.CheckRuntimeCompatibility(ctx, database); err != nil {
		t.Fatal(err)
	}
	var tasks, versions int
	if err = database.QueryRow("SELECT (SELECT COUNT(*) FROM task_record),(SELECT COUNT(*) FROM pipeline_template_versions)").Scan(&tasks, &versions); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || versions != 1 {
		t.Fatalf("unexpected effects tasks=%d versions=%d", tasks, versions)
	}
	t.Run("binding-preflight-read-only", func(t *testing.T) { checkBindingPreflightMySQL(t, database, cfg, v["id"].(string)) })
	auditRaw, _ := json.Marshal(audit.audits)
	if strings.Contains(string(auditRaw), "PRIVATE-REPOSITORY") {
		t.Fatal("audit leaked spec")
	}
}
