package runtime

import (
	"errors"
	"testing"

	"github.com/pbrazdil/onlava/errs"
)

type plainValidationPayload struct{}

func (plainValidationPayload) Validate() error {
	return errors.New("payload is invalid")
}

type codedValidationPayload struct{}

func (codedValidationPayload) Validate() error {
	return errs.B().Code(errs.FailedPrecondition).Msg("bad payload state").Err()
}

func TestMaybeValidateConvertsPlainErrorsToInvalidArgument(t *testing.T) {
	t.Parallel()

	err := maybeValidate(plainValidationPayload{})
	if err == nil {
		t.Fatal("maybeValidate() = nil, want error")
	}
	if got := errs.Code(err); got != errs.InvalidArgument {
		t.Fatalf("errs.Code(maybeValidate()) = %q, want %q", got, errs.InvalidArgument)
	}
	if got := err.Error(); got != "payload is invalid" {
		t.Fatalf("maybeValidate() message = %q, want %q", got, "payload is invalid")
	}
	if got := errs.HTTPStatus(err); got != 400 {
		t.Fatalf("errs.HTTPStatus(maybeValidate()) = %d, want 400", got)
	}
}

func TestMaybeValidatePreservesCodedErrors(t *testing.T) {
	t.Parallel()

	err := maybeValidate(codedValidationPayload{})
	if err == nil {
		t.Fatal("maybeValidate() = nil, want error")
	}
	if got := errs.Code(err); got != errs.FailedPrecondition {
		t.Fatalf("errs.Code(maybeValidate()) = %q, want %q", got, errs.FailedPrecondition)
	}
	if got := err.Error(); got != "bad payload state" {
		t.Fatalf("maybeValidate() message = %q, want %q", got, "bad payload state")
	}
}
