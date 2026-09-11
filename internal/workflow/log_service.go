package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxLogCursorBytes = 256
	MaxLogChunkBytes  = 256 * 1024
)

// LogService binds a public task/step identity to the opaque execution
// reference stored in its v2 snapshot or selected attempt. Callers can provide
// only an attempt and cursor; provider addresses never cross this API.
type LogService struct {
	store    LogSourceStore
	registry *Registry
}

type LogSession struct {
	taskID            int
	stepKey           string
	externalReference json.RawMessage
	reader            LogReader
}

func NewLogService(store LogSourceStore, registry *Registry) *LogService {
	return &LogService{store: store, registry: registry}
}

func (s *LogService) Open(ctx context.Context, taskID int, stepKey string) (*LogSession, error) {
	return s.OpenAttempt(ctx, taskID, stepKey, 0)
}

func (s *LogService) OpenAttempt(ctx context.Context, taskID int, stepKey string, attempt int) (*LogSession, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return nil, errors.New("日志服务未初始化")
	}
	if taskID <= 0 || !stepKeyPattern.MatchString(stepKey) {
		return nil, fmt.Errorf("task_id 或 step_key 无效")
	}
	var source TaskStepLogSource
	var err error
	if attempt > 0 {
		store, ok := s.store.(interface {
			GetAttemptLogSource(context.Context, int, string, int) (TaskStepLogSource, error)
		})
		if !ok {
			return nil, ErrNotFound
		}
		source, err = store.GetAttemptLogSource(ctx, taskID, stepKey, attempt)
	} else {
		source, err = s.store.GetTaskStepLogSource(ctx, taskID, stepKey)
	}
	if err != nil {
		return nil, err
	}
	// The exact pair is part of the store contract. Verify it again before an
	// executor sees the identity so a faulty store cannot redirect a request.
	if source.TaskID != taskID || source.StepKey != stepKey {
		return nil, fmt.Errorf("任务步骤快照身份不一致: %w", ErrNotFound)
	}
	reader, ok := s.registry.LogReader(source.Uses)
	if !ok {
		if _, registered := s.registry.Get(source.Uses); registered {
			return nil, ErrLogsUnsupported
		}
		return nil, fmt.Errorf("执行器未注册：%s: %w", source.Uses, ErrExecutorUnavailable)
	}
	if !hasJSONValue(source.ExternalReference) {
		return nil, ErrLogsNotReady
	}
	return &LogSession{
		taskID: taskID, stepKey: stepKey, reader: reader,
		externalReference: append(json.RawMessage(nil), source.ExternalReference...),
	}, nil
}

func (s *LogSession) Read(ctx context.Context, cursor string) (LogChunk, error) {
	if s == nil || s.reader == nil {
		return LogChunk{}, errors.New("日志会话未初始化")
	}
	if err := ValidateLogCursor(cursor); err != nil {
		return LogChunk{}, err
	}
	chunk, err := s.reader.ReadLogs(ctx, LogRequest{
		TaskID: s.taskID, StepKey: s.stepKey,
		ExternalReference: append(json.RawMessage(nil), s.externalReference...),
		Cursor:            cursor,
	})
	if err != nil {
		return LogChunk{}, err
	}
	if chunk.Cursor == "" {
		chunk.Cursor = cursor
	}
	if err := validateLogChunk(cursor, chunk); err != nil {
		return LogChunk{}, err
	}
	return chunk, nil
}

func ValidateLogCursor(cursor string) error {
	if len(cursor) > MaxLogCursorBytes || !utf8.ValidString(cursor) ||
		strings.ContainsAny(cursor, "\x00\r\n") {
		return ErrInvalidLogCursor
	}
	return nil
}

func validateLogChunk(previous string, chunk LogChunk) error {
	if len(chunk.Content) > MaxLogChunkBytes || !utf8.ValidString(chunk.Content) {
		return ErrInvalidLogChunk
	}
	if err := ValidateLogCursor(chunk.Cursor); err != nil {
		return ErrInvalidLogChunk
	}
	if chunk.Content == "" && chunk.Cursor != previous {
		return ErrInvalidLogChunk
	}
	if chunk.Content != "" && (chunk.Cursor == "" || chunk.Cursor == previous) {
		return ErrInvalidLogChunk
	}
	if bytes.IndexByte([]byte(chunk.Content), 0) >= 0 {
		return ErrInvalidLogChunk
	}
	return nil
}
