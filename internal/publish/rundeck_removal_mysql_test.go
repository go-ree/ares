package publish

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/app"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
)

func TestMySQLRundeckRemoval(t *testing.T) {
	h := newReleaseMySQLHarness(t)
	h.migrate(t)
	h.seed(t)
	engine := h.openAdminEngine(t)
	previous := db.Engine
	db.Engine = engine
	t.Cleanup(func() { db.Engine = previous; _ = engine.Close() })
	ctx := context.Background()
	const name = "w05-idempotent-ready"
	if _, err := h.database.ExecContext(ctx, "UPDATE apps SET rundeck_app_name = 'legacy-alias' WHERE app_name = ?", name); err != nil {
		t.Fatal(err)
	}
	if _, err := h.database.ExecContext(ctx, "INSERT INTO task_record (app_name,rundeck_app_name,branch,env,publisher) VALUES (?, 'legacy-alias','main','integration','tester')", name); err != nil {
		t.Fatal(err)
	}
	manager := NewPublishManager()
	application, err := manager.VerifyApp(&PublishRequest{AppName: name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.VerifyApp(&PublishRequest{AppName: "legacy-alias"}); err == nil {
		t.Fatal("legacy alias unexpectedly resolved")
	}
	title := "新名称"
	updated, err := app.NewAppManager().PatchAppByID(ctx, int64(application.AppId), app.PatchAppRequest{AppNameCN: &title})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(updated)
	if err != nil || strings.Contains(string(body), "rundeck") {
		t.Fatalf("application response = %s, error = %v", body, err)
	}
	for _, filter := range []string{name, "legacy-alias"} {
		session := manager.buildPublishQuery(ctx, PublishQuery{AppName: filter})
		var rows []entity.TaskRecord
		err := session.Find(&rows)
		_ = session.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if filter == name {
			want = 1
		}
		if len(rows) != want {
			t.Fatalf("filter %q: got %d records, want %d", filter, len(rows), want)
		}
		body, err := json.Marshal(rows)
		if err != nil || strings.Contains(string(body), "rundeck") {
			t.Fatalf("task response = %s, error = %v", body, err)
		}
	}
	var alias string
	if err := h.database.QueryRowContext(ctx, "SELECT rundeck_app_name FROM apps WHERE app_name = ?", name).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if alias != "legacy-alias" {
		t.Fatal("historical alias was modified")
	}
}
