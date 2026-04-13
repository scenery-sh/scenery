package errs

import (
	"net/http"

	pulseerrs "pulse.dev/errs"
)

type ErrCode = pulseerrs.ErrCode
type Code = pulseerrs.ErrCode
type Metadata = pulseerrs.Metadata
type ErrDetails = pulseerrs.ErrDetails
type Error = pulseerrs.Error
type Builder = pulseerrs.Builder

const (
	OK                 = pulseerrs.OK
	Canceled           = pulseerrs.Canceled
	Unknown            = pulseerrs.Unknown
	InvalidArgument    = pulseerrs.InvalidArgument
	DeadlineExceeded   = pulseerrs.DeadlineExceeded
	NotFound           = pulseerrs.NotFound
	AlreadyExists      = pulseerrs.AlreadyExists
	PermissionDenied   = pulseerrs.PermissionDenied
	ResourceExhausted  = pulseerrs.ResourceExhausted
	FailedPrecondition = pulseerrs.FailedPrecondition
	Aborted            = pulseerrs.Aborted
	OutOfRange         = pulseerrs.OutOfRange
	Unimplemented      = pulseerrs.Unimplemented
	Internal           = pulseerrs.Internal
	Unavailable        = pulseerrs.Unavailable
	DataLoss           = pulseerrs.DataLoss
	Unauthenticated    = pulseerrs.Unauthenticated
	Conflict           = pulseerrs.Conflict
)

func B() *Builder {
	return pulseerrs.B()
}

func Wrap(err error, msg string, metaPairs ...any) error {
	return pulseerrs.Wrap(err, msg, metaPairs...)
}

func WrapCode(err error, code ErrCode, msg string, metaPairs ...any) error {
	return pulseerrs.WrapCode(err, code, msg, metaPairs...)
}

func Convert(err error) error {
	return pulseerrs.Convert(err)
}

func As(err error) (*Error, bool) {
	return pulseerrs.As(err)
}

func CodeOf(err error) Code {
	return pulseerrs.Code(err)
}

func Meta(err error) Metadata {
	return pulseerrs.Meta(err)
}

func Details(err error) ErrDetails {
	return pulseerrs.Details(err)
}

func HTTPStatus(err error) int {
	return pulseerrs.HTTPStatus(err)
}

func HTTPError(w http.ResponseWriter, err error) {
	pulseerrs.HTTPError(w, err)
}

func HTTPErrorWithCode(w http.ResponseWriter, err error, status int) {
	pulseerrs.HTTPErrorWithCode(w, err, status)
}
