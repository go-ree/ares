package publish

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/canonicaljson"
)

const (
	minIdempotencyKeyBytes = 16
	maxIdempotencyKeyBytes = 128
	maxReleaseInputBytes   = 64 * 1024
	maxReleaseInputDepth   = 16
	maxReleaseInputNodes   = 1000
	maxBatchReleaseItems   = 100
	maxReleaseCommandBytes = 1024 * 1024
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)
var errReleaseInputLimit = errors.New("release input limit exceeded")

type IdempotencyToken struct {
	digest [sha256.Size]byte
}

func ParseIdempotencyKey(values []string) (IdempotencyToken, error) {
	if len(values) != 1 {
		return IdempotencyToken{}, releaseError(ReleaseCodeIdempotencyKeyInvalid, 400,
			"Idempotency-Key 必须且只能提供一次")
	}
	key := values[0]
	if len(key) < minIdempotencyKeyBytes || len(key) > maxIdempotencyKeyBytes ||
		!utf8.ValidString(key) || !idempotencyKeyPattern.MatchString(key) {
		return IdempotencyToken{}, releaseError(ReleaseCodeIdempotencyKeyInvalid, 400,
			"Idempotency-Key 必须为 16～128 字节的 ASCII token")
	}
	return IdempotencyToken{digest: sha256.Sum256([]byte("ares:idempotency-key:v1\x00" + key))}, nil
}

func (t IdempotencyToken) bytes() []byte {
	return append([]byte(nil), t.digest[:]...)
}

// NewEphemeralIdempotencyToken lets a deprecated client without a key use the
// same atomic domain path while deliberately preserving its non-replayable
// legacy semantics.
func NewEphemeralIdempotencyToken() (IdempotencyToken, error) {
	var token IdempotencyToken
	if _, err := rand.Read(token.digest[:]); err != nil {
		return IdempotencyToken{}, fmt.Errorf("生成临时幂等标识: %w", err)
	}
	return token, nil
}

type releaseDigestItem struct {
	ConfigID                  int             `json:"config_id"`
	Ref                       string          `json:"ref"`
	Inputs                    json.RawMessage `json:"inputs"`
	ExpectedWorkflowVersionID *int64          `json:"expected_workflow_version_id"`
}

type releaseDigestEnvelope struct {
	Schema string `json:"schema"`
	releaseDigestItem
}

type releaseBatchDigestEnvelope struct {
	Schema string              `json:"schema"`
	Items  []releaseDigestItem `json:"items"`
}

func normalizeReleaseCommand(command ReleaseCommand) (ReleaseCommand, error) {
	if command.ConfigID <= 0 {
		return ReleaseCommand{}, invalidReleaseRequest("config_id 必须大于 0")
	}
	if command.Ref == "" || !utf8.ValidString(command.Ref) || strings.TrimSpace(command.Ref) != command.Ref ||
		utf8.RuneCountInString(command.Ref) > 100 {
		return ReleaseCommand{}, invalidReleaseRequest("ref 不能为空、不能包含首尾空白且不能超过 100 个字符")
	}
	for _, char := range command.Ref {
		if unicode.IsControl(char) {
			return ReleaseCommand{}, invalidReleaseRequest("ref 不能包含控制字符")
		}
	}
	if command.ExpectedWorkflowVersionID != nil && *command.ExpectedWorkflowVersionID <= 0 {
		return ReleaseCommand{}, invalidReleaseRequest("expected_workflow_version_id 必须大于 0")
	}

	inputs := command.Inputs
	if len(inputs) == 0 {
		inputs = json.RawMessage(`{}`)
	}
	if bytes.Equal(bytes.TrimSpace(inputs), []byte("null")) {
		return ReleaseCommand{}, invalidReleaseRequest("inputs 必须是 JSON 对象，不能为 null")
	}
	canonical, decoded, err := canonicalReleaseInputs(inputs)
	if err != nil {
		if errors.Is(err, errReleaseInputLimit) {
			return ReleaseCommand{}, releaseError(ReleaseCodeRequestTooLarge, 413, "inputs 超过允许大小或复杂度")
		}
		return ReleaseCommand{}, invalidReleaseRequest("inputs 必须是受限的 JSON 对象")
	}
	if err := validateReleaseExtraData(decoded, "inputs"); err != nil {
		return ReleaseCommand{}, invalidReleaseRequest(err.Error())
	}
	command.Inputs = canonical
	return command, nil
}

