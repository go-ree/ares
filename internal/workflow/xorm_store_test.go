package workflow

import (
	"encoding/json"
	"github.com/go-ree/ares/internal/entity"
	"strings"
	"testing"
)

func TestTaskStepRecordDoesNotSerializePrivatePayloads(t *testing.T) {
	record := entity.TaskStepRecord{
		StepRecordID: 1,
		Config:       json.RawMessage(`{"clientSecret":"hidden"}`),
		ExternalRef:  json.RawMessage(`{"queue_id":42}`),
		Output:       json.RawMessage(`{"signed_url":"hidden"}`),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateValue := range []string{"clientSecret", "queue_id", "signed_url", "config", "external_ref", "output"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("private step payload %q leaked through JSON: %s", privateValue, encoded)
		}
	}
}

func TestNullableJSON(t *testing.T) {
	for _, value := range []json.RawMessage{nil, {}, json.RawMessage(" "), json.RawMessage("null")} {
		if got := nullableJSON(value); got != nil {
			t.Errorf("nullableJSON(%q) = %#v, want nil", value, got)
		}
	}
	value := json.RawMessage(`{"ok":true}`)
	got, ok := nullableJSON(value).(json.RawMessage)
	if !ok || string(got) != string(value) {
		t.Fatalf("nullableJSON(%q) = %#v", value, got)
	}
}

func TestTruncateRunesKeepsUTF8Valid(t *testing.T) {
	value := strings.Repeat("发", 1001)
	got := truncateRunes(value, 1000)
	if len([]rune(got)) != 1000 || !strings.HasSuffix(got, "发") {
		t.Fatalf("truncateRunes() returned %d runes", len([]rune(got)))
	}
	if got := truncateRunes("发布成功", 255); got != "发布成功" {
		t.Fatalf("short value changed: %q", got)
	}
}
