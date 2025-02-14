package jenkins

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"
)

type Nodes []Node

type Node struct {
	NodeName   string `json:"node_name"`
	NodeStatus string `json:"node_status"`
}

// GetJenkinsNodeStatus	获取jenkins中node的状态信息
func GetJenkinsNodeStatus() (*Nodes, error) {
	ctx := context.Background()
	if Jenkins == nil {
		return nil, errors.New("jenkins not initialized")
	}
	nodes, err := Jenkins.GetAllNodes(ctx)
	if err != nil {
		slog.Error("Failed to get all nodes", slog.Any("error", err))
		return nil, err
	}
	var nodeInfo Nodes
	for _, node := range nodes {
		poll, err := node.Poll(ctx)
		if err != nil {
			slog.Error("Failed to poll node", slog.Any("error", err))
			return nil, err
		}
		slog.Debug("获取node信息，poll的值为：", slog.Any("poll", poll))
		slog.Debug("当前node为：", slog.Any("node", node.Base))
		nodeStatus, err := node.IsOnline(ctx)
		if err != nil {
			slog.Error("Failed to get node status", slog.Any("error", err))
			return nil, err
		}
		if nodeStatus {
			slog.Debug("当前node在线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "在线"})
		} else {
			slog.Warn("当前node离线", slog.Any("node", node))
			nodeInfo = append(nodeInfo, Node{NodeName: node.Base, NodeStatus: "离线"})
			continue
		}
	}
	return &nodeInfo, nil
}

// GetJenkinsBuildLog 获取jenkins构建日志
func GetJenkinsBuildLog(jobName string, buildId int64) (string, error) {
	ctx := context.Background()
	if Jenkins == nil {
		return "", errors.New("jenkins not initialized")
	}
	job, err := Jenkins.GetJob(ctx, jobName)
	if err != nil {
		slog.Error("获取 Job 失败", slog.Any("error", err))
		return "", err
	}
	build, err := job.GetBuild(ctx, buildId)
	if err != nil {
		slog.Error("获取 Job 构建失败", slog.Any("error", err))
		return "", err
	}
	log := build.GetConsoleOutput(ctx)
	return log, nil
}

// StreamJenkinsBuildLog	持续获取jenkins的构建日志
// jobName: 执行的Job名称
// buildId：执行的任务id
func StreamJenkinsBuildLog(jobName string, buildId int64, logChan chan<- string, errChan chan<- error) bool {
	ctx := context.Background()
	if Jenkins == nil {
		errChan <- errors.New("jenkins not initialized")
		return false
	}
	job, err := Jenkins.GetJob(ctx, jobName)
	if err != nil {
		errChan <- err
		return false
	}
	build, err := job.GetBuild(ctx, buildId)
	if err != nil {
		errChan <- err
		return false
	}
	//build.IsRunning(ctx)

	var lastLog string
	for {
		log := build.GetConsoleOutput(ctx) // 这里持续获取增量日志的逻辑
		if log != lastLog && len(log) > len(lastLog) {
			logChan <- log[len(lastLog):]
			lastLog = log
		}
		if !build.IsRunning(ctx) {
			return true
		}
		time.Sleep(1 * time.Second) // 控制获取频率
	}
}

// CreateBuildTask 创建一个新的 Jenkins 构建任务
// 传入：管线名称、入参
// 返回：构建id、管线名称、错误信息
func CreateBuildTask(jobName string, params map[string]string) (int64, string, error) {
	ctx := context.Background()
	// 实现创建逻辑
	if Jenkins == nil {
		return 0, "", errors.New("jenkins not initialized")
	}
	queueId, err := Jenkins.BuildJob(ctx, jobName, params)
	if err != nil {
		slog.Error("任务构建失败", slog.Any("error", err))
		return 0, "", err
	}
	if queueId == 0 {
		return 0, "", errors.New("作业任务已在队列中。 如需优化,请将该job的静默期调整为0秒。默认为5秒,需设置为0秒即可解决。配置路径：jobName-->configure-->Quiet period  静默功能说明：https://www.jenkins.io/blog/2010/08/11/quiet-period-feature/")
	}

	// 这段代码是一个循环，检查任务的 Executable.Number 是否为 0。根据 Jenkins 的 API，任务在队列中会有一个大约 4.7 秒的静默期。
	// 在此期间，任务的构建编号可能尚未分配。循环中每隔 1 秒调用一次 Poll 方法来更新任务状态。如果在此过程中发生错误，返回 nil 和错误信息。
	buildInfo, err := Jenkins.GetBuildFromQueueID(ctx, queueId)
	if err != nil {
		return 0, "", err
	}
	// job任务中的id
	jobBuildIdStr := buildInfo.Raw.ID

	jobBuildId, err := strconv.ParseInt(jobBuildIdStr, 10, 64)
	if err != nil {
		return 0, "", err
	}

	return jobBuildId, jobName, nil
}

func BuildTask() error {
	if Jenkins == nil {
		return errors.New("jenkins not initialized")
	}

	return nil
}

// GetBuildStatus 获取 Jenkins构建结果
// 传入：管线名称、构建id
// 返回：构建状态（RUNNING、SUCCESS、FAILURE、ABORTED）
func GetBuildStatus(jobName string, buildId int64) (string, error) {
	if Jenkins == nil {
		return "", errors.New("jenkins not initialized")
	}

	ctx := context.Background()

	// 获取job对象
	job, err := Jenkins.GetJob(ctx, jobName)
	if err != nil {
		slog.Error("获取 Job 失败", slog.Any("error", err))
		return "", err
	}
	// 获取具体构建实例
	build, err := job.GetBuild(ctx, buildId)
	if err != nil {
		slog.Error("获取 Job 构建失败", slog.Any("error", err))
		return "", err
	}

	// 检查构建是否仍在运行
	isRunning := build.IsRunning(ctx)
	if isRunning {
		return "RUNNING", nil
	}

	// 获取最终构建结果
	result := build.GetResult()
	switch result {
	case "SUCCESS":
		return "SUCCESS", nil
	case "FAILURE":
		return "FAILURE", nil
	case "ABORTED":
		return "ABORTED", nil
	default:
		return "ABNORMAL", nil
	}
}

//// GetJob 获取 Jenkins Job
//func GetJob(id string) (Job, error) {
//	// 实现获取逻辑
//	return Job{}, nil
//}

//// UpdateJob 更新 Jenkins Job
//func UpdateJob(job Job) error {
//	// 实现更新逻辑
//	return nil
//}

// DeleteJob 删除 Jenkins
func DeleteJob(id string) error {
	// 实现删除逻辑
	return nil
}
