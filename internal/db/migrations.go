package db

import (
	"ares/internal/config"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

const schemaMigrationsTable = "schema_migrations"

var (
	migrationDBMu     sync.Mutex
	activeMigrationDB *sql.DB
)

type schemaMigration struct {
	version string
	up      func() error
}

var schemaMigrations = []schemaMigration{
	{version: legacyNullStringMigrationVersion, up: migrateLegacyNullStrings},
}

// RunSchemaMigrations applies versioned, resumable data migrations after the
// reference tables have been initialized. MySQL DDL implicitly commits, so
// each migration must be safe to retry and records its version only at the end.
func RunSchemaMigrations() error {
	return withMigrationDatabase(func(database *sql.DB) error {
		ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
		defer cancel()
		if _, err := database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			return fmt.Errorf("create schema migrations table: %w", err)
		}

		for _, migration := range schemaMigrations {
			applied, err := schemaMigrationApplied(migration.version)
			if err != nil {
				return err
			}
			if applied {
				continue
			}
			slog.Info("applying database migration", "version", migration.version)
			if err := migration.up(); err != nil {
				return fmt.Errorf("apply database migration %s: %w", migration.version, err)
			}

			insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err = database.ExecContext(insertCtx,
				"INSERT INTO schema_migrations (version) VALUES (?)", migration.version)
			insertCancel()
			if err != nil {
				return fmt.Errorf("record database migration %s: %w", migration.version, err)
			}
			slog.Info("database migration applied", "version", migration.version)
		}
		return nil
	})
}

// BackfillRequiredTextBeforeSchemaSync is an intentionally unversioned,
// idempotent preparation step. It changes data only; the complete migration
// still runs and records its version after Sync has added columns that did not
// exist in older Ares releases.
func BackfillRequiredTextBeforeSchemaSync() error {
	return withMigrationDatabase(func(*sql.DB) error {
		languageDefaults, err := loadMigrationLanguageDefaults()
		if err != nil {
			return err
		}
		if err := validateRequiredTextBackfills(languageDefaults); err != nil {
			return err
		}
		return backfillRequiredTextColumns(languageDefaults)
	})
}

func withMigrationDatabase(run func(*sql.DB) error) error {
	migrationDBMu.Lock()
	defer migrationDBMu.Unlock()

	database, err := openMigrationDatabase()
	if err != nil {
		return err
	}
	activeMigrationDB = database
	defer func() {
		activeMigrationDB = nil
		_ = database.Close()
	}()
	return run(database)
}

func openMigrationDatabase() (*sql.DB, error) {
	dsn, err := mysql.ParseDSN(config.Main.DB.ConnStr)
	if err != nil {
		return nil, fmt.Errorf("parse database DSN for schema migrations: %w", err)
	}
	driverTimeout := migrationOperationTimeout() + 30*time.Second
	if dsn.ReadTimeout == 0 || dsn.ReadTimeout < driverTimeout {
		dsn.ReadTimeout = driverTimeout
	}
	if dsn.WriteTimeout == 0 || dsn.WriteTimeout < driverTimeout {
		dsn.WriteTimeout = driverTimeout
	}

	database, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open database connection for schema migrations: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := database.PingContext(pingCtx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect database for schema migrations: %w", err)
	}
	return database, nil
}

func migrationDatabase() *sql.DB {
	if activeMigrationDB != nil {
		return activeMigrationDB
	}
	return Engine.DB().DB
}

func migrationOperationTimeout() time.Duration {
	return config.DBSchemaMigrationTimeout()
}

func schemaMigrationAppliedIfTableExists(version string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var tableExists bool
	if err := migrationDatabase().QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
	)`, schemaMigrationsTable).Scan(&tableExists); err != nil {
		return false, fmt.Errorf("check schema migrations table: %w", err)
	}
	if !tableExists {
		return false, nil
	}
	return schemaMigrationApplied(version)
}

func schemaMigrationApplied(version string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var applied bool
	if err := migrationDatabase().QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", version).Scan(&applied); err != nil {
		return false, fmt.Errorf("check database migration %s: %w", version, err)
	}
	return applied, nil
}
