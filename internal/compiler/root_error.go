package compiler

import "fmt"

// MissingAppError identifies a requested directory without the root declaration.
// It remains an invalid request across CLI and detached-build wrappers.
type MissingAppError struct{ Root string }

func (e *MissingAppError) Error() string {
	return fmt.Sprintf("invalid_request: %s does not contain app.scn", e.Root)
}

func (*MissingAppError) ExitCode() int { return 2 }
