package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"scenery.sh/errs"
	"scenery.sh/internal/authbridge"
)

// A worker invocation carries the caller's authentication: the host supplies
// the principal and the encoded authentication data, which the worker
// decodes into the request it serves.

type processLinkAuth struct {
	UID      string          `json:"uid,omitempty"`
	DataKind string          `json:"data_kind,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

const processLinkJSONAuthData = "json"

func decodeProcessLinkAuth(encoded processLinkAuth) (AuthInfo, error) {
	auth := AuthInfo{UID: encoded.UID}
	switch encoded.DataKind {
	case "":
		if len(encoded.Data) != 0 {
			return auth, fmt.Errorf("authentication data has no kind")
		}
		return auth, nil
	case processLinkJSONAuthData:
		var data map[string]any
		if err := json.Unmarshal(encoded.Data, &data); err != nil || data == nil {
			return auth, fmt.Errorf("decode authentication data: %v", err)
		}
		auth.Data = data
		return auth, nil
	}
	data, err := authbridge.DecodeData(encoded.DataKind, encoded.Data)
	if err != nil {
		return auth, fmt.Errorf("decode authentication data %s: %w", encoded.DataKind, err)
	}
	auth.Data = data
	return auth, nil
}

// processLinkError is the supported failure identity of a forwarded call:
// transport outcomes, typed errs failures, cancellation and deadline expiry.
// Other Go errors keep only their message.
type processLinkError struct {
	Kind    string            `json:"kind"`
	Outcome string            `json:"outcome,omitempty"`
	Status  int               `json:"status,omitempty"`
	Code    errs.ErrCode      `json:"code,omitempty"`
	Message string            `json:"message"`
	Meta    errs.Metadata     `json:"meta,omitempty"`
	Cause   *processLinkError `json:"cause,omitempty"`
}

func (encoded *processLinkError) err() error {
	if encoded == nil {
		return nil
	}
	switch encoded.Kind {
	case "transport":
		return &ContractTransportError{Outcome: encoded.Outcome, Message: encoded.Message, Cause: encoded.Cause.err()}
	case "errs":
		return &errs.Error{Code: encoded.Code, Message: encoded.Message, Meta: encoded.Meta, Cause: encoded.Cause.err()}
	case "deadline_exceeded":
		return &processLinkSentinelError{message: encoded.Message, sentinel: context.DeadlineExceeded}
	case "canceled":
		return &processLinkSentinelError{message: encoded.Message, sentinel: context.Canceled}
	default:
		if encoded.Code != "" || len(encoded.Meta) != 0 {
			// A Host binding failure carries {code,message,meta}; keeping the
			// code and meta.delivery lets the caller tell a safe retry apart.
			return &errs.Error{Code: hostFailureCode(encoded.Code), Message: encoded.Message, Meta: encoded.Meta, Cause: encoded.Cause.err()}
		}
		return errors.New(encoded.Message)
	}
}

// hostFailureCode maps a Host fault code onto the errs vocabulary. The Host
// names its transport and system faults by category; known errs codes pass
// through and any other code is unknown.
func hostFailureCode(code errs.ErrCode) errs.ErrCode {
	switch code {
	case "transport.invalid_request":
		return errs.InvalidArgument
	case "system.internal":
		return errs.Internal
	case errs.Canceled, errs.InvalidArgument, errs.DeadlineExceeded, errs.NotFound, errs.AlreadyExists,
		errs.PermissionDenied, errs.ResourceExhausted, errs.FailedPrecondition, errs.Aborted, errs.OutOfRange,
		errs.Unimplemented, errs.Internal, errs.Unavailable, errs.DataLoss, errs.Unauthenticated, errs.Conflict,
		errs.ConnectionNotConfigured, errs.ConnectionNotConnected, errs.ConnectionReauthRequired,
		errs.ConnectionScopeMissing, errs.ConnectionUnavailable, errs.ConnectionConfiguration:
		return code
	default:
		return errs.Unknown
	}
}

// processLinkSentinelError keeps a remote cancellation or deadline failure
// matchable with errors.Is while preserving the callee's message.
type processLinkSentinelError struct {
	message  string
	sentinel error
}

func (e *processLinkSentinelError) Error() string { return e.message }

func (e *processLinkSentinelError) Unwrap() error { return e.sentinel }
