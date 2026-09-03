package db

import (
	"ares/internal/entity"
	"testing"
)

func TestDemoDataUsesValidIDsAndTerminalTaskStatuses(t *testing.T) {
	apps := demoApplications()
	seenIDs := make(map[int]struct{}, len(apps))
	for _, app := range apps {
		if app.App.AppId < 10000 || app.App.AppId > 99999 {
			t.Fatalf("demo app %s has invalid ID %d", app.App.AppName, app.App.AppId)
		}
		if _, exists := seenIDs[app.App.AppId]; exists {
			t.Fatalf("duplicate demo app ID %d", app.App.AppId)
		}
		seenIDs[app.App.AppId] = struct{}{}
	}

	for _, task := range demoTaskSeeds() {
		switch task.status {
		case entity.StatusPackaging, entity.StatusPackaged, entity.StatusDeploying, "queued", "running":
			t.Fatalf("demo task status %s would trigger a release worker", task.status)
		}
	}
}

func TestDemoDataProvesEnvironmentsAreDataDriven(t *testing.T) {
	foundPreview := false
	for _, environment := range demoEnvironments() {
		if !environment.Enabled {
			t.Fatalf("demo environment %s should be enabled", environment.Env)
		}
		if environment.Env == "preview" {
			foundPreview = true
		}
	}
	if !foundPreview {
		t.Fatal("demo environments should include a value outside the historical dev/test/moni set")
	}
}
