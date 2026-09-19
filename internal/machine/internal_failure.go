package machine

import "sync/atomic"

// InternalFailure is the cause behind one report token. A public diagnostic
// carries only the token and a sanitized message; the process that minted the
// token hands the cause to its sink, so whoever runs that process can read what
// failed without the cause entering a contract payload.
type InternalFailure struct {
	ReportToken string
	Code        string
	Cause       string
}

var internalFailureSink atomic.Pointer[func(InternalFailure)]

// SetInternalFailureSink installs the process's receiver of internal failure
// causes. A process without a sink discards them.
func SetInternalFailureSink(sink func(InternalFailure)) {
	if sink == nil {
		internalFailureSink.Store(nil)
		return
	}
	internalFailureSink.Store(&sink)
}

// ReportInternalFailure hands the cause of a freshly minted report token to
// the process's sink. Every site that mints a token calls it once.
func ReportInternalFailure(token, code, cause string) {
	if sink := internalFailureSink.Load(); sink != nil && token != "" {
		(*sink)(InternalFailure{ReportToken: token, Code: code, Cause: cause})
	}
}
