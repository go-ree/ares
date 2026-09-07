package app

import (
	"errors"
	"strings"
	"testing"
)

func TestSafeAppCreationErrorRedactsInfrastructureFailures(t *testing.T) {
	const secret = "mysql://admin:provider-super-secret@database.internal/ares"
	if got := safeAppCreationError(errors.New(secret)); strings.Contains(got, secret) {
		t.Fatalf("infrastructure error leaked: %q", got)
	}
	validation := NewValidationError("app_name 格式无效")
	if got := safeAppCreationError(validation); got != validation.Error() {
		t.Fatalf("validation error = %q, want %q", got, validation.Error())
	}
}
