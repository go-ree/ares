package environment

import "testing"

func TestNormalizeCode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "normalizes case and whitespace", input: "  QA-CN ", want: "qa-cn"},
		{name: "accepts dots and underscores", input: "prod_blue.v2", want: "prod_blue.v2"},
		{name: "rejects leading number", input: "2prod", wantErr: true},
		{name: "rejects whitespace", input: "prod blue", wantErr: true},
		{name: "rejects empty", input: " ", wantErr: true},
		{name: "rejects too long", input: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeCode(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeCode() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("NormalizeCode() = %q, want %q", got, test.want)
			}
		})
	}
}
