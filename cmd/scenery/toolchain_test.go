package main

import (
	"bytes"
	"strings"
	"testing"

	"scenery.sh/internal/toolchain"
)

func TestParseToolchainArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseToolchainArgs([]string{"verify", "-o", "json", "--tool", "victoria-metrics", "--platform", "linux/amd64", "--images", "--strict"})
	if err != nil {
		t.Fatalf("parseToolchainArgs() error = %v", err)
	}
	if opts.Command != "verify" || !opts.JSON || opts.Tool != "victoria-metrics" || opts.Platform.String() != "linux/amd64" || !opts.Images || !opts.Strict {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseToolchainArgs([]string{"path"}); err == nil {
		t.Fatal("expected path without --tool to fail")
	}
}

func TestRunToolchainListJSON(t *testing.T) {
	t.Setenv("SCENERY_TOOLCHAIN_DIR", t.TempDir())
	var out bytes.Buffer
	if err := runToolchain(t.Context(), &out, []string{"list", "-o", "json"}); err != nil {
		t.Fatalf("runToolchain list: %v", err)
	}
	var payload struct {
		Kind           string `json:"kind"`
		SchemaRevision string `json:"schema_revision"`
		Artifacts      []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"artifacts"`
		SourceLocks []struct {
			Name string `json:"name"`
		} `json:"source_locks"`
	}
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Kind != toolchain.StatusKind || payload.SchemaRevision != toolchain.StatusSchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
	if len(payload.Artifacts) == 0 || len(payload.SourceLocks) == 0 {
		t.Fatalf("payload missing artifacts or source locks: %+v", payload)
	}
}

func TestRenderToolchainStatusHidesPluginsByDefault(t *testing.T) {
	t.Parallel()

	status := toolchain.Status{
		ManifestSHA256: "abc123",
		StoreDir:       "/tmp/store",
		Platform:       "darwin/arm64",
		Artifacts: []toolchain.ArtifactStatus{
			{Name: "victoria-metrics", Kind: "binary", Version: "v1.145.0", Status: "missing"},
			{Name: "victoria-logs", Kind: "binary", Version: "v1.50.0", Status: "missing", ManagedPath: "/tmp/store/victoria-logs"},
		},
	}
	var out bytes.Buffer
	if err := renderToolchainStatus(&out, false, false, status); err != nil {
		t.Fatalf("renderToolchainStatus: %v", err)
	}
	if !strings.Contains(out.String(), "victoria-metrics v1.145.0 missing") || !strings.Contains(out.String(), "victoria-logs v1.50.0 missing") {
		t.Fatalf("default output missing binary artifacts:\n%s", out.String())
	}
	if strings.Contains(out.String(), "/tmp/store/victoria-logs") {
		t.Fatalf("default output included managed path:\n%s", out.String())
	}
	out.Reset()
	if err := renderToolchainStatus(&out, false, true, status); err != nil {
		t.Fatalf("renderToolchainStatus all: %v", err)
	}
	if !strings.Contains(out.String(), "/tmp/store/victoria-logs") {
		t.Fatalf("--all output omitted managed path:\n%s", out.String())
	}
}

func TestVersionJSONIncludesToolchainManifest(t *testing.T) {
	t.Parallel()

	resp := buildVersionResponse()
	if resp.Toolchain == nil {
		t.Fatal("toolchain manifest metadata missing")
	}
	if resp.Toolchain.Kind != toolchain.ManifestKind || resp.Toolchain.SchemaRevision != toolchain.ManifestSchemaRevision {
		t.Fatalf("toolchain identity = %q %q", resp.Toolchain.Kind, resp.Toolchain.SchemaRevision)
	}
	if len(resp.Toolchain.SHA256) != 64 {
		t.Fatalf("toolchain sha = %q", resp.Toolchain.SHA256)
	}
}

func TestRunToolchainPathJSON(t *testing.T) {
	t.Setenv("SCENERY_TOOLCHAIN_DIR", t.TempDir())
	var out bytes.Buffer
	if err := runToolchain(t.Context(), &out, []string{"path", "-o", "json", "--tool", "victoria-metrics"}); err != nil {
		t.Fatalf("runToolchain path: %v", err)
	}
	if !strings.Contains(out.String(), `"managed_path"`) {
		t.Fatalf("path output missing managed_path: %s", out.String())
	}
}

func TestRunToolchainUnknownToolFailsClosed(t *testing.T) {
	t.Setenv("SCENERY_TOOLCHAIN_DIR", t.TempDir())
	for _, args := range [][]string{
		{"sync", "-o", "json", "--tool", "missing"},
		{"path", "-o", "json", "--tool", "missing"},
	} {
		var out bytes.Buffer
		err := runToolchain(t.Context(), &out, args)
		if err == nil || !strings.Contains(err.Error(), `unknown toolchain artifact "missing"`) {
			t.Fatalf("runToolchain(%v) unknown tool error = %v", args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("runToolchain(%v) wrote output: %s", args, out.String())
		}
	}
}

func TestRunToolchainStrictImagesRejectsTagOnlyRefs(t *testing.T) {
	t.Setenv("SCENERY_TOOLCHAIN_DIR", t.TempDir())
	var out bytes.Buffer
	err := runToolchain(t.Context(), &out, []string{"verify", "-o", "json", "--tool", "victoria-metrics", "--images", "--strict"})
	if err == nil {
		t.Fatal("expected strict tag-only image verification to fail")
	}
	if !strings.Contains(out.String(), `"status":"invalid"`) {
		t.Fatalf("strict image output missing invalid status: %s", out.String())
	}
}
