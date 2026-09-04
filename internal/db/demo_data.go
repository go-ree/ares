package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/workflow"
	"log/slog"
	"time"

	"xorm.io/xorm"
)

var defaultLanguageRules = []entity.DevLanguageRule{
	{DevLanguage: "java", Rules: json.RawMessage(`{"allowed":["jar","war"],"default":"jar"}`)},
	{DevLanguage: "python", Rules: json.RawMessage(`{"allowed":["python","ai"],"default":"python"}`)},
	{DevLanguage: "node.js", Rules: json.RawMessage(`{"allowed":["static","miniapp","node.js"],"default":"node.js"}`)},
	{DevLanguage: "golang", Rules: json.RawMessage(`{"allowed":["golang"],"default":"golang"}`)},
}

// InitializeReferenceData inserts only missing platform defaults. Existing
// operator-managed values are deliberately left untouched.
func InitializeReferenceData() error {
	session := Engine.NewSession()
	defer session.Close()
	if err := session.Begin(); err != nil {
		return fmt.Errorf("begin reference data transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = session.Rollback()
		}
	}()

	for _, rule := range defaultLanguageRules {
		var existing entity.DevLanguageRule
		has, err := session.Unscoped().Where("dev_language = ?", rule.DevLanguage).Get(&existing)
		if err != nil {
			return fmt.Errorf("query language rule %s: %w", rule.DevLanguage, err)
		}
		if has {
			continue
		}
		if _, err := session.Insert(&rule); err != nil {
			return fmt.Errorf("insert language rule %s: %w", rule.DevLanguage, err)
		}
	}

	if err := session.Commit(); err != nil {
		return fmt.Errorf("commit reference data: %w", err)
	}
	committed = true
	return nil
}

type demoApplication struct {
	App         entity.Apps
	PackageType string
	BaseImage   string
}

// InitializeDemoData seeds a small, coherent data set only when all related
// business tables are empty. The transaction makes restarts idempotent and
// avoids attaching demo rows to a partially initialized or restored database.
func InitializeDemoData() error {
	session := Engine.NewSession()
	defer session.Close()
	if err := session.Begin(); err != nil {
		return fmt.Errorf("begin demo data transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = session.Rollback()
		}
	}()

	nonEmptyTables, err := nonEmptyDemoBusinessTables(session)
	if err != nil {
		return err
	}
	if len(nonEmptyTables) > 0 {
		_ = session.Rollback()
		slog.Warn("demo data skipped because business tables are not empty", "tables", nonEmptyTables)
		return nil
	}

	for _, envConfig := range demoEnvironments() {
		if err := ensureDemoEnvironment(session, envConfig); err != nil {
			return err
		}
	}

	apps := demoApplications()
	configIDs := make(map[string]map[string]int)
	workflowVersionIDs := make(map[string]map[string]int64)
	for i := range apps {
		app := &apps[i]
		if _, err := session.Insert(&app.App); err != nil {
			return fmt.Errorf("insert demo app %s: %w", app.App.AppName, err)
		}
		configIDs[app.App.AppName] = make(map[string]int)
		workflowVersionIDs[app.App.AppName] = make(map[string]int64)
		for _, environment := range demoEnvironments() {
			envName := environment.Env
			appConfig := demoAppConfig(app.App.AppId, envName, app.PackageType, app.BaseImage)
			if _, err := session.Nullable(
				"code_package_path",
				"code_package_name",
				"base_image",
				"pre_stop_command",
			).Insert(&appConfig); err != nil {
				return fmt.Errorf("insert demo config %s/%s: %w", app.App.AppName, envName, err)
			}
			configIDs[app.App.AppName][envName] = appConfig.ConfigID
			versionID, err := insertDemoWorkflow(session, app.App.AppName, envName, appConfig.ConfigID)
			if err != nil {
				return err
			}
			workflowVersionIDs[app.App.AppName][envName] = versionID
		}
	}

	for appName, envConfigs := range configIDs {
		for envName, configID := range envConfigs {
			domain := entity.AppConfigDomain{
				ConfigID: configID,
				Host:     fmt.Sprintf("%s.%s.example.local", appName, envName),
				Path:     "/",
			}
			if _, err := session.Insert(&domain); err != nil {
				return fmt.Errorf("insert demo domain %s/%s: %w", appName, envName, err)
			}
		}
	}

	if err := insertDemoTasks(session, apps, workflowVersionIDs); err != nil {
		return err
	}

	if err := session.Commit(); err != nil {
		return fmt.Errorf("commit demo data: %w", err)
	}
	committed = true
	slog.Info("demo data initialized", "apps", len(apps), "environments", len(demoEnvironments()))
	return nil
}

