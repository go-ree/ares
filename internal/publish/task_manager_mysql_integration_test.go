package publish

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
)

func TestMySQLLegacyLeaderSingleOwnerAndConnectionLossTakeover(t *testing.T) {
	harness := newReleaseMySQLHarness(t)
	harness.migrate(t)
	engine := harness.openAdminEngine(t)
	previousEngine := schemadb.Engine
	schemadb.Engine = engine
	t.Cleanup(func() {
		schemadb.Engine = previousEngine
		if err := engine.Close(); err != nil {
			t.Errorf("close legacy leader engine: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstManager := NewTaskManager()
	first, acquired, err := firstManager.acquireMySQLLeadership(ctx)
	if err != nil || !acquired {
		t.Fatalf("first acquire = acquired:%t error:%v", acquired, err)
	}
	defer first.release()

	for contender := 2; contender <= 3; contender++ {
		leadership, acquired, err := NewTaskManager().acquireMySQLLeadership(ctx)
		if err != nil {
			t.Fatalf("contender %d acquire error = %v", contender, err)
		}
		if acquired {
			leadership.release()
			t.Fatalf("legacy manager %d acquired a concurrently held named lock", contender)
		}
	}

	// Historical nullable numeric columns must not make one malformed active v1
	// row abort the complete drain scan. Normalize NULL to zero, classify the
	// missing execution snapshot before any Jenkins access, and move the row to
	// a safe terminal state through the same leader connection.
	result, err := harness.database.ExecContext(ctx, `INSERT INTO task_record
		(app_name, branch, env, publisher, status, jenkins_address, engine_version,
		 ci_job_name, ci_build_id, cd_build_id, auto_deploy)
		VALUES ('legacy-nullable', 'main', 'test', 'integration-test', ?,
			'https://jenkins.example', 1, 'legacy-build', NULL, NULL, NULL)`,
		entity.StatusPackaging)
	if err != nil {
		t.Fatal(err)
	}
	malformedTaskID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	firstManager.ensureJenkinsCurrent = func(context.Context) error {
		t.Fatal("malformed nullable v1 task reached Jenkins settings refresh")
		return nil
	}
	if err := firstManager.updateLegacyTasks(first.ctx); err != nil {
		t.Fatalf("drain nullable legacy task: %v", err)
	}
	var malformedStatus, malformedMessage string
	if err := harness.database.QueryRowContext(ctx, `SELECT status, message
		FROM task_record WHERE task_id = ?`, malformedTaskID).
		Scan(&malformedStatus, &malformedMessage); err != nil {
		t.Fatal(err)
	}
	if malformedStatus != entity.StatusPackageFailed || !strings.Contains(malformedMessage, "Build ID") {
		t.Fatalf("nullable legacy task state = %q, %q", malformedStatus, malformedMessage)
	}

	lockName := legacyLeaderLockName(harness.databaseName)
	var connectionID sql.NullInt64
	if err := harness.admin.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)", lockName).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}
	if !connectionID.Valid || connectionID.Int64 <= 0 {
		t.Fatalf("named lock connection id = %#v", connectionID)
	}
	if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", connectionID.Int64)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("leader context was not canceled after its dedicated connection was killed")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		third, acquired, err := NewTaskManager().acquireMySQLLeadership(ctx)
		if err == nil && acquired {
			third.release()
			break
		}
		if err != nil {
			t.Fatalf("takeover acquire error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("another legacy manager did not take over after connection loss")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
