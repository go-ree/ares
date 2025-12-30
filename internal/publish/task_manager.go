package publish

import (
	"ares/internal/db"
	"ares/internal/entity"
	"ares/internal/jenkins"
	"ares/internal/tool"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TaskManager 任务管理器
type TaskManager struct{}

// NewTaskManager 创建新的任务管理器
func NewTaskManager() *TaskManager {
	return &TaskManager{}
}

// UpdateTaskStatuses 更新任务状态
func (tm *TaskManager) UpdateTaskStatuses() {
	tasks, err := tm.fetchTasks()
	if err != nil {
		slog.Error("查询任务列表失败", slog.Any("error", err))
		return
	}
	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Add(1)
		go func(task entity.TaskRecord) {
			defer wg.Done()
			switch task.Status {
			case entity.StatusPackaging:
				if err := tm.handlePackagingTask(task); err != nil {
					slog.Error("处理编译中任务失败", slog.Any("taskId", task.TaskId), slog.Any("error", err))
				}
			case entity.StatusPackaged:
				if err := tm.handlePackagedTask(task); err != nil {
					slog.Error("处理编译成功任务失败", slog.Any("taskId", task.TaskId), slog.Any("error", err))
				}
			case entity.StatusDeploying:
				if err := tm.handleDeployingTask(task); err != nil {
					slog.Error("处理部署中任务失败", slog.Any("taskId", task.TaskId), slog.Any("error", err))
				}
			}
		}(task)

	}
	wg.Wait()
}

// fetchTasks 查询状态为编译中、编译成功或打包中的任务
func (tm *TaskManager) fetchTasks() ([]entity.TaskRecord, error) {
	var tasks []entity.TaskRecord

	// 计算3小时前的时间点
	threeHoursAgo := time.Now().Add(-3 * time.Hour)

	err := db.Engine.
		Where("status IN (?, ?, ?) AND deleted_at IS NULL", entity.StatusPackaging, entity.StatusPackaged, entity.StatusDeploying).
		And("created_at > ?", threeHoursAgo).
		Find(&tasks)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表失败：%s", err)
	}

	// 添加日志记录
	slog.Info("监听构建任务列表状态（状态机）",
		"count", len(tasks),
		"time_range", fmt.Sprintf("获取最近3小时数据，构建状态为packaging、packaged、deploying的数据(%s)", threeHoursAgo.Format("2006-01-02 15:04:05")))

	return tasks, nil
}

// handlePackagingTask 处理编译中任务
func (tm *TaskManager) handlePackagingTask(task entity.TaskRecord) error {
	status, err := jenkins.GetBuildStatus(task.CiJobName, task.CiBuildId)
	if err != nil {
		return fmt.Errorf("查询管线状态失败：%s", err)
	}

	switch status {
	case "RUNNING":
		// 不做任何变动
		return nil
	case "SUCCESS":
		return tm.handlePackagingSuccess(task)
	case "FAILURE", "ABORTED":
		task.Status = entity.StatusPackageFailed
		_, err := db.Engine.ID(task.TaskId).Update(&task)
		return err
	default:
		return nil
	}
}

// handlePackagingSuccess 处理编译成功状态
func (tm *TaskManager) handlePackagingSuccess(task entity.TaskRecord) error {
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(task)
	}

	// 更新状态为“编译完成”
	task.Status = entity.StatusPackaged
	_, err := db.Engine.ID(task.TaskId).Update(&task)
	return err
}

// triggerJenkinsBuild 触发 Jenkins 构建任务
func (tm *TaskManager) triggerJenkinsBuild(task entity.TaskRecord) error {
	jenkinsParam, err := tool.ToMapStringInterface(task.PipelineParam)
	if err != nil {
		return fmt.Errorf("转换参数失败：%s", err)
	}

	jobBuildId, _, err := jenkins.CreateBuildTask(task.CdJobName, jenkinsParam)
	if err != nil {
		return fmt.Errorf("创建构建任务失败：%s", err)
	}

	// 更新任务信息
	task.Status = entity.StatusDeploying
	task.CdBuildId = jobBuildId
	_, err = db.Engine.ID(task.TaskId).Update(&task)
	slog.Info("任务自动部署中",
		"task_id", task.TaskId,
		"app_name", task.AppName,
		"env", task.Env,
		"publisher", task.Publisher,
		"branch", task.Branch,
		"image", task.Products)
	return err
}

// handlePackagedTask 处理编译成功任务
func (tm *TaskManager) handlePackagedTask(task entity.TaskRecord) error {
	// 检测自动部署的状态是否为1，如果是1则触发自动部署，开始继续执行，如果不为1则什么都不做
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(task)
	}
	return nil
}

// handleDeployingTask 处理部署中任务
func (tm *TaskManager) handleDeployingTask(task entity.TaskRecord) error {
	status, err := jenkins.GetBuildStatus(task.CdJobName, task.CdBuildId)
	if err != nil {
		return fmt.Errorf("查询部署状态失败：%s", err)
	}

	switch status {
	case "RUNNING":
		// 不做任何变动
		return nil
	case "SUCCESS":
		task.Status = entity.StatusDeployed
		_, err := db.Engine.ID(task.TaskId).Update(&task)
		return err
	case "FAILURE", "ABORTED":
		task.Status = entity.StatusDeployFailed
		_, err := db.Engine.ID(task.TaskId).Update(&task)
		return err
	default:
		return nil
	}
}
