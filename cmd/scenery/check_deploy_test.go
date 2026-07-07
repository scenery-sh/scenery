package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSceneryCheckReportsDeployRootInfo(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{
		"name": "deploycheck",
		"deploy": { "domain": "onlv.dev" },
		"proxy": {
			"frontends": {
				"web": { "host": "web" },
				"admin": { "host": "admin" }
			}
		}
	}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/deploycheck\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+filepath.ToSlash(repoRootForTest(t))+"\n")
	writeTestAppFile(t, root, "service/api.go", "package service\n\nimport \"context\"\n\ntype PingResponse struct { OK bool `json:\"ok\"` }\n\n//scenery:api public method=GET path=/ping\nfunc Ping(context.Context) (*PingResponse, error) { return &PingResponse{OK: true}, nil }\n")

	var out bytes.Buffer
	if err := runSceneryCheck(context.Background(), &out, []string{"--app-root", root, "--json"}); err != nil {
		t.Fatalf("runSceneryCheck: %v\n%s", err, out.String())
	}
	var payload checkResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || len(payload.Diagnostics) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	diag := payload.Diagnostics[0]
	if diag.Stage != "config" || diag.Severity != "info" || !strings.Contains(diag.Message, "deploy.root is unset") {
		t.Fatalf("diagnostic = %+v", diag)
	}
}
