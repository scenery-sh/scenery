package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
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

func TestRunSceneryCheckCompilesPersistentFixture(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "check-compile-smoke")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json":  `{"name":"checksmoke"}`,
		"go.mod":         "module example.com/checksmoke\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + filepath.ToSlash(repoRootForTest(t)) + "\n",
		"service/api.go": "package service\n\nimport \"context\"\n\ntype PingResponse struct { OK bool `json:\"ok\"` }\n\n//scenery:api public method=GET path=/ping\nfunc Ping(context.Context) (*PingResponse, error) { return &PingResponse{OK: true}, nil }\n",
	})

	var out bytes.Buffer
	if err := runSceneryCheck(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runSceneryCheck: %v\n%s", err, out.String())
	}
	var payload checkResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Diagnostics) != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}
