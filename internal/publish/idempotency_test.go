package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
)

func TestParseIdempotencyKeyStrictTokenContract(t *testing.T) {
	valid := []string{
		"1234567890abcdef",
		"8e03978e-40d5-43e8-bc93-6894a57f9324",
		"A" + strings.Repeat("z", 127),
	}
	for _, key := range valid {
		if _, err := ParseIdempotencyKey([]string{key}); err != nil {
			t.Errorf("ParseIdempotencyKey(%q) error = %v", key, err)
		}
	}
	invalid := [][]string{
		nil,
		{"1234567890abcdef", "fedcba0987654321"},
		{"short"},
		{" 1234567890abcdef"},
		{"1234567890abcdef "},
		{"1234567890abcdef,another"},
		{`"1234567890abcdef"`},
		{"1234567890abcde%"},
		{"1234567890abcdé"},
		{"A" + strings.Repeat("z", 128)},
	}
	for _, values := range invalid {
		if _, err := ParseIdempotencyKey(values); err == nil {
			t.Errorf("ParseIdempotencyKey(%q) succeeded", values)
		}
	}

	lower, _ := ParseIdempotencyKey([]string{"abcdef0123456789"})
	upper, _ := ParseIdempotencyKey([]string{"ABCDEF0123456789"})
	if lower.digest == upper.digest {
		t.Fatal("Idempotency-Key unexpectedly became case insensitive")
	}
}

func TestReleaseRequestDigestPreservesJSONNumberPrecision(t *testing.T) {
	command := func(inputs string) ReleaseCommand {
		normalized, err := normalizeReleaseCommand(ReleaseCommand{
			ConfigID: 7, Ref: "main", Inputs: json.RawMessage(inputs),
		})
		if err != nil {
			t.Fatal(err)
		}
		return normalized
	}

	first := command(`{"z":1.0,"large":9007199254740992}`)
	equivalent := command(` { "large" : 9007199254740992.0, "z" : 1e0 } `)
	different := command(`{"large":9007199254740993,"z":1}`)
	firstDigest, err := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{first})
	if err != nil {
		t.Fatal(err)
	}
	equivalentDigest, _ := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{equivalent})
	differentDigest, _ := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{different})
	if firstDigest != equivalentDigest {
		t.Fatal("equivalent JSON requests produced different digests")
	}
	if firstDigest == differentDigest {
		t.Fatal("adjacent integers beyond 2^53 produced the same digest")
	}
}

func TestReleaseRequestDigestIncludesPreconditionAndBatchOrder(t *testing.T) {
	versionOne, versionTwo := int64(1), int64(2)
	base, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 7, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	withOne := base
	withOne.ExpectedWorkflowVersionID = &versionOne
	withTwo := base
	withTwo.ExpectedWorkflowVersionID = &versionTwo
	baseDigest, _ := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{base})
	oneDigest, _ := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{withOne})
	twoDigest, _ := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{withTwo})
	if baseDigest == oneDigest || oneDigest == twoDigest {
		t.Fatal("workflow version precondition was omitted from request digest")
	}

	other, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 8, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	forward, _ := releaseRequestDigest(SemanticReleaseBatchCreate, []ReleaseCommand{base, other})
	reversed, _ := releaseRequestDigest(SemanticReleaseBatchCreate, []ReleaseCommand{other, base})
	if forward == reversed {
		t.Fatal("batch order was omitted from request digest")
	}
}

func TestNormalizeReleaseCommandRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	badInputs := []string{
		`null`, `[]`, `"value"`, `{"password":"secret"}`,
		strings.Repeat(" ", maxReleaseInputBytes) + `{}`,
	}
	for _, raw := range badInputs {
		if _, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 1, Ref: "main", Inputs: json.RawMessage(raw)}); err == nil {
			t.Errorf("unsafe inputs accepted: %.80q", raw)
		}
	}

	deep := `0`
	for index := 0; index < maxReleaseInputDepth+1; index++ {
		deep = `{"nested":` + deep + `}`
	}
	if _, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 1, Ref: "main", Inputs: json.RawMessage(deep)}); err == nil {
		t.Fatal("over-deep inputs accepted")
	}
	if _, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 1, Ref: "main\xff"}); err == nil {
		t.Fatal("invalid UTF-8 ref accepted")
	}
	if _, err := normalizeReleaseCommand(ReleaseCommand{ConfigID: 1, Ref: "main\u0085release"}); err == nil {
		t.Fatal("Unicode control character in ref accepted")
	}
	if _, err := normalizeReleaseCommand(ReleaseCommand{
		ConfigID: 1, Ref: "main", Inputs: json.RawMessage("{\"value\":\"\xff\"}"),
	}); err == nil {
		t.Fatal("invalid UTF-8 inputs accepted")
	}
}

