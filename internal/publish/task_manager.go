package publish

import (
	"context"
	"fmt"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/release"
	"github.com/go-ree/ares/internal/tool"
	"github.com/go-ree/ares/internal/workflow"
	"log/slog"
	"strings"
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
	if err := tm.updateWorkflowTasks(context.Background()); err != nil {
		slog.Error("推进通用发布任务失败", "error_type", fmt.Sprintf("%T", err))
	}
	// v1 任务只由旧状态机收尾。新任务即使使用 Jenkins，也只进入
	// 通用步骤引擎，二者不会 shadow 双执行。
	tasks, err := tm.fetchTasks()
	if err != nil {
		slog.Error("查询任务列表失败", "error_type", fmt.Sprintf("%T", err))
		return
	}
	jobs := make(chan entity.TaskRecord)
	workerCount := len(tasks)
	if workerCount > 8 {
		workerCount = 8
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if err := validateLegacyTaskAddress(task.JenkinsAddress); err != nil {
					if failErr := tm.failUnsafeLegacyTask(task, err); failErr != nil {
						slog.Error("终止无法安全续跑的旧版任务失败", slog.Any("taskId", task.TaskId), "error_type", fmt.Sprintf("%T", failErr))
					}
					continue
				}
				snapshot, release := jenkins.AcquireForOperation()
				if snapshot == nil {
					release()
					continue
				}
				if snapshot.Address() != normalizedLegacyTaskAddress(task.JenkinsAddress) {
					release()
					if failErr := tm.failUnsafeLegacyTask(task, fmt.Errorf("任务绑定的 Jenkins 实例与当前设置不一致")); failErr != nil {
						slog.Error("终止 Jenkins 实例不匹配的旧版任务失败", slog.Any("taskId", task.TaskId), "error_type", fmt.Sprintf("%T", failErr))
					}
					continue
				}
				switch task.Status {
				case entity.StatusPackaging:
					if err := tm.handlePackagingTask(task, snapshot); err != nil {
						slog.Error("处理编译中任务失败", slog.Any("taskId", task.TaskId), "error_type", fmt.Sprintf("%T", err))
					}
				case entity.StatusPackaged:
					if err := tm.handlePackagedTask(task, snapshot); err != nil {
						slog.Error("处理编译成功任务失败", slog.Any("taskId", task.TaskId), "error_type", fmt.Sprintf("%T", err))
					}
				case entity.StatusDeploying:
					if err := tm.handleDeployingTask(task, snapshot); err != nil {
						slog.Error("处理部署中任务失败", slog.Any("taskId", task.TaskId), "error_type", fmt.Sprintf("%T", err))
					}
				}
				release()
			}
		}()
	}
	for _, task := range tasks {
		jobs <- task
	}
	close(jobs)
	wg.Wait()
}

func (tm *TaskManager) updateWorkflowTasks(ctx context.Context) error {
	var tasks []entity.TaskRecord
	if err := db.Engine.Context(ctx).
		Where("engine_version = ? AND status IN (?, ?) AND deleted_at IS NULL", 2, workflow.TaskQueued, workflow.TaskRunning).
		Asc("updated_at", "task_id").Limit(200).Find(&tasks); err != nil {
		return fmt.Errorf("查询通用发布任务：%w", err)
	}
	if len(tasks) == 0 {
		return nil
	}
	runtime := release.Shared()
	jobs := make(chan int)
	workerCount := len(tasks)
	if workerCount > 8 {
		workerCount = 8
	}
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for taskID := range jobs {
				if _, err := runtime.Coordinator.RunUntilBlocked(ctx, taskID, 0); err != nil {
					slog.Warn("通用发布任务本轮协调失败，稍后重试", "task_id", taskID, "error_type", fmt.Sprintf("%T", err))
				}
				// Rotate every observed task to the back of the oldest-first scan,
				// including transient failures that do not update a step row. This
				// prevents the first 200 long-running tasks from starving the rest.
				if _, err := db.Engine.Context(ctx).Table(new(entity.TaskRecord)).ID(taskID).
					Update(map[string]any{"updated_at": time.Now()}); err != nil {
					slog.Warn("更新通用发布任务轮询时间失败", "task_id", taskID, "error_type", fmt.Sprintf("%T", err))
				}
			}
		}()
	}
	for _, task := range tasks {
		jobs <- task.TaskId
	}
	close(jobs)
	workers.Wait()
	return nil
}

