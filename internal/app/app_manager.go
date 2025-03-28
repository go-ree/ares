package app

import (
	"ares/internal/entity"
	"context"
	"log/slog"
)

// CreateAppWithTx 使用事务创建应用
func (am *AppManager) CreateAppWithTx(ctx context.Context, req *CreateAppRequest) (*entity.Apps, error) {
	// 检查应用是否已存在
	exists, err := am.checkAppExists(ctx, req.AppName)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, NewDuplicateAppError(req.AppName)
	}

	// 这里应该开启数据库事务
	// tx, err := am.db.BeginTx(ctx, nil)
	// if err != nil {
	//     return nil, err
	// }
	// defer tx.Rollback()

	// 创建应用
	app := &entity.Apps{
		AppName:       req.AppName,
		AppNameCn:     req.AppNameCN,
		Owner:         req.Owner,
		OwnerCN:       req.Owner,
		DevLanguage:   req.DevLanguage,
		DescriptionCN: req.DescriptionCN,
		GitUrl:        req.GitUrl,
		// 其他字段...
	}

	// 保存到数据库
	// err = am.repo.CreateApp(ctx, tx, app)
	// if err != nil {
	//     return nil, err
	// }

	// 提交事务
	// if err = tx.Commit(); err != nil {
	//     return nil, err
	// }

	// 模拟创建成功
	app.AppId = 1 // 实际应该是数据库生成的ID

	slog.Info("应用创建成功",
		"app_id", app.AppId,
		"app_name", app.AppName)

	return app, nil
}

// checkAppExists 检查应用是否已存在
func (am *AppManager) checkAppExists(ctx context.Context, name string) (bool, error) {
	// 实际实现应该查询数据库
	// return am.repo.ExistsByNameAndEnv(ctx, name, env)

	// 模拟实现
	return false, nil
}

// TriggerPostCreateHooks 触发应用创建后的钩子函数
func (am *AppManager) TriggerPostCreateHooks(app *entity.Apps) {
	slog.Info("触发应用创建后处理", "app_id", app.AppId)

	// 可以在这里实现各种后续处理逻辑
	// 1. 创建默认配置
	am.createDefaultConfig(app)

	// 2. 初始化CI/CD流水线
	am.initCIPipeline(app)

	// 3. 发送通知
	am.sendNotification(app)
}

// 创建默认配置
func (am *AppManager) createDefaultConfig(app *entity.Apps) {
	slog.Info("为应用创建默认配置", "app_id", app.AppId)
	// 实现创建默认配置的逻辑
}

// 初始化CI流水线
func (am *AppManager) initCIPipeline(app *entity.Apps) {
	slog.Info("为应用初始化CI流水线", "app_id", app.AppId)
	// 实现初始化CI流水线的逻辑
}

// 发送通知
func (am *AppManager) sendNotification(app *entity.Apps) {
	slog.Info("发送应用创建通知", "app_id", app.AppId)
	// 实现发送通知的逻辑
}
