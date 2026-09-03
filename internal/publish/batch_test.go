package publish

import (
	"context"
	"strings"
	"testing"
)

func TestCreateBatchPublishRejectsEmptyBatch(t *testing.T) {
	result, err := NewPublishManager().CreateBatchPublish(context.Background(), &CreateBatchPublishRequest{})
	if err == nil || !strings.Contains(err.Error(), "至少需要 1 个") {
		t.Fatalf("CreateBatchPublish() result=%#v error=%v, want empty-batch error", result, err)
	}
}
