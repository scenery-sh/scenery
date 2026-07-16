package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestDeployConfigInfoDiagnosticsReportsUnsetRoot(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "check-deploy-root")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{
		"name": "deploycheck",
		"frontends": {
			"web": { "root": "web" },
			"admin": { "root": "admin" }
		},
		"envs": {
			"local": {"default": true},
			"production": {
				"domain": "onlv.dev",
				"frontends": {"web": {"serve": "production"}, "admin": {"serve": "production"}},
				"deploy": {}
			}
		}
	}`,
	})
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := deployConfigInfoDiagnostics(root, cfg)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	diag := diagnostics[0]
	if diag.Stage != "config" || diag.Severity != "info" || !strings.Contains(diag.Message, "deploy.root is unset") {
		t.Fatalf("diagnostic = %+v", diag)
	}
}

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