func normalizeReleaseCommands(commands []ReleaseCommand) ([]ReleaseCommand, error) {
	if len(commands) == 0 {
		return nil, invalidReleaseRequest("批量发布至少需要 1 项")
	}
	if len(commands) > maxBatchReleaseItems {
		return nil, releaseError(ReleaseCodeRequestTooLarge, 413, "批量发布不能超过 100 项")
	}
	seen := make(map[int]struct{}, len(commands))
	normalized := make([]ReleaseCommand, len(commands))
	for index, command := range commands {
		item, err := normalizeReleaseCommand(command)
		if err != nil {
			return nil, fmt.Errorf("items[%d]: %w", index, err)
		}
		if _, exists := seen[item.ConfigID]; exists {
			return nil, invalidReleaseRequest("同一批量请求不能重复 config_id")
		}
		seen[item.ConfigID] = struct{}{}
		normalized[index] = item
	}
	return normalized, nil
}

func canonicalReleaseInputs(raw json.RawMessage) (json.RawMessage, any, error) {
	if len(raw) > maxReleaseInputBytes {
		return nil, nil, fmt.Errorf("%w: inputs bytes", errReleaseInputLimit)
	}
	if !utf8.Valid(raw) {
		return nil, nil, fmt.Errorf("inputs contains invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("inputs is not an object")
	}
	if err := validateJSONComplexity(object, 1, new(int)); err != nil {
		return nil, nil, err
	}
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return nil, nil, err
	}
	if len(canonical) > maxReleaseInputBytes {
		return nil, nil, fmt.Errorf("%w: canonical inputs bytes", errReleaseInputLimit)
	}
	return json.RawMessage(canonical), object, nil
}

func validateJSONComplexity(value any, depth int, nodes *int) error {
	*nodes++
	if *nodes > maxReleaseInputNodes {
		return fmt.Errorf("%w: JSON nodes", errReleaseInputLimit)
	}
	if depth > maxReleaseInputDepth {
		return fmt.Errorf("%w: JSON depth", errReleaseInputLimit)
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			if err := validateJSONComplexity(nested, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := validateJSONComplexity(nested, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

func releaseRequestDigest(operation string, commands []ReleaseCommand) ([sha256.Size]byte, error) {
	items := make([]releaseDigestItem, len(commands))
	for index, command := range commands {
		items[index] = releaseDigestItem{
			ConfigID: command.ConfigID, Ref: command.Ref,
			Inputs:                    append(json.RawMessage(nil), command.Inputs...),
			ExpectedWorkflowVersionID: command.ExpectedWorkflowVersionID,
		}
	}
	var envelope any
	if operation == SemanticReleaseCreate && len(items) == 1 {
		envelope = releaseDigestEnvelope{Schema: operation, releaseDigestItem: items[0]}
	} else {
		envelope = releaseBatchDigestEnvelope{Schema: operation, Items: items}
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if len(raw) > maxReleaseCommandBytes {
		return [sha256.Size]byte{}, releaseError(ReleaseCodeRequestTooLarge, 413, "规范化发布请求不能超过 1 MiB")
	}
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	if len(canonical) > maxReleaseCommandBytes {
		return [sha256.Size]byte{}, releaseError(ReleaseCodeRequestTooLarge, 413, "规范化发布请求不能超过 1 MiB")
	}
	return sha256.Sum256(canonical), nil
}
