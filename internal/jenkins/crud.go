package jenkins

import (
	"context"
	"errors"
	"log/slog"
)

type Job struct {
	ID   string
	Name string
	// 其他字段...
}

type Nodes []Node

type Node struct {
	NodeName   string `json:"node_name"`
	NodeStatus string `json:"node_status"`
}

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

// CreateJob 创建一个新的 Jenkins Job
func CreateJob(job Job) error {
	// 实现创建逻辑
	return nil
}

// GetJob 获取 Jenkins Job
func GetJob(id string) (Job, error) {
	// 实现获取逻辑
	return Job{}, nil
}

// UpdateJob 更新 Jenkins Job
func UpdateJob(job Job) error {
	// 实现更新逻辑
	return nil
}

// DeleteJob 删除 Jenkins Job
func DeleteJob(id string) error {
	// 实现删除逻辑
	return nil
}
