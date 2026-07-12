package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestDeployConfigInfoDiagnosticsReportsUnsetRoot(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "check-deploy-root")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{
		"name": "deploycheck",
		"deploy": { "domain": "onlv.dev" },
		"frontends": {
			"web": { "root": "web" },
			"admin": { "root": "admin" }
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
	var payload vnextEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode v1 envelope: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Diagnostics) != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}
