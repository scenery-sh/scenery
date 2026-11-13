package main

import (
	"bytes"
	"context"
	"testing"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestRunSceneryCheckAcceptsNativePersistentFixture(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "check-compile-smoke")
	preparePersistentTestApp(t, root, nativeHarnessTestFiles(t, "checksmoke", "return nil"))

	var out bytes.Buffer
	if err := runSceneryCheck(context.Background(), &out, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("runSceneryCheck: %v\n%s", err, out.String())
	}
	payload, err := machine.Decode[graph.Diagnostic](out.Bytes(), currentMachineSpecRevision())
	if err != nil {
		t.Fatalf("decode v1 envelope: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Diagnostics) != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}
