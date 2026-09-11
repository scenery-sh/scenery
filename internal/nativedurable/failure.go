package nativedurable

import "errors"

type failureError struct {
	outcome string
	cause   error
}

func (failure *failureError) Error() string {
	if failure == nil || failure.outcome == "" {
		return "contract durable execution failed"
	}
	return failure.outcome
}

func (failure *failureError) Unwrap() error { return failure.cause }

func Failure(outcome string, cause error) error {
	return &failureError{outcome: outcome, cause: cause}
}

func FailureOutcome(err error) string {
	var failure *failureError
	if errors.As(err, &failure) && failure.outcome != "" {
		return failure.outcome
	}
	return "system.internal"
}
