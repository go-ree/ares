package publish

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClientErrorMessageRequiresExplicitClassification(t *testing.T) {
	const secret = "mysql://admin:provider-super-secret@database.internal/ares"
	if message, safe := ClientErrorMessage(errors.New(secret)); safe || strings.Contains(message, secret) {
		t.Fatalf("unclassified error was public: message=%q safe=%v", message, safe)
	}
	input := newInputError("应用不存在")
	message, safe := ClientErrorMessage(fmt.Errorf("%s: %w", secret, input))
	if !safe || message != input.Error() || strings.Contains(message, secret) {
		t.Fatalf("typed input error was not safely unwrapped: message=%q safe=%v", message, safe)
	}
}
