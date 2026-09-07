package main

import (
	"errors"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

// cliDiagnosticError keeps an already constructed public diagnostic intact
// across orchestration boundaries, including its internal report token.
type cliDiagnosticError struct {
	err           error
	code          int
	diagnostic    graph.Diagnostic
	startupReason string
}

func (e *cliDiagnosticError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	message := e.diagnostic.Code + ": " + e.diagnostic.Message
	if e.diagnostic.ReportToken != "" {
		message += " (report " + e.diagnostic.ReportToken + ")"
	}
	return message
}

func (e *cliDiagnosticError) Unwrap() error { return e.err }
func (e *cliDiagnosticError) ExitCode() int { return e.code }

func cliErrorDiagnostic(err error) graph.Diagnostic {
	if reported, ok := errors.AsType[*cliDiagnosticError](err); ok {
		return reported.diagnostic
	}
	if reported, ok := errors.AsType[*build.ContractError](err); ok {
		return reported.Diagnostic
	}
	code := cliExitCode(err)
	kind, _, _ := strings.Cut(err.Error(), ":")
	switch code {
	case 2:
		kind = "invalid_request"
	case 3:
		if kind != "revision_conflict" {
			kind = "failed_precondition"
		}
	case 4:
		kind = "capability_unavailable"
	case 5:
		kind = "permission_denied"
	case 10:
		kind = "internal"
	}
	return compiler.TransportDiagnostic(kind, err.Error())
}

func preserveCLIDiagnostic(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*cliDiagnosticError](err); ok {
		return err
	}
	reported := &cliDiagnosticError{err: err, code: cliExitCode(err), diagnostic: cliErrorDiagnostic(err)}
	if _, silent := errors.AsType[*silentCLIError](err); silent {
		return &silentCLIError{err: reported, code: reported.code}
	}
	return reported
}
