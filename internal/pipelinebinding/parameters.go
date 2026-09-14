// Package pipelinebinding defines the new binding contract. It does not adapt
// new templates to legacy workflows or authorize any executor invocation.
package pipelinebinding

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/pipelinetemplate"
)

const MaxParameterBytes = 16 * 1024

var (
	ErrParameters = errors.New("invalid binding parameters")
	ErrDefinition = errors.New("invalid immutable template definition")
)

// ResolveParameters never mutates or aliases caller-owned maps/JSON. Binding
// values override defaults; run values require allow_override. Neither layer
// can introduce undeclared parameters, null, objects, arrays or coercions.
// The resolved values are private; callers must not return them in preflight.
func ResolveParameters(spec pipelinetemplate.Spec, binding, run map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(pipelinetemplate.Validate(spec)) != 0 {
		return nil, ErrDefinition
	}
	for _, layer := range []map[string]json.RawMessage{binding, run} {
		if len(layer) > pipelinetemplate.MaxParameters {
			return nil, ErrParameters
		}
		raw, err := json.Marshal(layer)
		if err != nil || len(raw) > MaxParameterBytes {
			return nil, ErrParameters
		}
	}
	out := make(map[string]json.RawMessage)
	for key, p := range spec.Parameters {
		if len(p.Default) > 0 {
			value, ok := parameterValue(p.Type, p.Default)
			if !ok {
				return nil, ErrDefinition
			}
			out[key] = value
		}
	}
	for index, layer := range []map[string]json.RawMessage{binding, run} {
		for key, raw := range layer {
			p, ok := spec.Parameters[key]
			if !ok || (index == 1 && !p.AllowOverride) {
				return nil, ErrParameters
			}
			value, ok := parameterValue(p.Type, raw)
			if !ok {
				return nil, ErrParameters
			}
			out[key] = value
		}
	}
	for key, p := range spec.Parameters {
		if _, ok := out[key]; p.Required && !ok {
			return nil, ErrParameters
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > MaxParameterBytes {
		return nil, ErrParameters
	}
	return out, nil
}

func parameterValue(kind string, raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) > 4096 || !utf8.Valid(raw) {
		return nil, false
	}
	raw = bytes.TrimSpace(raw)
	switch kind {
	case "string":
		var v *string
		if json.Unmarshal(raw, &v) != nil || v == nil || len(*v) > 2048 {
			return nil, false
		}
		// Reject unpaired UTF-16 escapes, which encoding/json otherwise silently
		// replaces, by validating their pairing before decoding.
		if !validSurrogates(raw) {
			return nil, false
		}
		encoded, err := json.Marshal(*v)
		return encoded, err == nil
	case "boolean":
		if string(raw) != "true" && string(raw) != "false" {
			return nil, false
		}
	case "integer":
		if !json.Valid(raw) {
			return nil, false
		}
		if _, err := strconv.ParseInt(string(raw), 10, 64); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	return bytes.Clone(raw), true
}

func validSurrogates(raw []byte) bool {
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