// fetchTasks 查询状态为编译中、编译成功或打包中的任务
func (tm *TaskManager) fetchTasks() ([]entity.TaskRecord, error) {
	var tasks []entity.TaskRecord

	err := db.Engine.
		Where(`engine_version < ? AND deleted_at IS NULL AND (
			status IN (?, ?) OR (status = ? AND auto_deploy = 1)
		)`, 2, entity.StatusPackaging, entity.StatusDeploying, entity.StatusPackaged).
		Find(&tasks)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表失败：%s", err)
	}
	for i := range tasks {
		normalizeTaskRecordNullableText(&tasks[i])
	}

	// 添加日志记录
	slog.Info("监听构建任务列表状态（状态机）",
		"count", len(tasks),
		"engine_version", 1)

	return tasks, nil
}

// handlePackagingTask 处理编译中任务
func (tm *TaskManager) handlePackagingTask(task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	status, err := client.GetBuildStatusContext(context.Background(), task.CiJobName, task.CiBuildId)
	if err != nil {
		return fmt.Errorf("查询管线状态失败：%s", err)
	}

	switch status {
	case "RUNNING":
		// 不做任何变动
		return nil
	case "SUCCESS":
		return tm.handlePackagingSuccess(task, client)
	case "FAILURE", "ABORTED":
		return tm.finishLegacyTask(task, entity.StatusPackageFailed, "Jenkins 编译终态："+status)
	default:
		return tm.finishLegacyTask(task, entity.StatusPackageFailed, "Jenkins 返回无法识别的编译终态")
	}
}

// handlePackagingSuccess 处理编译成功状态
func (tm *TaskManager) handlePackagingSuccess(task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(task, client)
	}

	// 更新状态为“编译完成”
	task.Status = entity.StatusPackaged
	_, err := db.Engine.ID(task.TaskId).Update(&task)
	return err
}

// triggerJenkinsBuild 触发 Jenkins 构建任务
func (tm *TaskManager) triggerJenkinsBuild(task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	jenkinsParam, err := tool.ToMapStringInterface(task.PipelineParam)
	if err != nil {
		return fmt.Errorf("转换参数失败：%s", err)
	}
	normalizeLegacyPipelineParameters(jenkinsParam)

	jobBuildId, _, err := client.CreateBuildTaskContext(context.Background(), task.CdJobName, jenkinsParam)
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
func (tm *TaskManager) handlePackagedTask(task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	// 检测自动部署的状态是否为1，如果是1则触发自动部署，开始继续执行，如果不为1则什么都不做
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(task, client)
	}
	return nil
}

// handleDeployingTask 处理部署中任务
func (tm *TaskManager) handleDeployingTask(task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	status, err := client.GetBuildStatusContext(context.Background(), task.CdJobName, task.CdBuildId)
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
		return tm.finishLegacyTask(task, entity.StatusDeployFailed, "Jenkins 部署终态："+status)
	default:
		return tm.finishLegacyTask(task, entity.StatusDeployFailed, "Jenkins 返回无法识别的部署终态")
	}
}

func validateLegacyTaskAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("旧版任务缺少 Jenkins 实例绑定，无法安全续跑")
	}
	if _, err := jenkins.NormalizeAddress(address); err != nil {
		return fmt.Errorf("旧版任务的 Jenkins 实例绑定无效：%w", err)
	}
	return nil
}

func normalizedLegacyTaskAddress(address string) string {
	normalized, _ := jenkins.NormalizeAddress(address)
	return normalized
}

func (tm *TaskManager) failUnsafeLegacyTask(task entity.TaskRecord, cause error) error {
	status := legacyUnsafeFailureStatus(task.Status)
	message := "旧版任务已安全终止：" + cause.Error()
	return tm.finishLegacyTask(task, status, message)
}

func legacyUnsafeFailureStatus(current string) string {
	if current == entity.StatusPackaged || current == entity.StatusDeploying {
		return entity.StatusDeployFailed
	}
	return entity.StatusPackageFailed
}

func (tm *TaskManager) finishLegacyTask(task entity.TaskRecord, status, message string) error {
	updated, err := db.Engine.Table(new(entity.TaskRecord)).
		Where("task_id = ? AND status = ? AND engine_version < ? AND deleted_at IS NULL", task.TaskId, task.Status, 2).
		Update(map[string]any{"status": status, "message": message, "updated_at": time.Now()})
	if err != nil {
		return err
	}
	if updated > 0 {
		slog.Warn("旧版任务进入失败终态", "task_id", task.TaskId, "status", status, "message", message)
	}
	return nil
}
