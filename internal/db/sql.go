package db

import (
	"ares/internal/config"
	"ares/internal/entity"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

var Engine *xorm.Engine

func Init() error {
	var err error
	Engine, err = xorm.NewEngine("mysql", config.Main.DB.ConnStr)
	if err != nil {
		slog.Error("init xorm error", slog.Any("error", err))
		return err
	}

	// 使用 context 设置 ping 超时时间
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 验证数据库连接是否真的建立成功
	if err := Engine.PingContext(ctx); err != nil {
		slog.Error("database connection test failed", slog.Any("error", err))
		return err
	}

	// 在控制台打印生成的SQL语句
	//Engine.ShowSQL(true)

	// Serialize schema synchronization and seed writes. Compose currently runs a
	// single API replica, but this also makes first boot safe if operators scale
	// the service or two instances restart together.
	if err = withInitializationLock(func() error {
		if err := InitializeDB(); err != nil {
			return err
		}
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
		return err
	}

	slog.Info("init xorm success")
	return nil
}

const initializationLockName = "ares_database_initialization"

func withInitializationLock(initialize func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	conn, err := Engine.DB().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire initialization connection: %w", err)
	}
	defer conn.Close()

	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", initializationLockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire database initialization lock: %w", err)
	}
	if acquired != 1 {
		return fmt.Errorf("timed out waiting for database initialization lock")
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		if _, releaseErr := conn.ExecContext(releaseCtx, "DO RELEASE_LOCK(?)", initializationLockName); releaseErr != nil {
			slog.Warn("failed to release database initialization lock", slog.Any("error", releaseErr))
		}
	}()

	return initialize()
}

// InitializeDB 初始化数据库并自动创建表
func InitializeDB() error {
	pipelineCombinationExisted, err := Engine.IsTableExist(new(entity.PipelinesJobCombination))
	if err != nil {
		return fmt.Errorf("check pipelines_job_combination table: %w", err)
	}
	languageRulesExisted, err := Engine.IsTableExist(new(entity.DevLanguageRule))
	if err != nil {
		return fmt.Errorf("check dev_language_rules table: %w", err)
	}

	// 检查并自动创建表
	err = Engine.Sync2(
		new(entity.Apps),
		new(entity.AppConfigs),
		new(entity.AppConfigDomain),
		new(entity.TaskRecord),
		new(entity.TaskRecordImage),
		new(entity.Pipelines),
		new(entity.EnvConfigs),
	)
	if err != nil {
		slog.Error("failed to sync database tables", slog.Any("error", err))
		return err
	}
	// These two tables historically came from init.sql. Xorm 1.x maps its JSON
	// tag to TEXT on MySQL and can rewrite an existing JSON column when another
	// column differs. Only ask Xorm to create missing auxiliary tables so an
	// upgrade never mutates their operator-managed indexes, constraints or JSON.
	if !pipelineCombinationExisted {
		if err := Engine.Sync2(new(entity.PipelinesJobCombination)); err != nil {
			return fmt.Errorf("create pipelines_job_combination table: %w", err)
		}
	}
	if !languageRulesExisted {
		if err := Engine.Sync2(new(entity.DevLanguageRule)); err != nil {
			return fmt.Errorf("create dev_language_rules table: %w", err)
		}
	}

	// Make schema postconditions resumable. A previous process may have stopped
	// after CREATE TABLE but before one of these ALTER statements completed.
	if err := ensureJSONColumn(jsonColumnSpec{
		tableName:        "task_record",
		columnName:       "pipeline_param",
		hasRowsSQL:       "SELECT EXISTS(SELECT 1 FROM task_record LIMIT 1)",
		alterColumnSQL:   "ALTER TABLE task_record MODIFY COLUMN pipeline_param JSON NULL",
		targetDefinition: "JSON NULL",
	}); err != nil {
		return err
	}
	if err := ensureJSONColumn(jsonColumnSpec{
		tableName:        "dev_language_rules",
		columnName:       "rules",
		hasRowsSQL:       "SELECT EXISTS(SELECT 1 FROM dev_language_rules LIMIT 1)",
		alterColumnSQL:   "ALTER TABLE dev_language_rules MODIFY COLUMN rules JSON NOT NULL",
		targetDefinition: "JSON NOT NULL",
	}); err != nil {
		return err
	}
	if err := ensureForeignKey(foreignKeySpec{
		constraintName: "fk_pipelines_ci_job",
		hasRowsSQL:     "SELECT EXISTS(SELECT 1 FROM pipelines_job_combination LIMIT 1)",
		alterTableSQL:  "ALTER TABLE pipelines_job_combination ADD CONSTRAINT fk_pipelines_ci_job FOREIGN KEY (ci_job_name) REFERENCES pipelines(job_name) ON DELETE RESTRICT ON UPDATE CASCADE",
	}); err != nil {
		return err
	}
	if err := ensureForeignKey(foreignKeySpec{
		constraintName: "fk_pipelines_cd_job",
		hasRowsSQL:     "SELECT EXISTS(SELECT 1 FROM pipelines_job_combination LIMIT 1)",
		alterTableSQL:  "ALTER TABLE pipelines_job_combination ADD CONSTRAINT fk_pipelines_cd_job FOREIGN KEY (cd_job_name) REFERENCES pipelines(job_name) ON DELETE RESTRICT ON UPDATE CASCADE",
	}); err != nil {
		return err
	}
	// App APIs validate IDs in the 10000-99999 range. MySQL keeps a higher
	// existing AUTO_INCREMENT value when this statement runs on a populated DB.
	if _, err := Engine.Exec("ALTER TABLE apps AUTO_INCREMENT = 10000"); err != nil {
		slog.Error("failed to set apps auto increment", slog.Any("error", err))
		return err
	}
	slog.Info("database tables synced successfully")
	return nil
}