func nonEmptyDemoBusinessTables(session *xorm.Session) ([]string, error) {
	tables := []struct {
		name string
		bean any
	}{
		{"env_configs", new(entity.EnvConfigs)},
		{"apps", new(entity.Apps)},
		{"app_configs", new(entity.AppConfigs)},
		{"app_config_domains", new(entity.AppConfigDomain)},
		{"task_record", new(entity.TaskRecord)},
		{"task_record_images", new(entity.TaskRecordImage)},
		{"release_workflows", new(entity.ReleaseWorkflow)},
		{"release_workflow_versions", new(entity.ReleaseWorkflowVersion)},
		{"app_config_workflows", new(entity.AppConfigWorkflow)},
		{"task_step_records", new(entity.TaskStepRecord)},
	}
	nonEmpty := make([]string, 0)
	for _, table := range tables {
		count, err := session.Unscoped().Count(table.bean)
		if err != nil {
			return nil, fmt.Errorf("count %s before demo seed: %w", table.name, err)
		}
		if count > 0 {
			nonEmpty = append(nonEmpty, fmt.Sprintf("%s=%d", table.name, count))
		}
	}
	return nonEmpty, nil
}

func demoApplications() []demoApplication {
	return []demoApplication{
		{
			App: entity.Apps{
				AppId:         10000,
				AppName:       "demo-api",
				AppNameCn:     "Demo API 服务",
				Owner:         "demo.user",
				OwnerCN:       "演示用户",
				DevLanguage:   "golang",
				DescriptionCN: "用于体验 Ares 应用配置和发布记录的 Go 服务",
				GitUrl:        "git@github.com:go-ree/ares.git",
			},
			PackageType: "golang",
			BaseImage:   "golang:1.23-alpine",
		},
		{
			App: entity.Apps{
				AppId:         10001,
				AppName:       "demo-web",
				AppNameCn:     "Demo Web 前端",
				Owner:         "demo.user",
				OwnerCN:       "演示用户",
				DevLanguage:   "node.js",
				DescriptionCN: "用于体验静态前端发布配置的示例应用",
				GitUrl:        "git@github.com:go-ree/chaoscanvas.git",
			},
			PackageType: "static",
			BaseImage:   "nginx:alpine",
		},
		{
			App: entity.Apps{
				AppId:         10002,
				AppName:       "demo-worker",
				AppNameCn:     "Demo 异步任务",
				Owner:         "demo.user",
				OwnerCN:       "演示用户",
				DevLanguage:   "python",
				DescriptionCN: "用于体验失败记录和 Python 配置的示例应用",
				GitUrl:        "git@github.com:example/demo-worker.git",
			},
			PackageType: "python",
			BaseImage:   "python:3.13-alpine",
		},
	}
}

func demoEnvironments() []entity.EnvConfigs {
	return []entity.EnvConfigs{
		{Env: "dev", DescriptionCN: "开发环境", Enabled: true, SortOrder: 10, HarborURL: "registry.example.local", HarborProjectName: "ares-dev"},
		{Env: "test", DescriptionCN: "测试环境", Enabled: true, SortOrder: 20, HarborURL: "registry.example.local", HarborProjectName: "ares-test"},
		{Env: "moni", DescriptionCN: "模拟环境", Enabled: true, SortOrder: 30, HarborURL: "registry.example.local", HarborProjectName: "ares-moni"},
		{Env: "preview", DescriptionCN: "预览环境", Enabled: true, SortOrder: 40, HarborURL: "registry.example.local", HarborProjectName: "ares-preview"},
	}
}

