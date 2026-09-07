package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/entity"
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

func TestDecodeStoredWorkflowSpecVerifiesChecksum(t *testing.T) {
	specJSON := json.RawMessage(`{"schema_version":1,"name":"demo","steps":[]}`)
	canonical, err := canonicaljson.Canonicalize(specJSON)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	valid := entity.ReleaseWorkflowVersion{
		VersionID: 7,
		Spec:      specJSON,
		Checksum:  hex.EncodeToString(digest[:]),
	}
	if _, err := decodeStoredWorkflowSpec(valid); err != nil {
		t.Fatalf("valid stored workflow rejected: %v", err)
	}
	rewritten := valid
	rewritten.Spec = json.RawMessage(` { "steps": [], "name": "demo", "schema_version": 1 } `)
	if _, err := decodeStoredWorkflowSpec(rewritten); err != nil {
		t.Fatalf("semantically identical MySQL-style JSON rewrite rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*entity.ReleaseWorkflowVersion)
	}{
		{name: "changed spec", mutate: func(version *entity.ReleaseWorkflowVersion) {
			version.Spec = json.RawMessage(`{"schema_version":1,"name":"tampered","steps":[]}`)
		}},
		{name: "changed checksum", mutate: func(version *entity.ReleaseWorkflowVersion) {
			version.Checksum = strings.Repeat("0", 64)
		}},
		{name: "uppercase checksum", mutate: func(version *entity.ReleaseWorkflowVersion) {
			version.Checksum = strings.ToUpper(version.Checksum)
		}},
		{name: "malformed checksum", mutate: func(version *entity.ReleaseWorkflowVersion) {
			version.Checksum = "not-a-checksum"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			version := valid
			test.mutate(&version)
			if _, err := decodeStoredWorkflowSpec(version); err == nil || !strings.Contains(err.Error(), "完整性校验失败") {
				t.Fatalf("corrupted workflow error = %v", err)
			}
		})
	}
}
