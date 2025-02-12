package publish

import (
	"encoding/json"
	"fmt"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/db"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/entity"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/jenkins"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/tool"
	"strconv"
	"time"
)

// PublishingEntry 所有类型项目的统一发布入口，由这里决定具体的发布动作
func PublishingEntry(appName string, branch string, env string) error {
	var app entity.Apps
	exists, err := db.Engine.Where("app_name = ? AND deleted_at IS NULL", appName).Get(&app)
	if err != nil {
		return fmt.Errorf("应用信息查询失败：%s", err)
	}
	if !exists {
		return fmt.Errorf("未找到应用：%s", appName)
	}
	//fmt.Printf("APPID:%d app_name:%s deleted_at:%s,created_at:%s \n", app.AppId, app.AppName, app.DeletedTime, app.CreatedTime.Format("2006-01-02 15:04:05"))

	// 检验发布的应用环境是否存在
	var appConfig entity.AppConfigs
	exists, err = db.Engine.Where("app_id = ? AND env = ? AND deleted_at IS NULL", app.AppId, env).Get(&appConfig)
	if err != nil {
		return fmt.Errorf("配置信息查询失败：%s", err)
	}
	if !exists {
		return fmt.Errorf("未找到对应环境配置信息，appId：%d,env：%s", app.AppId, env)
	}
	//fmt.Printf("aaaa%+v\n", appConfig)

	// 这里需要构建实际的发布数据
	jenkinsParam, err := ComposePublishData(appName, branch, env)
	if err != nil {
		return err
	}
	fmt.Println(jenkinsParam)

	fmt.Println(appConfig.CodePackageType)

	// 确定该类型的项目应该走哪个发布管线
	var pipelines entity.Pipelines
	exists, err = db.Engine.Where("code_package_type = ? AND deleted_at IS NULL", appConfig.CodePackageType).Get(&pipelines)
	if err != nil {
		return fmt.Errorf("代码包类型所属管线查询失败：%s", err)
	}
	jobBuildId, jobName, err := jenkins.CreateBuildTask(pipelines.JobName, jenkinsParam)
	if err != nil {
		return err
	}
	fmt.Println(jobBuildId)
	fmt.Println(jobName)

	return nil
}

// ComposePublishData 构建发布数据
func ComposePublishData(appName string, branch string, env string) (map[string]string, error) {
	JenkinsParam := make(map[string]string)
	var app entity.Apps
	var appConfig entity.AppConfigs
	var envConfig entity.EnvConfigs
	exists, err := db.Engine.Where("app_name = ? AND deleted_at IS NULL", appName).Get(&app)
	if err != nil {
		return nil, fmt.Errorf("应用信息查询失败：%s", err)
	}
	if !exists {
		return nil, fmt.Errorf("未找到应用：%s", appName)
	}

	exists, err = db.Engine.Where("app_id = ? AND env = ? AND deleted_at IS NULL", app.AppId, env).Get(&appConfig)
	if err != nil {
		return nil, fmt.Errorf("配置信息查询失败：%s", err)
	}
	if !exists {
		return nil, fmt.Errorf("未找到对应环境配置信息，appId：%d,env：%s", app.AppId, env)
	}

	exists, err = db.Engine.Where("env = ? AND deleted_at IS NULL", env).Get(&envConfig)
	if err != nil {
		return nil, fmt.Errorf("配置信息查询失败：%s", err)
	}
	if !exists {
		return nil, fmt.Errorf("未找到环境配置：%s", env)
	}
	// 输出当前时间的时间戳，精确到毫秒
	milliseconds := time.Now().UnixMilli()

	// 示例格式
	// harbor.ttpai.work/publish/dev/asr-job:1739265948923
	image := envConfig.HarborURL + "/" + envConfig.HarborProjectName + "/" + env + "/" + appName + ":" + fmt.Sprintf("%d", milliseconds)

	JenkinsParam["app_name"] = appName
	JenkinsParam["env"] = env
	JenkinsParam["branch"] = branch
	JenkinsParam["git_url"] = app.GitUrl
	JenkinsParam["code_package_type"] = appConfig.CodePackageType
	JenkinsParam["code_package_path"] = appConfig.CodePackagePath
	JenkinsParam["code_package_name"] = appConfig.CodePackageName
	JenkinsParam["base_image"] = appConfig.BaseImage
	JenkinsParam["pod_count"] = strconv.Itoa(appConfig.PodCount)
	JenkinsParam["limits_memory"] = strconv.Itoa(appConfig.LimitsMemory)
	JenkinsParam["probe_type"] = appConfig.ProbeType
	JenkinsParam["probe_check_path"] = appConfig.ProbeCheckPath
	JenkinsParam["pre_stop_type"] = appConfig.PreStopType
	JenkinsParam["pre_stop_check_path"] = appConfig.ProbeCheckPath
	JenkinsParam["pre_stop_command"] = appConfig.PreStopCommand
	JenkinsParam["domain"] = appConfig.Domain
	JenkinsParam["domain_path"] = appConfig.DomainPath
	JenkinsParam["image"] = image
	jsonStr, err := tool.ToJSON(JenkinsParam)
	if err != nil {
		return nil, err
	}
	fmt.Println(jsonStr)
	// 先直接在数据库中写入数据，此时记录状态信息
	// 然后携带相关的发布信息，对jenkins发送api请求，jenkins会返回对应的build_number，这个用于查询执行状态和获取日志信息。
	//
	taskRecord := &entity.TaskRecord{
		AppName:       appName,
		PipelineParam: json.RawMessage(jsonStr),
		Products:      image,
	}
	_, err = db.Engine.Insert(taskRecord)
	if err != nil {
		return nil, fmt.Errorf("任务记录创建失败: %s", err)
	}
	fmt.Println("aaaaa", taskRecord.TaskId)

	return JenkinsParam, nil
}

// QueryTaskStatus 查询任务的执行状态
// 实际的执行逻辑就是根据任务id查询数据库中的status字段，由此判断任务的执行状态并返回结果。
func QueryTaskStatus(taskId int) (string, error) {

	return "成功", nil
}
