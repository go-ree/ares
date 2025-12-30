package db

import (
	"ares/internal/config"
	"ares/internal/entity"
	"context"
	"log/slog"
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

	// 初始化数据库并自动创建表
	err = InitializeDB()
	if err != nil {
		return err
	}

	slog.Info("init xorm success")
	return nil
}

// InitializeDB 初始化数据库并自动创建表
func InitializeDB() error {
	// 检查并自动创建表
	err := Engine.Sync2(
		new(entity.Apps),
		new(entity.AppConfigs),
		new(entity.TaskRecord),
		new(entity.TaskRecordImage),
		new(entity.Pipelines),
		new(entity.EnvConfigs),
	)
	if err != nil {
		slog.Error("failed to sync database tables", slog.Any("error", err))
		return err
	}
	slog.Info("database tables synced successfully")
	return nil
}
