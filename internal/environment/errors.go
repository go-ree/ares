package environment

// ValidationError is safe to return to a caller because it describes only a
// rejected environment-directory input, never a database implementation error.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func newValidationError(message string) *ValidationError {
	return &ValidationError{Message: message}
}