func TestNormalizeReleaseCommandsRejectsDuplicateTargets(t *testing.T) {
	_, err := normalizeReleaseCommands([]ReleaseCommand{
		{ConfigID: 7, Ref: "main"},
		{ConfigID: 7, Ref: "release"},
	})
	if err == nil {
		t.Fatal("duplicate config_id in one batch was accepted")
	}
}

func TestLegacyResolverRejectsCanonicalInputBeforeStorage(t *testing.T) {
	service := &IdempotentReleaseService{}
	tests := []struct {
		name       string
		request    CreatePublishRequest
		wantCode   string
		wantStatus int
	}{
		{
			name: "invalid ref",
			request: CreatePublishRequest{
				AppName: "demo-api", Env: "dev", Branch: " main",
			},
			wantCode: ReleaseCodeInvalidRequest, wantStatus: http.StatusBadRequest,
		},
		{
			name: "oversized extra data",
			request: CreatePublishRequest{
				AppName: "demo-api", Env: "dev", Branch: "main",
				ExtraData: map[string]any{"value": strings.Repeat("a", maxReleaseInputBytes)},
			},
			wantCode: ReleaseCodeRequestTooLarge, wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "unencodable extra data",
			request: CreatePublishRequest{
				AppName: "demo-api", Env: "dev", Branch: "main",
				ExtraData: map[string]any{"value": make(chan int)},
			},
			wantCode: ReleaseCodeInvalidRequest, wantStatus: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ResolveLegacyCommand(context.Background(), test.request)
			var commandError *ReleaseCommandError
			if !errors.As(err, &commandError) || commandError.Code != test.wantCode ||
				commandError.HTTPStatus != test.wantStatus {
				t.Fatalf("ResolveLegacyCommand() error = %#v", err)
			}
		})
	}
}

func TestLegacyBatchResolverCanReportOnlyLocalErrorsWithoutStorage(t *testing.T) {
	service := &IdempotentReleaseService{}
	commands, itemErrors, err := service.ResolveLegacyCommands(context.Background(), []CreatePublishRequest{
		{AppName: "demo-api", Env: "dev", Branch: " main"},
		{AppName: " demo-api", Env: "dev", Branch: "main"},
	})
	if err != nil || len(commands) != 2 || len(itemErrors) != 2 || itemErrors[0] == nil || itemErrors[1] == nil {
		t.Fatalf("ResolveLegacyCommands() = commands:%d itemErrors:%#v err:%v", len(commands), itemErrors, err)
	}
}

func TestReleasePreflightRevalidatesStoredStepAgainstCurrentExecutor(t *testing.T) {
	service := NewIdempotentReleaseService(nil, workflow.DefaultRegistry())
	result, err := service.applyTargetAvailability(context.Background(), releaseTargetSnapshot{
		workflow: workflow.WorkflowView{Spec: workflow.WorkflowSpec{
			Steps: []workflow.StepSpec{{
				Key: "deploy", Name: "Deploy", Uses: workflow.NoopUses,
				With: json.RawMessage(`{"unknown":true}`),
			}},
		}},
	}, ReleasePreflight{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.ErrorCode != ReleaseCodeWorkflowInvalid || len(result.Steps) != 1 ||
		result.Steps[0].Available || result.Steps[0].ErrorCode != ReleaseCodeWorkflowInvalid {
		t.Fatalf("availability result = %#v", result)
	}
}

func TestReleaseRequestDigestEnforcesCanonicalBatchBudget(t *testing.T) {
	commands := make([]ReleaseCommand, 17)
	value := strings.Repeat("a", maxReleaseInputBytes-128)
	for index := range commands {
		normalized, err := normalizeReleaseCommand(ReleaseCommand{
			ConfigID: index + 1,
			Ref:      "main",
			Inputs:   json.RawMessage(`{"value":"` + value + `"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		commands[index] = normalized
	}
	_, err := releaseRequestDigest(SemanticReleaseBatchCreate, commands)
	var commandError *ReleaseCommandError
	if !errors.As(err, &commandError) || commandError.Code != ReleaseCodeRequestTooLarge || commandError.HTTPStatus != 413 {
		t.Fatalf("releaseRequestDigest error = %#v", err)
	}
}

func TestTransactionRetryClassificationKeepsLockWaitBounded(t *testing.T) {
	lockTimeout := &mysql.MySQLError{Number: 1205, Message: "lock wait timeout"}
	deadlock := &mysql.MySQLError{Number: 1213, Message: "deadlock"}
	if isRetryableTransactionError(lockTimeout) {
		t.Fatal("lock wait timeout must not start another potentially long transaction")
	}
	if !isLockWaitTimeout(lockTimeout) {
		t.Fatal("lock wait timeout was not classified as an in-progress reservation")
	}
	if !isRetryableTransactionError(deadlock) {
		t.Fatal("deadlock should receive the bounded transaction retry")
	}
}
