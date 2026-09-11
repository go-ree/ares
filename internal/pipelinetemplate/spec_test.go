package pipelinetemplate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sample() Spec {
	t := ArtifactType{Kind: "file", MediaType: "application/java-archive", Simulated: true}
	return Spec{SchemaVersion: 1, Kind: "ci", Name: "Java Maven", ApplicationType: "java",
		Parameters: map[string]Parameter{"module": {Type: "string", Default: json.RawMessage(`"app"`)}},
		Steps:      []Step{{Key: "build", Uses: "example.maven@v1", Parameters: map[string]string{"module": "module"}, Outputs: map[string]ArtifactType{"jar": t}}},
		Outputs:    map[string]Input{"package": {From: "steps.build.outputs.jar", Type: t}}}
}

func TestTemplateContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Spec)
		code   string
	}{
		{"valid ci", func(s *Spec) {}, ""},
		{"custom type", func(s *Spec) { s.ApplicationType = "rust-service" }, ""},
		{"future schema", func(s *Spec) { s.SchemaVersion = 2 }, "unsupported_version"},
		{"invalid kind", func(s *Spec) { s.Kind = "combined" }, "invalid_kind"},
		{"ci environment", func(s *Spec) { s.TargetType = "kubernetes" }, "ci_has_no_deployment_inputs"},
		{"empty outputs", func(s *Spec) { s.Outputs = nil }, "ci_requires_output"},
		{"duplicate steps", func(s *Spec) { s.Steps = append(s.Steps, s.Steps[0]) }, "invalid_or_duplicate_key"},
		{"self reference", func(s *Spec) { s.Steps[0].Inputs = s.Outputs }, "unknown_or_forward_reference"},
		{"forward reference", func(s *Spec) {
			s.Steps = append([]Step{{Key: "first", Uses: "example.check@v1", Inputs: s.Outputs}}, s.Steps...)
		}, "unknown_or_forward_reference"},
		{"missing reference", func(s *Spec) {
			v := s.Outputs["package"]
			v.From = "steps.missing.outputs.jar"
			s.Outputs["package"] = v
		}, "unknown_or_forward_reference"},
		{"simulation mismatch", func(s *Spec) { v := s.Outputs["package"]; v.Type.Simulated = false; s.Outputs["package"] = v }, "artifact_type_mismatch"},
		{"media mismatch", func(s *Spec) {
			v := s.Outputs["package"]
			v.Type.MediaType = "application/zip"
			s.Outputs["package"] = v
		}, "artifact_type_mismatch"},
		{"unknown parameter", func(s *Spec) { s.Steps[0].Parameters["module"] = "missing" }, "invalid_parameter_binding"},
		{"secret parameter", func(s *Spec) { s.Parameters["password"] = Parameter{Type: "string"} }, "invalid_parameter_name"},
		{"sensitive config", func(s *Spec) { s.Steps[0].With = json.RawMessage(`{"token":"do-not-echo"}`) }, "invalid_or_sensitive_config"},
		{"config must object", func(s *Spec) { s.Steps[0].With = json.RawMessage(`[]`) }, "invalid_or_sensitive_config"},
		{"count bound", func(s *Spec) { s.Steps = make([]Step, MaxSteps+1) }, "invalid_count"},
		{"parameter type", func(s *Spec) { s.Parameters["module"] = Parameter{Type: "object"} }, "invalid_parameter_type"},
		{"integer overflow", func(s *Spec) {
			s.Parameters["module"] = Parameter{Type: "integer", Default: json.RawMessage(`9223372036854775808`)}
		}, "invalid_default"},
		{"integer precision", func(s *Spec) {
			s.Parameters["module"] = Parameter{Type: "integer", Default: json.RawMessage(`9223372036854775807`)}
		}, ""},
		{"null default", func(s *Spec) { s.Parameters["module"] = Parameter{Type: "string", Default: json.RawMessage(`null`)} }, "invalid_default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sample()
			tc.change(&s)
			before, _ := json.Marshal(s)
			problems := Validate(s)
			after, _ := json.Marshal(s)
			if string(before) != string(after) {
				t.Fatal("validator mutated input")
			}
			if !reflect.DeepEqual(problems, Validate(s)) {
				t.Fatal("unstable diagnostics")
			}
			if tc.code == "" {
				if len(problems) != 0 {
					t.Fatal(problems)
				}
				return
			}
			for _, p := range problems {
				if p.Code == tc.code {
					return
				}
			}
			t.Fatalf("missing %s in %+v", tc.code, problems)
		})
	}
}

func TestCDChainAndLimits(t *testing.T) {
	typeOf := ArtifactType{Kind: "oci_image", MediaType: "application/vnd.oci.image.manifest.v1+json"}
	s := Spec{SchemaVersion: 1, Kind: "cd", Name: "add agent", TargetType: "kubernetes", Inputs: map[string]ArtifactType{"image": typeOf}, Steps: []Step{
		{Key: "agent", Uses: "example.agent@v1", Inputs: map[string]Input{"base": {From: "inputs.image", Type: typeOf}}, Outputs: map[string]ArtifactType{"image": typeOf}},
		{Key: "deploy", Uses: "example.deploy@v1", Inputs: map[string]Input{"image": {From: "steps.agent.outputs.image", Type: typeOf}}},
	}}
	if p := Validate(s); len(p) != 0 {
		t.Fatal(p)
	}
	s.ApplicationType = "java"
	if p := Validate(s); len(p) == 0 {
		t.Fatal("accepted language-bound CD")
	}
	s.ApplicationType = ""
	s.Inputs = nil
	if p := Validate(s); len(p) == 0 {
		t.Fatal("accepted input-free CD")
	}
	s = sample()
	s.Steps[0].With = json.RawMessage(`{"note":"` + strings.Repeat("x", 8192) + `"}`)
	if p := Validate(s); len(p) == 0 {
		t.Fatal("accepted oversized config")
	}
	s = sample()
	s.Outputs = map[string]Input{}
	for i := 0; i <= MaxSlots; i++ {
		s.Outputs[strings.Repeat("a", i+1)] = Input{}
	}
	if p := Validate(s); len(p) == 0 {
		t.Fatal("accepted oversized outputs")
	}
}

func FuzzValidateNoPanic(f *testing.F) {
	data, _ := json.Marshal(sample())
	f.Add(data)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxRequestBytes {
			return
		}
		var s Spec
		if json.Unmarshal(data, &s) != nil {
			return
		}
		Validate(s)
	})
}
