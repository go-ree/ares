package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-ree/ares/internal/config"

	_ "github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

var Engine *xorm.Engine

const (
	runtimeDatabaseMaxOpenConnections = 25
	runtimeDatabaseMaxIdleConnections = 10
	runtimeDatabaseConnectionLifetime = 30 * time.Minute
	runtimeDatabaseConnectionIdleTime = 5 * time.Minute
)

// Init opens the runtime database, verifies the versioned schema without
// changing it, then writes only idempotent reference/demo data. Database DDL is
// owned exclusively by `ares migrate up`.
func Init() error {
	runtimeDSN, err := normalizedAresMySQLDSN(config.Main.DB.ConnStr)
	if err != nil {
		slog.Error("parse runtime database DSN", "error", SafeMigrationErrorText(err))
		return err
	}
	engine, err := xorm.NewEngine("mysql", runtimeDSN)
	if err != nil {
		slog.Error("init xorm error", "error", SafeMigrationErrorText(err))
		return err
	}
	Engine = engine
	runtimeDatabase := engine.DB().DB
	runtimeDatabase.SetMaxOpenConns(runtimeDatabaseMaxOpenConnections)
	runtimeDatabase.SetMaxIdleConns(runtimeDatabaseMaxIdleConnections)
	runtimeDatabase.SetConnMaxLifetime(runtimeDatabaseConnectionLifetime)
	runtimeDatabase.SetConnMaxIdleTime(runtimeDatabaseConnectionIdleTime)
	failed := true
	defer func() {
		if failed {
			_ = engine.Close()
			Engine = nil
		}
	}()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = engine.PingContext(pingCtx)
	pingCancel()
	if err != nil {
		slog.Error("database connection test failed", "error", SafeMigrationErrorText(err))
		return err
	}
	compatibilityCtx, compatibilityCancel := context.WithTimeout(context.Background(), config.DBSchemaMigrationTimeout())
	err = CheckRuntimeCompatibility(compatibilityCtx, engine.DB().DB)
	compatibilityCancel()
	if err != nil {
		slog.Error("database schema compatibility check failed", "error", SafeMigrationErrorText(err))
		return err
	}

	if err := withSeedLock(func() error {
		if err := InitializeReferenceData(); err != nil {
			return err
		}
		if config.Main.DemoData.Enabled {
			if err := InitializeDemoData(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		slog.Error("database seed initialization failed", "error", SafeMigrationErrorText(err))
		return err
	}

	failed = false
	slog.Info("init xorm success", "schema_epoch", ApplicationSchemaEpoch)
	return nil
}

func withSeedLock(initialize func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	conn, err := Engine.DB().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire seed connection: %w", err)
	}
	defer conn.Close()

	var databaseName string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		return fmt.Errorf("resolve database name for seed lock: %w", err)
	}
	digest := sha256.Sum256([]byte(databaseName))
	lockName := "ares_reference_seed_" + hex.EncodeToString(digest[:16])
	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire database seed lock: %w", err)
	}
	if acquired != 1 {
		return fmt.Errorf("timed out waiting for database seed lock")
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		if _, releaseErr := conn.ExecContext(releaseCtx, "DO RELEASE_LOCK(?)", lockName); releaseErr != nil {
			slog.Warn("failed to release database seed lock", "error", SafeMigrationErrorText(releaseErr))
		}
	}()

	return initialize()
}

// These definitions remain as pure planning helpers for the historical
// collation migration tests. Runtime startup never calls them or executes DDL.
type characterColumnDefinition struct {
	characterSet string
	collation    string
}

type pipelineCombinationCollationPlan struct {
	alterTableSQL string
	skipPopulated bool
}

func planPipelineCombinationCollationAlignment(source characterColumnDefinition, targets []characterColumnDefinition, hasRows bool) (pipelineCombinationCollationPlan, error) {
	alignmentNeeded := false
	for _, target := range targets {
		if !strings.EqualFold(target.characterSet, source.characterSet) ||
			!strings.EqualFold(target.collation, source.collation) {
			alignmentNeeded = true
			break
		}
	}
	if !alignmentNeeded {
		return pipelineCombinationCollationPlan{}, nil
	}
	if hasRows {
		return pipelineCombinationCollationPlan{skipPopulated: true}, nil
	}
	definitions := append([]characterColumnDefinition{source}, targets...)
	for _, definition := range definitions {
		if !safeSQLIdentifier(definition.characterSet) {
			return pipelineCombinationCollationPlan{}, fmt.Errorf("invalid character set %q", definition.characterSet)
		}
		if !safeSQLIdentifier(definition.collation) {
			return pipelineCombinationCollationPlan{}, fmt.Errorf("invalid collation %q", definition.collation)
		}
	}
	return pipelineCombinationCollationPlan{alterTableSQL: fmt.Sprintf(
		"ALTER TABLE pipelines_job_combination CONVERT TO CHARACTER SET %s COLLATE %s",
		source.characterSet, source.collation,
	)}, nil
}
