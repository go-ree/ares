package publish

import (
	"testing"

	"ares/internal/entity"
)

func TestValidateLegacyTaskAddress(t *testing.T) {
	for _, test := range []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "valid http", address: "http://jenkins.example"},
		{name: "valid https normalized", address: " https://jenkins.example/ "},
		{name: "missing", address: "", wantErr: true},
		{name: "whitespace", address: "  ", wantErr: true},
		{name: "file URL", address: "file:///tmp/jenkins", wantErr: true},
		{name: "embedded credentials", address: "https://user:secret@jenkins.example", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLegacyTaskAddress(test.address); (err != nil) != test.wantErr {
				t.Fatalf("validateLegacyTaskAddress(%q) error = %v, wantErr %v", test.address, err, test.wantErr)
			}
		})
	}
}

func TestLegacyUnsafeFailureStatus(t *testing.T) {
	for _, test := range []struct {
		current string
		want    string
	}{
		{current: entity.StatusPackaging, want: entity.StatusPackageFailed},
		{current: entity.StatusPackaged, want: entity.StatusDeployFailed},
		{current: entity.StatusDeploying, want: entity.StatusDeployFailed},
	} {
		if got := legacyUnsafeFailureStatus(test.current); got != test.want {
			t.Errorf("legacyUnsafeFailureStatus(%q) = %q, want %q", test.current, got, test.want)
		}
	}
}