func ensureDemoEnvironment(session *xorm.Session, row entity.EnvConfigs) error {
	var existing entity.EnvConfigs
	has, err := session.Unscoped().Where("env = ?", row.Env).Get(&existing)
	if err != nil {
		return fmt.Errorf("query demo environment %s: %w", row.Env, err)
	}
	if has {
		return nil
	}
	if _, err := session.Insert(&row); err != nil {
		return fmt.Errorf("insert demo environment %s: %w", row.Env, err)
	}
	return nil
}

func insertDemoWorkflow(session *xorm.Session, appName, env string, configID int) (int64, error) {
	name := fmt.Sprintf("%s/%s Demo 发布流程", appName, env)
	spec := workflow.WorkflowSpec{
		SchemaVersion: workflow.SchemaVersionV1,
		Name:          name,
		Steps: []workflow.StepSpec{
			{
				Key: "prepare", Name: "准备发布上下文", Uses: workflow.NoopUses,
				Category: "prepare", With: json.RawMessage(`{"message":"发布上下文已准备"}`),
				TimeoutSeconds: 60, OnFailure: workflow.FailureStop,
			},
			{
				Key: "release", Name: "模拟发布", Uses: workflow.NoopUses,
				Category: "deploy", With: json.RawMessage(`{"message":"Demo 发布完成","output":{"demo":true}}`),
				TimeoutSeconds: 60, OnFailure: workflow.FailureStop,
			},
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return 0, fmt.Errorf("marshal demo workflow %s/%s: %w", appName, env, err)
	}
	digest := sha256.Sum256(specJSON)
	workflowRow := entity.ReleaseWorkflow{
		Name:        name,
		Description: "无需外部 CI/CD 平台即可运行的示例流程",
	}
	if _, err := session.Insert(&workflowRow); err != nil {
		return 0, fmt.Errorf("insert demo workflow %s/%s: %w", appName, env, err)
	}
	versionRow := entity.ReleaseWorkflowVersion{
		WorkflowID: workflowRow.WorkflowID,
		Version:    1,
		Spec:       specJSON,
		Checksum:   hex.EncodeToString(digest[:]),
		CreatedBy:  "demo-seed",
	}
	if _, err := session.Insert(&versionRow); err != nil {
		return 0, fmt.Errorf("insert demo workflow version %s/%s: %w", appName, env, err)
	}
	binding := entity.AppConfigWorkflow{
		AppConfigID: configID,
		WorkflowID:  workflowRow.WorkflowID,
		VersionID:   versionRow.VersionID,
		Revision:    1,
	}
	if _, err := session.Insert(&binding); err != nil {
		return 0, fmt.Errorf("bind demo workflow %s/%s: %w", appName, env, err)
	}
	return versionRow.VersionID, nil
}

func demoAppConfig(appID int, envName, packageType, baseImage string) entity.AppConfigs {
	return entity.AppConfigs{
		AppID:                  appID,
		Env:                    envName,
		CodePackageType:        packageType,
		CodePackagePath:        ".",
		CodePackageName:        "app",
		BaseImage:              baseImage,
		PodCount:               1,
		LimitsMemory:           1,
		GpuCount:               0,
		ProbeType:              "HTTP",
		ProbeCheckPath:         "/health",
		ProbeCheckTcpPort:      8080,
		ProbeCheckHttpPort:     8080,
		ProbeStopCheckHttpPort: 8080,
		ContainerPort:          8080,
		PreStopType:            "HTTP",
		PreStopCheckPath:       "/health",
		PreStopCommand:         "",
	}
}

type demoTaskSeed struct {
	appIndex int
	env      string
	branch   string
	status   string
	message  string
	age      time.Duration
}

func demoTaskSeeds() []demoTaskSeed {
	return []demoTaskSeed{
		{0, "dev", "main", workflow.TaskSucceeded, "Demo 发布成功", 45 * time.Minute},
		{1, "test", "main", workflow.TaskSucceeded, "Demo 发布成功", 2 * time.Hour},
		{2, "preview", "feature/demo", workflow.TaskFailed, "Demo 步骤失败记录", 4 * time.Hour},
		{0, "moni", "release_demo", workflow.TaskSucceededWithWarnings, "Demo 带告警完成记录", 8 * time.Hour},
	}
}

func insertDemoTasks(session *xorm.Session, apps []demoApplication, workflowVersions map[string]map[string]int64) error {
	for i, item := range demoTaskSeeds() {
		app := apps[item.appIndex]
		params, err := json.Marshal(map[string]string{
			"app_name": app.App.AppName,
			"branch":   item.branch,
			"env":      item.env,
		})
		if err != nil {
			return fmt.Errorf("marshal demo task parameters: %w", err)
		}
		createdAt := time.Now().Add(-item.age)
		task := entity.TaskRecord{
			AppName:           app.App.AppName,
			Branch:            item.branch,
			Env:               item.env,
			Publisher:         "Demo 用户",
			PipelineParam:     params,
			Status:            item.status,
			Message:           item.message,
			EngineVersion:     2,
			WorkflowVersionID: workflowVersions[app.App.AppName][item.env],
			Products:          fmt.Sprintf("registry.example.local/ares-demo/%s:demo", app.App.AppName),
			CreatedTime:       createdAt,
			UpdatedTime:       createdAt,
		}
		if _, err := session.NoAutoTime().Nullable("rundeck_app_name", "ci_job_name", "cd_job_name").Insert(&task); err != nil {
			return fmt.Errorf("insert demo task %s/%s: %w", app.App.AppName, item.env, err)
		}
		if err := insertDemoTaskSteps(session, task, item, i); err != nil {
			return err
		}
	}
	return nil
}

func insertDemoTaskSteps(session *xorm.Session, task entity.TaskRecord, seed demoTaskSeed, index int) error {
	createdAt := task.CreatedTime
	steps := []entity.TaskStepRecord{
		{
			TaskID: task.TaskId, WorkflowVersionID: task.WorkflowVersionID,
			StepKey: "prepare", Name: "准备发布上下文", Uses: workflow.NoopUses, Category: "prepare", Position: 0,
			Config: json.RawMessage(`{"message":"发布上下文已准备"}`), TimeoutSeconds: 60,
			OnFailure: workflow.FailureStop, Status: workflow.StepSucceeded, Attempt: 1,
			Output: json.RawMessage(`{}`), Message: "发布上下文已准备",
			StartedTime: &createdAt, FinishedTime: &createdAt, CreatedTime: createdAt, UpdatedTime: createdAt,
		},
		{
			TaskID: task.TaskId, WorkflowVersionID: task.WorkflowVersionID,
			StepKey: "release", Name: "模拟发布", Uses: workflow.NoopUses, Category: "deploy", Position: 1,
			Config: json.RawMessage(`{"message":"Demo 发布完成","output":{"demo":true}}`), TimeoutSeconds: 60,
			OnFailure: workflow.FailureStop, Status: workflow.StepSucceeded, Attempt: 1,
			Output: json.RawMessage(`{"demo":true}`), Message: seed.message,
			StartedTime: &createdAt, FinishedTime: &createdAt, CreatedTime: createdAt, UpdatedTime: createdAt,
		},
	}
	if seed.status == workflow.TaskFailed {
		steps[0].Status = workflow.StepFailed
		steps[0].Message = seed.message
		steps[1].Status = workflow.StepSkipped
		steps[1].Message = "前置步骤失败，流程已停止"
		steps[1].Output = nil
		steps[1].StartedTime = nil
	}
	if seed.status == workflow.TaskSucceededWithWarnings {
		steps[0].Status = workflow.StepFailed
		steps[0].OnFailure = workflow.FailureContinue
		steps[0].Message = "Demo 非阻断检查失败"
	}
	for stepIndex := range steps {
		insert := session.NoAutoTime().Nullable("external_ref")
		if len(steps[stepIndex].Output) == 0 {
			insert = insert.Nullable("output")
		}
		if steps[stepIndex].StartedTime == nil {
			insert = insert.Nullable("started_at")
		}
		if _, err := insert.Insert(&steps[stepIndex]); err != nil {
			return fmt.Errorf("insert demo task step %s/%s/%d: %w", task.AppName, task.Env, index, err)
		}
	}
	return nil
}
