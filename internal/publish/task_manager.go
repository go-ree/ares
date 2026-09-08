package publish

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/integration"
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/tool"
)

const (
	legacyTaskWorkerLimit      = 8
	legacyLeaderHealthInterval = time.Second
	legacyLeaderProbeTimeout   = time.Second
	legacyLeaderReleaseTimeout = 2 * time.Second
	legacyLeaderLockNamePrefix = "ares_v1_drain_"
)

type legacyLeadership struct {
	ctx     context.Context
	release func()
}

type legacyConnectionContextKey struct{}

type legacyLeadershipAcquirer func(context.Context) (legacyLeadership, bool, error)
type legacyRoundRunner func(context.Context) error

// TaskManager drains v1 tasks only. New v2 work is owned by workflow.Worker;
// keeping both state machines behind the old cron callback would allow every
// API replica to reconcile the same running v2 step.
type TaskManager struct {
	acquireLeadership    legacyLeadershipAcquirer
	runLeaderRound       legacyRoundRunner
	ensureJenkinsCurrent func(context.Context) error
}

// NewTaskManager creates the compatibility task manager. Dependencies are
// captured as functions so RunOnce can be tested without a live MySQL server.
func NewTaskManager() *TaskManager {
	tm := &TaskManager{}
	tm.acquireLeadership = tm.acquireMySQLLeadership
	tm.runLeaderRound = tm.updateLegacyTasks
	tm.ensureJenkinsCurrent = integration.EnsureJenkinsCurrent
	return tm
}

// RunOnce attempts one legacy drain round. The named lock is acquired before
// querying task_record and remains held until every Jenkins call and guarded
// database update in this round has returned.
func (tm *TaskManager) RunOnce(ctx context.Context) error {
	if ctx == nil {
		return errors.New("legacy task manager context is nil")
	}
	if tm == nil || tm.acquireLeadership == nil || tm.runLeaderRound == nil {
		return errors.New("legacy task manager is not initialized")
	}
	leadership, acquired, err := tm.acquireLeadership(ctx)
	if err != nil {
		return fmt.Errorf("acquire legacy task leader: %w", err)
	}
	if !acquired {
		return nil
	}
	if leadership.ctx == nil || leadership.release == nil {
		return errors.New("legacy task leader returned an invalid handle")
	}
	defer leadership.release()
	if err := leadership.ctx.Err(); err != nil {
		return err
	}
	return tm.runLeaderRound(leadership.ctx)
}

func (tm *TaskManager) acquireMySQLLeadership(ctx context.Context) (legacyLeadership, bool, error) {
	if db.Engine == nil || db.Engine.DB() == nil || db.Engine.DB().DB == nil {
		return legacyLeadership{}, false, errors.New("database is not initialized")
	}
	conn, err := db.Engine.DB().DB.Conn(ctx)
	if err != nil {
		return legacyLeadership{}, false, err
	}
	closeConnection := true
	defer func() {
		if closeConnection {
			_ = conn.Close()
		}
	}()

	var databaseName sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		return legacyLeadership{}, false, err
	}
	if !databaseName.Valid || strings.TrimSpace(databaseName.String) == "" {
		return legacyLeadership{}, false, errors.New("database connection has no selected schema")
	}
	lockName := legacyLeaderLockName(databaseName.String)
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
		return legacyLeadership{}, false, err
	}
	if !acquired.Valid {
		return legacyLeadership{}, false, errors.New("database returned an invalid legacy leader lock result")
	}
	if acquired.Int64 != 1 {
		return legacyLeadership{}, false, nil
	}

	leaderCtx, cancelLeader := context.WithCancel(ctx)
	leaderCtx = context.WithValue(leaderCtx, legacyConnectionContextKey{}, conn)
	monitorDone := make(chan struct{})
	go monitorLegacyLeadership(leaderCtx, conn, lockName, legacyLeaderHealthInterval, cancelLeader, monitorDone)

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			cancelLeader()
			<-monitorDone
			releaseCtx, cancelRelease := context.WithTimeout(context.Background(), legacyLeaderReleaseTimeout)
			defer cancelRelease()
			var released sql.NullInt64
			if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
				slog.Warn("释放旧版任务 leader 锁失败", "error_type", fmt.Sprintf("%T", err))
				// sql.Conn.Close normally returns the physical connection to the
				// pool. A connection with an uncertain user-level lock must instead
				// be discarded so a pooled session can never retain leadership.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			if err := conn.Close(); err != nil {
				slog.Warn("关闭旧版任务 leader 连接失败", "error_type", fmt.Sprintf("%T", err))
			}
		})
	}
	closeConnection = false
	return legacyLeadership{ctx: leaderCtx, release: release}, true, nil
}

