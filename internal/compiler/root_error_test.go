package compiler

import (
	"errors"
	"testing"
)

func TestMissingAppDeclarationIsAnInvalidRequest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := Compile(root)
	var missing *MissingAppError
	if !errors.As(err, &missing) || missing.Root != root || missing.ExitCode() != 2 {
		t.Fatalf("missing root declaration = %v", err)
	}
}
