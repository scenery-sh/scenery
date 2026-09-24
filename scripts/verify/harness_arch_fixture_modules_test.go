package main

import (
	"strings"
	"testing"
)

func TestCheckArchitectureFixtureModulesComparesRepositoryReplacements(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.27.0\n\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.11.0\n\tgithub.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect\n\tgolang.org/x/text v0.42.0\n)\n")
	fixture := func(rel, goVersion, requirements, replacement string) {
		writeTestAppFile(t, root, rel, "module example.com/fixture\n\ngo "+goVersion+"\n\nrequire scenery.sh v0.0.0\n\nrequire (\n"+requirements+")\n\nreplace scenery.sh => "+replacement+"\n")
	}
	stale := "\tgithub.com/jackc/pgx/v5 v5.10.0 // indirect\n\tgithub.com/jackc/pgservicefile v0.0.0-20230101000000-000000000000 // indirect\n\tgolang.org/x/text v0.42.0 // indirect\n"
	fixture("testdata/apps/stale/go.mod", "1.26.3", stale, "../../..")
	fixture("testdata/apps/current/go.mod", "1.27.0", "\tgolang.org/x/text v0.43.0 // indirect\n\tgithub.com/example/fixtureonly v0.1.0\n", "../../..")
	// Modules that do not inherit this repository's requirements are not fixtures.
	fixture("examples/elsewhere/go.mod", "1.26.3", stale, "../../other")
	writeTestAppFile(t, root, "internal/plain/testdata/go.mod", "module example.com/plain\n\ngo 1.26.3\n\nrequire github.com/jackc/pgx/v5 v5.10.0\n")
	writeTestAppFile(t, root, "internal/broken/testdata/go.mod", "module\n")

	checked, diagnostics := checkArchitectureFixtureModules(root, []string{
		"examples/elsewhere/go.mod",
		"internal/broken/testdata/go.mod",
		"internal/plain/testdata/go.mod",
		"testdata/apps/current/go.mod",
		"testdata/apps/stale/go.mod",
	})
	if checked != 2 {
		t.Fatalf("checked = %d, want 2", checked)
	}
	if len(diagnostics) != 1 || diagnostics[0].File != "testdata/apps/stale/go.mod" || diagnostics[0].Severity != "error" {
		t.Fatalf("diagnostics = %+v, want one error for the stale fixture", diagnostics)
	}
	message := diagnostics[0].Message
	for _, want := range []string{
		"go 1.26.3 < 1.27.0",
		"github.com/jackc/pgx/v5 v5.10.0 < v5.11.0",
		"github.com/jackc/pgservicefile v0.0.0-20230101000000-000000000000 < v0.0.0-20240606120523-5a60cdf6a761",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "golang.org/x/text") {
		t.Fatalf("message %q reports a requirement equal to the root", message)
	}
}
