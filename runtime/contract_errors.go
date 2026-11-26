package runtime

// ContractTransportError is a failure a generated adapter or client reports
// instead of a declared outcome. The worker answers it with a neutral fault.
type ContractTransportError struct {
	Outcome string
	Message string
	Cause   error
}

// ContractSystemError wraps an implementation failure of a generated
// adapter or client.
func ContractSystemError(err error) error {
	return &ContractTransportError{Outcome: "system.internal", Message: "contract implementation failure", Cause: err}
}

func (e *ContractTransportError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Outcome
}

func (e *ContractTransportError) Unwrap() error { return e.Cause }