func legacyLeaderLockName(databaseName string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(databaseName)))
	return legacyLeaderLockNamePrefix + hex.EncodeToString(digest[:16])
}

func legacyConnection(ctx context.Context) (*sql.Conn, error) {
	if ctx == nil {
		return nil, errors.New("legacy leader context is nil")
	}
	conn, ok := ctx.Value(legacyConnectionContextKey{}).(*sql.Conn)
	if !ok || conn == nil {
		return nil, errors.New("legacy leader connection is unavailable")
	}
	return conn, nil
}

// monitorLegacyLeadership detects a dead/replaced dedicated connection while
// a Jenkins request is in flight. Correctness still comes from the MySQL named
// lock; this bounded probe shortens the interval in which an old round has not
// yet observed that MySQL already released its lock.
func monitorLegacyLeadership(
	ctx context.Context,
	conn *sql.Conn,
	lockName string,
	interval time.Duration,
	cancelLeadership context.CancelFunc,
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probeCtx, cancelProbe := context.WithTimeout(ctx, legacyLeaderProbeTimeout)
			var stillLeader sql.NullBool
			err := conn.QueryRowContext(probeCtx,
				"SELECT IS_USED_LOCK(?) = CONNECTION_ID()", lockName).Scan(&stillLeader)
			cancelProbe()
			if err != nil || !stillLeader.Valid || !stillLeader.Bool {
				cancelLeadership()
				return
			}
		}
	}
}

func (tm *TaskManager) updateLegacyTasks(ctx context.Context) error {
	tasks, err := tm.fetchTasks(ctx)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}

	workerCount := len(tasks)
	if workerCount > legacyTaskWorkerLimit {
		workerCount = legacyTaskWorkerLimit
	}
	jobs := make(chan entity.TaskRecord)
	results := make(chan error, len(tasks))
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-jobs:
					if !ok {
						return
					}
					if err := tm.processLegacyTask(ctx, task); err != nil {
						results <- fmt.Errorf("task %d: %w", task.TaskId, err)
					}
				}
			}
		}()
	}

sendLoop:
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- task:
		}
	}
	close(jobs)
	workers.Wait()
	close(results)

	errorsFound := make([]error, 0, len(results)+1)
	for err := range results {
		errorsFound = append(errorsFound, err)
	}
	if err := ctx.Err(); err != nil {
		errorsFound = append(errorsFound, err)
	}
	return errors.Join(errorsFound...)
}

func (tm *TaskManager) processLegacyTask(ctx context.Context, task entity.TaskRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateLegacyTaskAddress(task.JenkinsAddress); err != nil {
		if failErr := tm.failUnsafeLegacyTask(ctx, task, err); failErr != nil {
			return fmt.Errorf("终止无法安全续跑的旧版任务: %w", failErr)
		}
		return nil
	}
	if err := validateLegacyTaskExecution(task); err != nil {
		if failErr := tm.failUnsafeLegacyTask(ctx, task, err); failErr != nil {
			return fmt.Errorf("终止缺少执行快照的旧版任务: %w", failErr)
		}
		return nil
	}
	if tm.ensureJenkinsCurrent == nil {
		return errors.New("legacy Jenkins settings synchronizer is unavailable")
	}
	// The named lock serializes v1 drain rounds, but it does not synchronize
	// the process-local Jenkins runtime with settings committed by another Ares
	// replica. Refresh immediately before acquiring a runtime snapshot so a DB
	// outage or unapplied revision fails closed instead of using stale access.
	if err := tm.ensureJenkinsCurrent(ctx); err != nil {
		return fmt.Errorf("refresh legacy Jenkins settings: %w", err)
	}
	snapshot, releaseSnapshot := jenkins.AcquireForOperation()
	if snapshot == nil {
		releaseSnapshot()
		return nil
	}
	defer releaseSnapshot()
	if snapshot.Address() != normalizedLegacyTaskAddress(task.JenkinsAddress) {
		return tm.failUnsafeLegacyTask(ctx, task, fmt.Errorf("任务绑定的 Jenkins 实例与当前设置不一致"))
	}
	switch task.Status {
	case entity.StatusPackaging:
		return tm.handlePackagingTask(ctx, task, snapshot)
	case entity.StatusPackaged:
		return tm.handlePackagedTask(ctx, task, snapshot)
	case entity.StatusDeploying:
		return tm.handleDeployingTask(ctx, task, snapshot)
	default:
		return nil
	}
}