const schemaOperationTimeout = 30 * time.Second

type jsonColumnSpec struct {
	tableName        string
	columnName       string
	hasRowsSQL       string
	alterColumnSQL   string
	targetDefinition string
}

func ensureJSONColumn(spec jsonColumnSpec) error {
	ctx, cancel := context.WithTimeout(context.Background(), schemaOperationTimeout)
	defer cancel()

	var dataType string
	err := Engine.DB().QueryRowContext(ctx, `SELECT DATA_TYPE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, spec.tableName, spec.columnName).Scan(&dataType)
	if err != nil {
		return fmt.Errorf("inspect %s.%s type: %w", spec.tableName, spec.columnName, err)
	}
	if strings.EqualFold(dataType, "json") {
		return nil
	}

	var hasRows bool
	if err := Engine.DB().QueryRowContext(ctx, spec.hasRowsSQL).Scan(&hasRows); err != nil {
		return fmt.Errorf("check whether %s has rows before JSON repair: %w", spec.tableName, err)
	}
	if hasRows {
		// Converting TEXT to JSON may rebuild and lock a large legacy table.
		// Automatic repair is intentionally limited to an empty table left by an
		// interrupted first boot; populated schemas need an explicit migration.
		slog.Warn("skipping automatic JSON column repair on populated legacy table",
			slog.String("table", spec.tableName),
			slog.String("column", spec.columnName),
			slog.String("current_type", dataType),
			slog.String("target_definition", spec.targetDefinition))
		return nil
	}

	if _, err := Engine.DB().ExecContext(ctx, spec.alterColumnSQL); err != nil {
		return fmt.Errorf("set %s.%s to %s: %w", spec.tableName, spec.columnName, spec.targetDefinition, err)
	}
	slog.Info("repaired JSON column type",
		slog.String("table", spec.tableName),
		slog.String("column", spec.columnName),
		slog.String("definition", spec.targetDefinition))
	return nil
}

type foreignKeySpec struct {
	constraintName string
	hasRowsSQL     string
	alterTableSQL  string
}

func ensureForeignKey(spec foreignKeySpec) error {
	ctx, cancel := context.WithTimeout(context.Background(), schemaOperationTimeout)
	defer cancel()

	var constraintCount int
	err := Engine.DB().QueryRowContext(ctx, `SELECT COUNT(*)
		FROM information_schema.TABLE_CONSTRAINTS
		WHERE CONSTRAINT_SCHEMA = DATABASE()
			AND TABLE_NAME = 'pipelines_job_combination'
			AND CONSTRAINT_NAME = ?
			AND CONSTRAINT_TYPE = 'FOREIGN KEY'`, spec.constraintName).Scan(&constraintCount)
	if err != nil {
		return fmt.Errorf("inspect foreign key %s: %w", spec.constraintName, err)
	}
	if constraintCount > 0 {
		return nil
	}

	var hasRows bool
	if err := Engine.DB().QueryRowContext(ctx, spec.hasRowsSQL).Scan(&hasRows); err != nil {
		return fmt.Errorf("check whether rows exist before foreign key %s repair: %w", spec.constraintName, err)
	}
	if hasRows {
		// Adding a foreign key can scan and lock the whole table. As with JSON
		// conversion, only repair an empty table left by an interrupted first boot.
		slog.Warn("skipping automatic foreign key repair on populated legacy table",
			slog.String("constraint", spec.constraintName))
		return nil
	}

	if _, err := Engine.DB().ExecContext(ctx, spec.alterTableSQL); err != nil {
		return fmt.Errorf("add foreign key %s: %w", spec.constraintName, err)
	}
	slog.Info("repaired missing foreign key", slog.String("constraint", spec.constraintName))
	return nil
}
