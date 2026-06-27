package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDevNamedLockSecondProcessTimesOutWithNamedError(t *testing.T) {
	root := t.TempDir()
	restore := setDevLockTestTiming(io.Discard)
	defer restore()
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	cmd := exec.Command(os.Args[0], "-test.run=TestDevNamedLockSubprocessAcquireTimeout", "--", root)
	cmd.Env = append(os.Environ(), "SCENERY_LOCK_TEST_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("subprocess lock unexpectedly succeeded:\n%s", output)
	}
	got := string(output)
	for _, want := range []string{
		"waiting for database branch registry lock at",
		"timed out waiting for database branch registry lock",
		dbBranchRegistryLockPath(root),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subprocess output missing %q:\n%s", want, got)
		}
	}
}

func TestDevNamedLockSubprocessAcquireTimeout(t *testing.T) {
	if os.Getenv("SCENERY_LOCK_TEST_HELPER") != "1" {
		return
	}
	if len(os.Args) == 0 {
		fmt.Fprintln(os.Stderr, "missing os.Args")
		os.Exit(2)
	}
	root := os.Args[len(os.Args)-1]
	restore := setDevLockTestTiming(os.Stdout)
	defer restore()
	unlock, err := lockDBBranchRegistry(root)
	if err == nil {
		unlock()
		os.Exit(0)
	}
	fmt.Fprintln(os.Stdout, err)
	os.Exit(2)
}

func TestDevNamedLockRejectsSubstrateAcquireWhileRegistryHeld(t *testing.T) {
	restore := setDevLockTestTiming(io.Discard)
	defer restore()
	root := t.TempDir()
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	substrateUnlock, err := lockManagedSubstrateRoot(root, "sqlite")
	if err == nil {
		substrateUnlock()
		t.Fatal("substrate lock succeeded while registry lock was held")
	}
	if !strings.Contains(err.Error(), "lock ordering violation") ||
		!strings.Contains(err.Error(), "database branch registry") ||
		!strings.Contains(err.Error(), "shared substrate sqlite") {
		t.Fatalf("ordering error = %v", err)
	}
}

func TestDevNamedLockRejectsBranchOperationAcquireWhileRegistryHeld(t *testing.T) {
	restore := setDevLockTestTiming(io.Discard)
	defer restore()
	root := t.TempDir()
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	operationUnlock, err := lockDBBranchOperation(root, "demo")
	if err == nil {
		operationUnlock()
		t.Fatal("branch operation lock succeeded while registry lock was held")
	}
	if !strings.Contains(err.Error(), "lock ordering violation") ||
		!strings.Contains(err.Error(), "database branch registry") ||
		!strings.Contains(err.Error(), "database branch operation demo") {
		t.Fatalf("ordering error = %v", err)
	}
}

func setDevLockTestTiming(writer io.Writer) func() {
	oldRetry := devLockRetryInterval
	oldWarn := devLockWarnAfter
	oldTimeout := devLockTimeout
	oldWriter := devLockWarnWriter
	devLockRetryInterval = 10 * time.Millisecond
	devLockWarnAfter = 20 * time.Millisecond
	devLockTimeout = 120 * time.Millisecond
	devLockWarnWriter = writer
	return func() {
		devLockRetryInterval = oldRetry
		devLockWarnAfter = oldWarn
		devLockTimeout = oldTimeout
		devLockWarnWriter = oldWriter
	}
}
