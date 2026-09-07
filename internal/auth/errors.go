package auth

// InputError identifies a validation failure that is safe and useful to show
// to the same client. Infrastructure failures must remain ordinary errors so
// HTTP adapters cannot accidentally disclose their details.
type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

func newInputError(message string) *InputError {
	return &InputError{Message: message}
}