// fetchTasks queries only v1 rows. A corrupt v1 task can never enter the v2
// lease worker, and a v2 task can never enter this Jenkins compatibility loop.
func (tm *TaskManager) fetchTasks(ctx context.Context) ([]entity.TaskRecord, error) {
	conn, err := legacyConnection(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT task_id, app_name, branch, env, publisher,
		ci_build_id, cd_build_id, pipeline_param, status, ci_job_name, cd_job_name,
		jenkins_address, auto_deploy, products
		FROM task_record
		WHERE engine_version = ? AND deleted_at IS NULL AND (
			status IN (?, ?) OR (status = ? AND auto_deploy = 1)
		)
		ORDER BY task_id`, 1, entity.StatusPackaging, entity.StatusDeploying, entity.StatusPackaged)
	if err != nil {
		return nil, fmt.Errorf("查询旧版任务列表: %w", err)
	}
	defer rows.Close()
	tasks := make([]entity.TaskRecord, 0)
	for rows.Next() {
		var task entity.TaskRecord
		var appName, branch, env, publisher sql.NullString
		var status, ciJobName, cdJobName, jenkinsAddress, products sql.NullString
		var ciBuildID, cdBuildID, autoDeploy sql.NullInt64
		var pipelineParam []byte
		if err := rows.Scan(
			&task.TaskId, &appName, &branch, &env, &publisher,
			&ciBuildID, &cdBuildID, &pipelineParam, &status,
			&ciJobName, &cdJobName, &jenkinsAddress, &autoDeploy, &products,
		); err != nil {
			return nil, fmt.Errorf("读取旧版任务: %w", err)
		}
		task.AppName = appName.String
		task.Branch = branch.String
		task.Env = env.String
		task.Publisher = publisher.String
		task.CiBuildId = ciBuildID.Int64
		task.CdBuildId = cdBuildID.Int64
		task.PipelineParam = append(task.PipelineParam[:0], pipelineParam...)
		task.Status = status.String
		task.CiJobName = ciJobName.String
		task.CdJobName = cdJobName.String
		task.JenkinsAddress = jenkinsAddress.String
		task.AutoDeploy = int(autoDeploy.Int64)
		task.Products = products.String
		task.EngineVersion = 1
		normalizeTaskRecordNullableText(&task)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取旧版任务列表: %w", err)
	}
	slog.Info("监听旧版构建任务列表状态", "count", len(tasks), "engine_version", 1)
	return tasks, nil
}

func (tm *TaskManager) handlePackagingTask(ctx context.Context, task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	status, err := client.GetBuildStatusContext(ctx, task.CiJobName, task.CiBuildId)
	if err != nil {
		return fmt.Errorf("查询管线状态失败: %w", err)
	}

	switch status {
	case "RUNNING":
		return nil
	case "SUCCESS":
		return tm.handlePackagingSuccess(ctx, task, client)
	case "FAILURE", "ABORTED":
		return tm.finishLegacyTask(ctx, task, entity.StatusPackageFailed, "Jenkins 编译终态："+status)
	default:
		return tm.finishLegacyTask(ctx, task, entity.StatusPackageFailed, "Jenkins 返回无法识别的编译终态")
	}
}

func (tm *TaskManager) handlePackagingSuccess(ctx context.Context, task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(ctx, task, client)
	}
	return updateLegacyTask(ctx, task, "status = ?, updated_at = ?", entity.StatusPackaged, time.Now())
}

func (tm *TaskManager) triggerJenkinsBuild(ctx context.Context, task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	jenkinsParam, err := tool.ToMapStringInterface(task.PipelineParam)
	if err != nil {
		return fmt.Errorf("转换参数失败: %w", err)
	}
	normalizeLegacyPipelineParameters(jenkinsParam)

	jobBuildID, _, err := client.CreateBuildTaskContext(ctx, task.CdJobName, jenkinsParam)
	if err != nil {
		return fmt.Errorf("创建构建任务失败: %w", err)
	}
	err = updateLegacyTask(ctx, task, "status = ?, cd_build_id = ?, updated_at = ?",
		entity.StatusDeploying, jobBuildID, time.Now())
	if err == nil {
		slog.Info("旧版任务自动部署中",
			"task_id", task.TaskId,
			"app_name", task.AppName,
			"env", task.Env,
			"publisher", task.Publisher,
			"branch", task.Branch,
			"image", task.Products)
	}
	return err
}

func (tm *TaskManager) handlePackagedTask(ctx context.Context, task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	if task.AutoDeploy == 1 {
		return tm.triggerJenkinsBuild(ctx, task, client)
	}
	return nil
}

func (tm *TaskManager) handleDeployingTask(ctx context.Context, task entity.TaskRecord, client *jenkins.ClientSnapshot) error {
	status, err := client.GetBuildStatusContext(ctx, task.CdJobName, task.CdBuildId)
	if err != nil {
		return fmt.Errorf("查询部署状态失败: %w", err)
	}

	switch status {
	case "RUNNING":
		return nil
	case "SUCCESS":
		return updateLegacyTask(ctx, task, "status = ?, updated_at = ?", entity.StatusDeployed, time.Now())
	case "FAILURE", "ABORTED":
		return tm.finishLegacyTask(ctx, task, entity.StatusDeployFailed, "Jenkins 部署终态："+status)
	default:
		return tm.finishLegacyTask(ctx, task, entity.StatusDeployFailed, "Jenkins 返回无法识别的部署终态")
	}
}

func validateLegacyTaskAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("旧版任务缺少 Jenkins 实例绑定，无法安全续跑")
	}
	if _, err := jenkins.NormalizeAddress(address); err != nil {
		return fmt.Errorf("旧版任务的 Jenkins 实例绑定无效: %w", err)
	}
	return nil
}

func validateLegacyTaskExecution(task entity.TaskRecord) error {
	if task.AutoDeploy != 0 && task.AutoDeploy != 1 {
		return fmt.Errorf("旧版任务的自动部署标记无效")
	}
	switch task.Status {
	case entity.StatusPackaging:
		if strings.TrimSpace(task.CiJobName) == "" || task.CiBuildId <= 0 {
			return fmt.Errorf("旧版编译任务缺少 Jenkins Job 或 Build ID")
		}
	case entity.StatusPackaged:
		if task.AutoDeploy == 1 && strings.TrimSpace(task.CdJobName) == "" {
			return fmt.Errorf("旧版自动部署任务缺少 Jenkins Job")
		}
	case entity.StatusDeploying:
		if strings.TrimSpace(task.CdJobName) == "" || task.CdBuildId <= 0 {
			return fmt.Errorf("旧版部署任务缺少 Jenkins Job 或 Build ID")
		}
	}
	return nil
}

func normalizedLegacyTaskAddress(address string) string {
	normalized, _ := jenkins.NormalizeAddress(address)
	return normalized
}

func (tm *TaskManager) failUnsafeLegacyTask(ctx context.Context, task entity.TaskRecord, cause error) error {
	status := legacyUnsafeFailureStatus(task.Status)
	message := "旧版任务已安全终止：" + cause.Error()
	return tm.finishLegacyTask(ctx, task, status, message)
}

func legacyUnsafeFailureStatus(current string) string {
	if current == entity.StatusPackaged || current == entity.StatusDeploying {
		return entity.StatusDeployFailed
	}
	return entity.StatusPackageFailed
}

func (tm *TaskManager) finishLegacyTask(ctx context.Context, task entity.TaskRecord, status, message string) error {
	conn, err := legacyConnection(ctx)
	if err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx, `UPDATE task_record
		SET status = ?, message = ?, updated_at = ?
		WHERE task_id = ? AND status = ? AND engine_version = 1 AND deleted_at IS NULL`,
		status, message, time.Now(), task.TaskId, task.Status)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated > 0 {
		slog.Warn("旧版任务进入失败终态", "task_id", task.TaskId, "status", status, "message", message)
	}
	return nil
}

func updateLegacyTask(ctx context.Context, task entity.TaskRecord, assignments string, values ...any) error {
	conn, err := legacyConnection(ctx)
	if err != nil {
		return err
	}
	arguments := append(values, task.TaskId, task.Status)
	result, err := conn.ExecContext(ctx, `UPDATE task_record SET `+assignments+
		` WHERE task_id = ? AND status = ? AND engine_version = 1 AND deleted_at IS NULL`, arguments...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("legacy task state changed before the guarded update")
	}
	return nil
}
