package publish

import (
	"errors"

	"github.com/go-ree/ares/internal/environment"
	"github.com/go-ree/ares/internal/workflow"
)

// InputError marks a release request validation failure that is safe to show
// to the caller. All other errors are treated as infrastructure failures.
type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

func newInputError(message string) *InputError { return &InputError{Message: message} }

type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string { return e.Message }

func newNotFoundError(message string) *NotFoundError { return &NotFoundError{Message: message} }

// ClientErrorMessage returns a public message only for explicitly classified
// domain and input errors. A false result means the original error may contain
// database, upstream or secret-bearing implementation details.
func ClientErrorMessage(err error) (string, bool) {
	var inputError *InputError
	var environmentValidation *environment.ValidationError
	var workflowValidation *workflow.ValidationError
	switch {
	case errors.As(err, &inputError):
		return inputError.Error(), true
	case errors.As(err, &environmentValidation):
		return environmentValidation.Error(), true
	case errors.Is(err, environment.ErrNotFound):
		return environment.ErrNotFound.Error(), true
	case errors.Is(err, environment.ErrDisabled):
		return environment.ErrDisabled.Error(), true
	case errors.Is(err, ErrWorkflowNotConfigured):
		return ErrWorkflowNotConfigured.Error(), true
	case errors.As(err, &workflowValidation):
		return workflowValidation.Error(), true
	default:
		return "", false
	}
}

func NotFoundErrorMessage(err error) (string, bool) {
	var notFoundError *NotFoundError
	if !errors.As(err, &notFoundError) {
		return "", false
	}
	return notFoundError.Error(), true
}
