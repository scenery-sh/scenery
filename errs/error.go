package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"scenery.sh/internal/redact"
)

type ErrCode string

const (
	OK                   ErrCode = "ok"
	Canceled             ErrCode = "canceled"
	Unknown              ErrCode = "unknown"
	InvalidArgument      ErrCode = "invalid_argument"
	DeadlineExceeded     ErrCode = "deadline_exceeded"
	NotFound             ErrCode = "not_found"
	AlreadyExists        ErrCode = "already_exists"
	PermissionDenied     ErrCode = "permission_denied"
	ResourceExhausted    ErrCode = "resource_exhausted"
	FailedPrecondition   ErrCode = "failed_precondition"
	Aborted              ErrCode = "aborted"
	OutOfRange           ErrCode = "out_of_range"
	Unimplemented        ErrCode = "unimplemented"
	Internal             ErrCode = "internal"
	Unavailable          ErrCode = "unavailable"
	DataLoss             ErrCode = "data_loss"
	Unauthenticated      ErrCode = "unauthenticated"
	Conflict             ErrCode = "conflict"
	GoogleReauthRequired ErrCode = "google_reauth_required"
	GoogleScopeMissing   ErrCode = "google_scope_missing"
)

type Metadata map[string]any

type ErrDetails interface {
	ErrDetails()
}

type Error struct {
	Code    ErrCode    `json:"code"`
	Message string     `json:"message"`
	Details ErrDetails `json:"details,omitempty"`
	Meta    Metadata   `json:"meta,omitempty"`
	Cause   error      `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return string(e.Code)
	}
	return string(Unknown)
}

func (e *Error) ErrorMessage() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return e.Error()
	}
	if e.Message == "" {
		return e.Cause.Error()
	}
	return e.Message + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) MarshalJSON() ([]byte, error) {
	type alias Error
	return json.Marshal((*alias)(e))
}

type Builder struct {
	err Error
}

func B() *Builder {
	return &Builder{err: Error{Code: Unknown}}
}

func (b *Builder) Code(code ErrCode) *Builder {
	b.err.Code = code
	return b
}

func (b *Builder) Msg(msg string) *Builder {
	b.err.Message = msg
	return b
}

func (b *Builder) Msgf(format string, args ...any) *Builder {
	b.err.Message = fmt.Sprintf(format, args...)
	return b
}

func (b *Builder) Meta(kv ...any) *Builder {
	if len(kv) == 0 {
		return b
	}
	if b.err.Meta == nil {
		b.err.Meta = make(Metadata, len(kv)/2)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		b.err.Meta[key] = kv[i+1]
	}
	return b
}

func (b *Builder) Details(details ErrDetails) *Builder {
	b.err.Details = details
	return b
}

func (b *Builder) Cause(err error) *Builder {
	b.err.Cause = err
	return b
}

func (b *Builder) Err() error {
	err := b.err
	if err.Code == "" || err.Code == OK {
		err.Code = Unknown
	}
	if err.Message == "" && err.Cause != nil {
		err.Message = err.Cause.Error()
	}
	if err.Message == "" {
		err.Message = "unknown error"
	}
	return &err
}

func Wrap(err error, msg string, metaPairs ...any) error {
	if err == nil {
		return nil
	}
	if pe, ok := As(err); ok {
		clone := *pe
		if msg != "" {
			clone.Message = msg
		}
		if len(metaPairs) > 0 {
			clone.Meta = mergeMeta(clone.Meta, metaPairs...)
		}
		return &clone
	}
	wrapped := &Error{
		Code:    Internal,
		Message: msg,
		Cause:   err,
	}
	if wrapped.Message == "" {
		wrapped.Message = err.Error()
	}
	if len(metaPairs) > 0 {
		wrapped.Meta = mergeMeta(nil, metaPairs...)
	}
	return wrapped
}

func WrapCode(err error, code ErrCode, msg string, metaPairs ...any) error {
	if err == nil {
		if code == OK {
			return nil
		}
		return B().Code(code).Msg(msg).Meta(metaPairs...).Err()
	}
	wrapped := Wrap(err, msg, metaPairs...)
	if pe, ok := As(wrapped); ok {
		clone := *pe
		if code != OK {
			clone.Code = code
		}
		return &clone
	}
	return wrapped
}

func Convert(err error) error {
	if err == nil {
		return nil
	}
	if pe, ok := As(err); ok {
		return pe
	}
	return &Error{Code: Unknown, Message: err.Error(), Cause: err}
}

func As(err error) (*Error, bool) {
	return errors.AsType[*Error](err)
}

func Code(err error) ErrCode {
	if err == nil {
		return OK
	}
	if pe, ok := As(err); ok {
		if pe.Code != "" {
			return pe.Code
		}
		return Unknown
	}
	return Internal
}

func CodeOf(err error) ErrCode {
	return Code(err)
}

func Meta(err error) Metadata {
	if pe, ok := As(err); ok {
		return pe.Meta
	}
	return nil
}

func Details(err error) ErrDetails {
	if pe, ok := As(err); ok {
		return pe.Details
	}
	return nil
}

func HTTPStatus(err error) int {
	switch Code(err) {
	case OK:
		return http.StatusOK
	case Canceled:
		return 499
	case InvalidArgument, FailedPrecondition, OutOfRange, GoogleReauthRequired, GoogleScopeMissing:
		return http.StatusBadRequest
	case DeadlineExceeded:
		return http.StatusGatewayTimeout
	case Unauthenticated:
		return http.StatusUnauthorized
	case PermissionDenied:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case AlreadyExists, Aborted, Conflict:
		return http.StatusConflict
	case ResourceExhausted:
		return http.StatusTooManyRequests
	case Unimplemented:
		return http.StatusNotImplemented
	case Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func HTTPError(w http.ResponseWriter, err error) {
	HTTPErrorWithCode(w, err, 0)
}

func HTTPErrorWithCode(w http.ResponseWriter, err error, status int) {
	if status == 0 {
		status = HTTPStatus(err)
	}
	if err == nil {
		err = &Error{Code: OK}
	}
	payload := struct {
		Code    ErrCode  `json:"code"`
		Message string   `json:"message"`
		Details any      `json:"details,omitempty"`
		Meta    Metadata `json:"meta,omitempty"`
	}{
		Code:    Code(err),
		Message: err.Error(),
		Details: redact.Value(Details(err)),
		Meta:    Metadata(redact.Metadata(map[string]any(Meta(err)))),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func mergeMeta(base Metadata, kv ...any) Metadata {
	if len(kv) == 0 {
		return base
	}
	if base == nil {
		base = make(Metadata, len(kv)/2)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		base[key] = kv[i+1]
	}
	return base
}
