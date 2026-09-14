package pipelinebinding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/pipelinetemplate"
)

func testSpec() pipelinetemplate.Spec {
	a := pipelinetemplate.ArtifactType{Kind: "file", MediaType: "application/java-archive", Simulated: true}
	return pipelinetemplate.Spec{SchemaVersion: 1, Kind: "ci", Name: "Java", ApplicationType: "java", Parameters: map[string]pipelinetemplate.Parameter{
		"profile":  {Type: "string", Required: true, AllowOverride: true, Default: json.RawMessage(`"default"`)},
		"count":    {Type: "integer", AllowOverride: true, Default: json.RawMessage(`9007199254740993`)},
		"tests":    {Type: "boolean", Required: true, Default: json.RawMessage(`true`)},
		"optional": {Type: "string"},
	}, Steps: []pipelinetemplate.Step{{Key: "build", Uses: "mock.build@v1", Outputs: map[string]pipelinetemplate.ArtifactType{"jar": a}}}, Outputs: map[string]pipelinetemplate.Input{"jar": {From: "steps.build.outputs.jar", Type: a}}}
}
func TestParameterPrecedenceAndOwnership(t *testing.T) {
	spec := testSpec()
	binding := map[string]json.RawMessage{"profile": json.RawMessage(`"binding"`), "tests": json.RawMessage(`false`)}
	run := map[string]json.RawMessage{"profile": json.RawMessage(`"run"`)}
	before, _ := json.Marshal([]any{spec, binding, run})
	out, err := ResolveParameters(spec, binding, run)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]json.RawMessage{"profile": json.RawMessage(`"run"`), "tests": json.RawMessage(`false`), "count": json.RawMessage(`9007199254740993`)}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("%s", out)
	}
	out["count"][0] = '1'
	out["tests"][0] = 'x'
	out["profile"][1] = 'x'
	after, _ := json.Marshal([]any{spec, binding, run})
	if string(before) != string(after) {
		t.Fatal("caller state mutated")
	}
	out, err = ResolveParameters(spec, nil, nil)
	if err != nil || string(out["tests"]) != "true" {
		t.Fatal("missing defaults")
	}
	if _, ok := out["optional"]; ok {
		t.Fatal("optional absent field fabricated")
	}
}
func TestParameterValidation(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		run              bool
	}{
		{"null", "profile", "null", false}, {"string-number", "count", `"2"`, false}, {"fraction", "count", "1.0", false}, {"exponent", "count", "1e0", false}, {"overflow", "count", "9223372036854775808", false}, {"object", "profile", "{}", false}, {"array", "profile", "[]", false}, {"unknown", "undeclared", `"PRIVATE"`, false}, {"locked-runtime", "tests", "false", true}, {"unknown-runtime", "other", "true", true}, {"wrong-bool", "tests", `"false"`, false}, {"trailing", "count", "1 2", false}, {"long-string", "profile", `"` + strings.Repeat("a", 2049) + `"`, false}, {"surrogate", "profile", `"\ud800"`, false}, {"low-surrogate", "profile", `"\udc00"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer := map[string]json.RawMessage{tc.key: json.RawMessage(tc.value)}
			var err error
			if tc.run {
				_, err = ResolveParameters(testSpec(), nil, layer)
			} else {
				_, err = ResolveParameters(testSpec(), layer, nil)
			}
			if err != ErrParameters {
				t.Fatalf("%v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("value leak")
			}
		})
	}
	spec := testSpec()
	p := spec.Parameters["profile"]
	p.Default = nil
	spec.Parameters["profile"] = p
	if _, err := ResolveParameters(spec, nil, nil); err != ErrParameters {
		t.Fatal("missing required accepted")
	}
	spec.SchemaVersion = 2
	if _, err := ResolveParameters(spec, nil, nil); err != ErrDefinition {
		t.Fatal("unknown spec version accepted")
	}
	for _, v := range []string{`"\ud83d\ude00"`, `"\\ud800"`, `"中文"`} {
		if _, err := ResolveParameters(testSpec(), map[string]json.RawMessage{"profile": json.RawMessage(v)}, nil); err != nil {
			t.Fatalf("valid string rejected %s", v)
		}
	}
	for _, v := range []string{"-9223372036854775808", "9223372036854775807"} {
		if _, err := ResolveParameters(testSpec(), map[string]json.RawMessage{"count": json.RawMessage(v)}, nil); err != nil {
			t.Fatal(err)
		}
	}
}
func TestParameterAggregateLimits(t *testing.T) {
	spec := testSpec()
	spec.Parameters = make(map[string]pipelinetemplate.Parameter)
	binding := make(map[string]json.RawMessage)
	for i := 0; i < 9; i++ {
		key := string(rune('a' + i))
		spec.Parameters[key] = pipelinetemplate.Parameter{Type: "string"}
		binding[key] = json.RawMessage(`"` + strings.Repeat("x", 2000) + `"`)
	}
	if _, err := ResolveParameters(spec, binding, nil); err != ErrParameters {
		t.Fatal("aggregate layer limit ignored")
	}
	for key, value := range binding {
		spec.Parameters[key] = pipelinetemplate.Parameter{Type: "string", Default: value}
	}
	if _, err := ResolveParameters(spec, nil, nil); err != ErrParameters {
		t.Fatal("resolved default aggregate limit ignored")
	}
}
