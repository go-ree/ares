package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

func NewRegistry() *Registry {
	return &Registry{executors: make(map[string]Executor)}
}

func (r *Registry) Register(executor Executor) error {
	if executor == nil {
		return fmt.Errorf("执行器不能为空")
	}
	descriptor := executor.Descriptor()
	descriptor.Uses = strings.TrimSpace(descriptor.Uses)
	if !usesPattern.MatchString(descriptor.Uses) {
		return fmt.Errorf("执行器 uses 格式无效：%s", descriptor.Uses)
	}
	if strings.TrimSpace(descriptor.Name) == "" {
		return fmt.Errorf("执行器 %s 的 name 不能为空", descriptor.Uses)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[descriptor.Uses]; exists {
		return fmt.Errorf("执行器已注册：%s", descriptor.Uses)
	}
	r.executors[descriptor.Uses] = executor
	return nil
}

func (r *Registry) Get(uses string) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[uses]
	return executor, ok
}

func (r *Registry) Descriptors(ctx context.Context) []Descriptor {
	r.mu.RLock()
	executors := make([]Executor, 0, len(r.executors))
	for _, executor := range r.executors {
		executors = append(executors, executor)
	}
	r.mu.RUnlock()

	descriptors := make([]Descriptor, 0, len(executors))
	for _, executor := range executors {
		descriptor := executor.Descriptor()
		descriptor.Available = true
		descriptor.UnavailableReason = ""
		if checker, ok := executor.(AvailabilityChecker); ok {
			if err := checker.Available(ctx); err != nil {
				descriptor.Available = false
				descriptor.UnavailableReason = err.Error()
			}
		}
		descriptors = append(descriptors, descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Uses < descriptors[j].Uses })
	return descriptors
}
