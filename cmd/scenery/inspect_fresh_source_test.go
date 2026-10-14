package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestInspectUsesCurrentSourceAfterSuccessfulProjection(t *testing.T) {
	t.Parallel()
	root := contractFixtureRoot(t)
	writeTestAppFile(t, root, ".scenery.json", `{"name":"house","id":"fresh-source-test"}`)
	if _, err := generate.GenerateTypeScriptClients(root, "typescript_client.public_api", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"models.json", "views.json"} {
		writeTestAppFile(t, root, filepath.Join(".scenery", "gen", name), `{"stale":"do not read"}`)
	}
	inspect := func() (machine.Envelope[graph.Diagnostic], error) {
		t.Helper()
		var output strings.Builder
		err := runSceneryInspect([]string{"endpoints", "--app-root", root, "-o", "json"}, &output)
		envelope, decodeErr := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return envelope, err
	}
	first, err := inspect()
	if err != nil || !first.OK {
		t.Fatalf("initial inspect: %v", err)
	}
	path := filepath.Join(root, "house", "package.scn")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(before), `"/house/process"`, `"/house/edited-process"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := inspect()
	data, marshalErr := json.Marshal(current.Data)
	if err != nil || marshalErr != nil || !strings.Contains(string(data), "/house/edited-process") {
		t.Fatalf("inspect ignored source edit: %v %s", err, data)
	}
	if current.WorkspaceRevision == nil || current.ContractRevision == nil || current.WorkspaceRevision == first.WorkspaceRevision {
		t.Fatalf("inspect did not report the captured revision: %#v", current)
	}
	if err := os.WriteFile(path, []byte(edited+"\nsetting \"bad\" { label = \"a\" hidden = true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invalid, err := inspect()
	if err == nil || invalid.OK || len(invalid.Diagnostics) == 0 {
		t.Fatalf("invalid current source received stale success: %v %#v", err, invalid)
	}
	for _, name := range []string{"models.json", "views.json"} {
		bytes, err := os.ReadFile(filepath.Join(root, ".scenery", "gen", name))
		if err != nil || string(bytes) != `{"stale":"do not read"}` {
			t.Fatalf("inspection modified private historical file: %v", err)
		}
	}
}
