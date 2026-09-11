// Package pipelinetemplate defines the new CI/CD template contract. It never
// starts executors or reads/writes persisted templates or artifacts.
package pipelinetemplate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/security"
)

const (
	MaxRequestBytes = 64 * 1024
	MaxSteps        = 32
	MaxSlots        = 16
	MaxParameters   = 32
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var usesPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*\.[a-z][a-z0-9.-]*@v[1-9][0-9]*$`)

type ArtifactType struct {
	Kind      string `json:"kind"`
	MediaType string `json:"media_type"`
	Simulated bool   `json:"simulated"`
}

type Parameter struct {
	Type          string          `json:"type"`
	Required      bool            `json:"required"`
	AllowOverride bool            `json:"allow_override"`
	Default       json.RawMessage `json:"default,omitempty" swaggertype:"object"`
}

type Input struct {
	From string       `json:"from"`
	Type ArtifactType `json:"type"`
}

type Step struct {
	Key     string                  `json:"key"`
	Uses    string                  `json:"uses"`
	With    json.RawMessage         `json:"with,omitempty" swaggertype:"object"`
	Inputs  map[string]Input        `json:"inputs,omitempty"`
	Outputs map[string]ArtifactType `json:"outputs,omitempty"`
	// Keys are executor parameter slots; values name template parameters.
	Parameters map[string]string `json:"parameters,omitempty"`
}

type Spec struct {
	SchemaVersion   int                     `json:"schema_version"`
	Kind            string                  `json:"kind"`
	Name            string                  `json:"name"`
	ApplicationType string                  `json:"application_type,omitempty"`
	TargetType      string                  `json:"target_type,omitempty"`
	Parameters      map[string]Parameter    `json:"parameters,omitempty"`
	Inputs          map[string]ArtifactType `json:"inputs,omitempty"`
	Steps           []Step                  `json:"steps"`
	Outputs         map[string]Input        `json:"outputs,omitempty"`
}

type Problem struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// Validate checks only the structural contract. Even a valid result must not
// be converted to a legacy workflow or treated as executor capability proof.
// Returned problems never echo supplied values or opaque executor config.
func Validate(s Spec) []Problem {
	problems := make([]Problem, 0)
	add := func(path, code string) { problems = append(problems, Problem{path, code}) }
	if s.SchemaVersion != 1 {
		add("schema_version", "unsupported_version")
	}
	if strings.TrimSpace(s.Name) == "" || utf8.RuneCountInString(s.Name) > 120 || !utf8.ValidString(s.Name) {
		add("name", "invalid_name")
	}
	switch s.Kind {
	case "ci":
		if !identifier.MatchString(s.ApplicationType) {
			add("application_type", "invalid_identifier")
		}
		if s.TargetType != "" || len(s.Inputs) != 0 {
			add("inputs", "ci_has_no_deployment_inputs")
		}
		if len(s.Outputs) == 0 {
			add("outputs", "ci_requires_output")
		}
	case "cd":
		if s.ApplicationType != "" {
			add("application_type", "cd_is_not_language_bound")
		}
		if !identifier.MatchString(s.TargetType) {
			add("target_type", "invalid_identifier")
		}
		if len(s.Inputs) == 0 {
			add("inputs", "cd_requires_input")
		}
	default:
		add("kind", "invalid_kind")
	}
	if len(s.Steps) < 1 || len(s.Steps) > MaxSteps {
		add("steps", "invalid_count")
		return problems
	}
	if len(s.Parameters) > MaxParameters {
		add("parameters", "too_many_parameters")
		return problems
	}
	for _, key := range keys(s.Parameters) {
		p := s.Parameters[key]
		if !identifier.MatchString(key) || security.IsSensitiveKey(key) {
			add("parameters", "invalid_parameter_name")
			continue
		}
		if p.Type != "string" && p.Type != "boolean" && p.Type != "integer" {
			add("parameters."+key, "invalid_parameter_type")
			continue
		}
		if len(p.Default) > 0 && !validDefault(p) {
			add("parameters."+key, "invalid_default")
		}
	}
	available := make(map[string]ArtifactType)
	checkTypes := func(path string, types map[string]ArtifactType, prefix string) {
		if len(types) > MaxSlots {
			add(path, "too_many_slots")
			return
		}
		for _, key := range keys(types) {
			if !identifier.MatchString(key) {
				add(path, "invalid_slot_name")
				continue
			}
			t := types[key]
			if !validType(t) {
				add(path+"."+key, "invalid_artifact_type")
				continue
			}
			available[prefix+key] = t
		}
	}
	checkTypes("inputs", s.Inputs, "inputs.")
	checkRefs := func(path string, inputs map[string]Input) {
		if len(inputs) > MaxSlots {
			add(path, "too_many_slots")
			return
		}
		for _, key := range keys(inputs) {
			if !identifier.MatchString(key) {
				add(path, "invalid_slot_name")
				continue
			}
			input := inputs[key]
			t, ok := available[input.From]
			if !ok {
				add(path+"."+key, "unknown_or_forward_reference")
			} else if !validType(input.Type) || t != input.Type {
				add(path+"."+key, "artifact_type_mismatch")
			}
		}
	}
	seen := make(map[string]bool)
	for i, step := range s.Steps {
		path := fmt.Sprintf("steps[%d]", i)
		if !identifier.MatchString(step.Key) || seen[step.Key] {
			add(path+".key", "invalid_or_duplicate_key")
			continue
		}
		seen[step.Key] = true
		if !usesPattern.MatchString(step.Uses) || len(step.Uses) > 128 {
			add(path+".uses", "invalid_executor_reference")
		}
		if len(step.With) > 0 {
			var obj map[string]json.RawMessage
			if json.Unmarshal(step.With, &obj) != nil || obj == nil || len(step.With) > 8192 || security.ValidateJSONNoSensitiveKeys(step.With, "with") != nil {
				add(path+".with", "invalid_or_sensitive_config")
			}
		}
		if len(step.Parameters) > MaxParameters {
			add(path+".parameters", "too_many_parameters")
		} else {
			for _, slot := range keys(step.Parameters) {
				_, ok := s.Parameters[step.Parameters[slot]]
				if !identifier.MatchString(slot) || security.IsSensitiveKey(slot) || !ok {
					add(path+".parameters", "invalid_parameter_binding")
				}
			}
		}
		checkRefs(path+".inputs", step.Inputs)
		// Publish declarations only after inputs: self references are rejected.
		checkTypes(path+".outputs", step.Outputs, "steps."+step.Key+".outputs.")
	}
	checkRefs("outputs", s.Outputs)
	if s.Kind == "ci" {
		for _, output := range s.Outputs {
			if !strings.HasPrefix(output.From, "steps.") {
				add("outputs", "ci_output_requires_step")
				break
			}
		}
	}
	return problems
}

func validType(t ArtifactType) bool {
	switch t.Kind {
	case "oci_image":
		return t.MediaType == "application/vnd.oci.image.manifest.v1+json" || t.MediaType == "application/vnd.oci.image.index.v1+json"
	case "file":
		return t.MediaType == "application/java-archive" || t.MediaType == "application/zip" || t.MediaType == "application/octet-stream"
	default:
		return false
	}
}

func validDefault(p Parameter) bool {
	if len(p.Default) > 4096 {
		return false
	}
	switch p.Type {
	case "string":
		var v *string
		return json.Unmarshal(p.Default, &v) == nil && v != nil && utf8.ValidString(*v) && len(*v) <= 2048
	case "boolean":
		return string(p.Default) == "true" || string(p.Default) == "false"
	case "integer":
		v := string(p.Default)
		_, err := strconv.ParseInt(v, 10, 64)
		return err == nil && json.Valid(p.Default) && !strings.ContainsAny(v, ".eE+")
	}
	return false
}

func keys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
