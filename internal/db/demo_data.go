package db

import (
	"ares/internal/entity"
	"encoding/json"
	"fmt"
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

// InitializeDemoData seeds a small, coherent data set only when the apps table
// is completely empty. The transaction makes restarts idempotent and prevents
// users' existing data from being overwritten.
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

	appCount, err := session.Unscoped().Count(new(entity.Apps))
	if err != nil {
		return fmt.Errorf("count apps before demo seed: %w", err)
	}
	if appCount != 0 {
		_ = session.Rollback()
		slog.Info("demo data skipped because apps table is not empty", "app_count", appCount)
		return nil
	}

	for _, envConfig := range demoEnvironments() {
		if err := ensureDemoEnvironment(session, envConfig); err != nil {
			return err
		}
	}

	pipelineJobs := make(map[string][2]string)
	for _, packageType := range []string{"golang", "static", "python"} {
		ciJob, cdJob, err := ensureDemoPipelinePair(session, packageType)
		if err != nil {
			return err
		}
		pipelineJobs[packageType] = [2]string{ciJob, cdJob}
	}

	apps := demoApplications()
	configIDs := make(map[string]map[string]int)
	for i := range apps {
		app := &apps[i]
		if _, err := session.Insert(&app.App); err != nil {
			return fmt.Errorf("insert demo app %s: %w", app.App.AppName, err)
		}
		configIDs[app.App.AppName] = make(map[string]int)
		for _, envName := range []string{"dev", "test", "moni"} {
			appConfig := demoAppConfig(app.App.AppId, envName, app.PackageType, app.BaseImage)
			if _, err := session.Insert(&appConfig); err != nil {
				return fmt.Errorf("insert demo config %s/%s: %w", app.App.AppName, envName, err)
			}
			configIDs[app.App.AppName][envName] = appConfig.ConfigID
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

	if err := insertDemoTasks(session, apps, pipelineJobs); err != nil {
		return err
	}

	if err := session.Commit(); err != nil {
		return fmt.Errorf("commit demo data: %w", err)
	}
	committed = true
	slog.Info("demo data initialized", "apps", len(apps), "environments", 3)
	return nil
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
		{Env: "dev", ClusterName: "demo-dev", DescriptionCN: "开发环境", HarborURL: "registry.example.local", HarborProjectName: "ares-dev", NodeVersion: "22", MavenVersion: "3.9"},
		{Env: "test", ClusterName: "demo-test", DescriptionCN: "测试环境", HarborURL: "registry.example.local", HarborProjectName: "ares-test", NodeVersion: "22", MavenVersion: "3.9"},
		{Env: "moni", ClusterName: "demo-moni", DescriptionCN: "模拟环境", HarborURL: "registry.example.local", HarborProjectName: "ares-moni", NodeVersion: "22", MavenVersion: "3.9"},
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

func ensureDemoPipelinePair(session *xorm.Session, packageType string) (string, string, error) {
	var existingCombination entity.PipelinesJobCombination
	has, err := session.Unscoped().Where("code_package_type = ?", packageType).Get(&existingCombination)
	if err != nil {
		return "", "", fmt.Errorf("query demo pipeline combination %s: %w", packageType, err)
	}
	if has {
		return existingCombination.CiJobName, existingCombination.CdJobName, nil
	}

	ciJob := fmt.Sprintf("demo-%s-ci", packageType)
	cdJob := fmt.Sprintf("demo-%s-cd", packageType)
	for _, job := range []entity.Pipelines{
		{JobName: ciJob, DescriptionCN: fmt.Sprintf("%s Demo 构建流水线", packageType), URL: fmt.Sprintf("http://jenkins:8080/job/%s", ciJob)},
		{JobName: cdJob, DescriptionCN: fmt.Sprintf("%s Demo 部署流水线", packageType), URL: fmt.Sprintf("http://jenkins:8080/job/%s", cdJob)},
	} {
		var existingJob entity.Pipelines
		jobExists, err := session.Unscoped().Where("job_name = ?", job.JobName).Get(&existingJob)
		if err != nil {
			return "", "", fmt.Errorf("query demo pipeline %s: %w", job.JobName, err)
		}
		if !jobExists {
			if _, err := session.Insert(&job); err != nil {
				return "", "", fmt.Errorf("insert demo pipeline %s: %w", job.JobName, err)
			}
		}
	}

	combination := entity.PipelinesJobCombination{
		DescriptionCN:   fmt.Sprintf("%s Demo 流水线组合", packageType),
		CiJobName:       ciJob,
		CdJobName:       cdJob,
		CodePackageType: packageType,
	}
	if _, err := session.Insert(&combination); err != nil {
		return "", "", fmt.Errorf("insert demo pipeline combination %s: %w", packageType, err)
	}
	return ciJob, cdJob, nil
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
		PreStopCommand:         "NULL",
	}
}

type demoTaskSeed struct {
	appIndex int
	env      string
	status   string
	message  string
	age      time.Duration
}

func demoTaskSeeds() []demoTaskSeed {
	return []demoTaskSeed{
		{0, "dev", entity.StatusDeployed, "Demo 发布成功", 45 * time.Minute},
		{1, "test", entity.StatusDeployed, "Demo 发布成功", 2 * time.Hour},
		{2, "dev", entity.StatusPackageFailed, "Demo 构建失败记录", 4 * time.Hour},
		{0, "moni", entity.StatusDeployFailed, "Demo 部署失败记录", 8 * time.Hour},
	}
}

func insertDemoTasks(session *xorm.Session, apps []demoApplication, jobs map[string][2]string) error {
	for i, item := range demoTaskSeeds() {
		app := apps[item.appIndex]
		jobPair := jobs[app.PackageType]
		branch := "main"
		if item.env == "moni" {
			branch = "release_demo"
		}
		params, err := json.Marshal(map[string]string{
			"app_name": app.App.AppName,
			"branch":   branch,
			"env":      item.env,
		})
		if err != nil {
			return fmt.Errorf("marshal demo task parameters: %w", err)
		}
		createdAt := time.Now().Add(-item.age)
		task := entity.TaskRecord{
			AppName:       app.App.AppName,
			Branch:        branch,
			Env:           item.env,
			Publisher:     "Demo 用户",
			CiBuildId:     int64(100 + i),
			CdBuildId:     int64(200 + i),
			PipelineParam: params,
			Status:        item.status,
			Message:       item.message,
			CiJobName:     jobPair[0],
			CdJobName:     jobPair[1],
			AutoDeploy:    1,
			Products:      fmt.Sprintf("registry.example.local/ares-demo/%s:demo", app.App.AppName),
			CreatedTime:   createdAt,
			UpdatedTime:   createdAt,
		}
		if _, err := session.NoAutoTime().Insert(&task); err != nil {
			return fmt.Errorf("insert demo task %s/%s: %w", app.App.AppName, item.env, err)
		}
	}
	return nil
}
