package wiremodel

import (
	"os"
	"path/filepath"
	"testing"

	appcfg "onlava.com/internal/app"
	"onlava.com/internal/parse"
)

func TestAppCapabilitiesMarksUnsupportedEndpointJSONOnly(t *testing.T) {
	root := t.TempDir()
	writeWireModelTestFile(t, root, "go.mod", "module example.com/wiretest\n\ngo 1.26.0\n\nrequire onlava.com v0.0.0\n\nreplace onlava.com => "+appcfg.RepoRoot()+"\n")
	writeWireModelTestFile(t, root, ".onlava.json", `{"name":"wiretest"}`)
	writeWireModelTestFile(t, root, "svc/api.go", `package svc

import "context"

type SupportedRequest struct {
	Name string `+"`json:\"name\"`"+`
}

type UnsupportedResponse struct {
	Meta map[string]any `+"`json:\"meta\"`"+`
}

//onlava:api public
func Supported(ctx context.Context, req *SupportedRequest) (*SupportedRequest, error) {
	return req, nil
}

//onlava:api public
func Unsupported(ctx context.Context) (*UnsupportedResponse, error) {
	return &UnsupportedResponse{}, nil
}
`)
	model, err := parse.App(root, "wiretest")
	if err != nil {
		t.Fatalf("parse.App() error = %v", err)
	}
	caps := AppCapabilities(model)
	if got := caps.Endpoints["svc.Supported"]; !got.Available {
		t.Fatalf("supported endpoint = %+v", got)
	}
	if got := caps.Endpoints["svc.Unsupported"]; got.Available || got.UnsupportedReason == "" {
		t.Fatalf("unsupported endpoint = %+v", got)
	}
	if caps.SchemaHash == "" {
		t.Fatal("schema hash is empty")
	}
}

func writeWireModelTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
