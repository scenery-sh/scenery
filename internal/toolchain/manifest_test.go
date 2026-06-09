package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDockerRunner struct {
	present map[string]bool
	pulls   []string
	err     error
}

func (f *fakeDockerRunner) InspectImage(_ context.Context, ref string) error {
	if f.err != nil {
		return f.err
	}
	if f.present[ref] {
		return nil
	}
	return fmt.Errorf("No such image: %s", ref)
}

func (f *fakeDockerRunner) PullImage(_ context.Context, ref string) error {
	if f.err != nil {
		return f.err
	}
	f.pulls = append(f.pulls, ref)
	if f.present == nil {
		f.present = map[string]bool{}
	}
	f.present[ref] = true
	return nil
}

func TestBundledManifestMatchesRootFile(t *testing.T) {
	rootData, err := os.ReadFile(filepath.Join("..", "..", "onlava.toolchain.json"))
	if err != nil {
		t.Fatalf("read root manifest: %v", err)
	}
	if string(rootData) != string(BundledManifestBytes()) {
		t.Fatal("bundled toolchain manifest differs from onlava.toolchain.json")
	}
	if _, err := LoadBundledManifest(); err != nil {
		t.Fatalf("LoadBundledManifest() error = %v", err)
	}
}

func TestBundledManifestDeclaresNeonSelfhostUmbrella(t *testing.T) {
	manifest, err := LoadBundledManifest()
	if err != nil {
		t.Fatalf("LoadBundledManifest() error = %v", err)
	}
	artifact, ok := manifest.Artifact("neon-selfhost")
	if !ok {
		t.Fatal("neon-selfhost artifact missing")
	}
	if artifact.Kind != "image" {
		t.Fatalf("neon-selfhost kind = %q, want image", artifact.Kind)
	}
	refs := map[string]bool{}
	for _, image := range artifact.Images {
		refs[image.Ref] = true
	}
	for _, want := range []string{
		"ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f",
		"ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249",
		"quay.io/minio/minio:RELEASE.2022-10-20T00-55-09Z",
		"minio/mc@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727",
	} {
		if !refs[want] {
			t.Fatalf("neon-selfhost images missing %s: %+v", want, artifact.Images)
		}
	}
}

func TestParseManifestRejectsUnknownFields(t *testing.T) {
	_, err := ParseManifest([]byte(`{
		"schema_version":"onlava.toolchain.v1",
		"manifest_version":1,
		"source_locks":[],
		"artifacts":[],
		"extra":true
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseManifest unknown field error = %v", err)
	}
}

func TestParseManifestRejectsTrailingJSON(t *testing.T) {
	_, err := ParseManifest([]byte(`{
		"schema_version":"onlava.toolchain.v1",
		"manifest_version":1,
		"source_locks":[],
		"artifacts":[]
	} {}`))
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("ParseManifest trailing JSON error = %v", err)
	}
}

func TestParseManifestRejectsInvalidPlatform(t *testing.T) {
	_, err := ParseManifest([]byte(`{
		"schema_version":"onlava.toolchain.v1",
		"manifest_version":1,
		"source_locks":[],
		"artifacts":[{
			"name":"demo",
			"kind":"binary",
			"version":"1",
			"default_binary":"demo",
			"platforms":{
				"linux":{"archive":"tar.gz","url":"https://example.com/demo.tar.gz","sha256":"0000000000000000000000000000000000000000000000000000000000000000","extract":"demo"}
			}
		}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "invalid platform") {
		t.Fatalf("ParseManifest invalid platform error = %v", err)
	}
}

func TestStoreSyncAndVerify(t *testing.T) {
	archive := testTarGz(t, map[string]string{"demo": "#!/bin/sh\nexit 0\n"})
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	manifest := testManifest(server.URL, hex.EncodeToString(sum[:]))
	store, err := NewStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Platform = Platform{GOOS: "linux", GOARCH: "amd64"}

	status, err := store.Sync(context.Background(), Options{Platform: store.Platform, Tool: "demo"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(status.Artifacts) != 1 || status.Artifacts[0].Status != "installed" {
		t.Fatalf("Sync status = %+v", status.Artifacts)
	}
	if !isExecutableFile(status.Artifacts[0].ManagedPath) {
		t.Fatalf("managed path is not executable: %s", status.Artifacts[0].ManagedPath)
	}

	status, err = store.Verify(context.Background(), Options{Platform: store.Platform, Tool: "demo"})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if status.Artifacts[0].Status != "installed" {
		t.Fatalf("Verify status = %+v", status.Artifacts[0])
	}
}

func TestStorePathChangesWithArtifactVersion(t *testing.T) {
	manifest := testManifest("https://example.com/demo.tar.gz", strings.Repeat("0", 64))
	store, err := NewStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	first, err := store.Path(context.Background(), "demo", platform)
	if err != nil {
		t.Fatalf("Path(first) error = %v", err)
	}
	manifest.Artifacts[0].Version = "2.0.0"
	store, err = NewStore(store.Dir, manifest)
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	second, err := store.Path(context.Background(), "demo", platform)
	if err != nil {
		t.Fatalf("Path(second) error = %v", err)
	}
	if first.ManagedPath == second.ManagedPath {
		t.Fatalf("managed path did not change with version: %s", first.ManagedPath)
	}
	if !strings.Contains(second.ManagedPath, "2.0.0") {
		t.Fatalf("managed path does not include new version: %s", second.ManagedPath)
	}
}

func TestStoreSyncHonorsDownloadDisable(t *testing.T) {
	store, err := NewStore(t.TempDir(), testManifest("https://example.com/demo.tar.gz", strings.Repeat("0", 64)))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Platform = Platform{GOOS: "linux", GOARCH: "amd64"}
	t.Setenv("ONLAVA_TOOLCHAIN_DOWNLOAD", "0")
	_, err = store.Sync(context.Background(), Options{Platform: store.Platform, Tool: "demo"})
	if err == nil || !strings.Contains(err.Error(), "ONLAVA_TOOLCHAIN_DOWNLOAD=0") {
		t.Fatalf("Sync download-disabled error = %v", err)
	}
}

func TestStoreUnknownToolFailsClosed(t *testing.T) {
	store, err := NewStore(t.TempDir(), testManifest("https://example.com/demo.tar.gz", strings.Repeat("0", 64)))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	cases := map[string]func() error{
		"list": func() error {
			_, err := store.List(context.Background(), Options{Tool: "missing"})
			return err
		},
		"verify": func() error {
			_, err := store.Verify(context.Background(), Options{Tool: "missing"})
			return err
		},
		"sync": func() error {
			_, err := store.Sync(context.Background(), Options{Tool: "missing"})
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), `unknown toolchain artifact "missing"`) {
				t.Fatalf("%s unknown tool error = %v", name, err)
			}
		})
	}
}

func TestStoreSyncSourceBuildArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sourcebuild\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmdDir := filepath.Join(root, "cmd", "demo")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	manifest := Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: 1,
		Artifacts: []Artifact{{
			Name:          "demo-source",
			Kind:          "binary",
			Version:       "dev",
			DefaultBinary: "demo-source",
			SourceBuild: &SourceBuildArtifact{
				Kind:    "go",
				Package: "./cmd/demo",
			},
		}},
	}
	store, err := NewStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.RootDir = root
	store.Platform = Platform{GOOS: "linux", GOARCH: "amd64"}

	status, err := store.Sync(context.Background(), Options{RootDir: root, Platform: store.Platform, Tool: "demo-source"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got := status.Artifacts[0].Status; got != "installed" {
		t.Fatalf("source build status = %q, want installed: %+v", got, status.Artifacts[0])
	}
	if !isExecutableFile(status.Artifacts[0].ManagedPath) {
		t.Fatalf("managed path is not executable: %s", status.Artifacts[0].ManagedPath)
	}
	if status.Artifacts[0].Source != "source-build" {
		t.Fatalf("source = %q", status.Artifacts[0].Source)
	}

	status, err = store.Verify(context.Background(), Options{RootDir: root, Platform: store.Platform, Tool: "demo-source"})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := status.Artifacts[0].Status; got != "installed" {
		t.Fatalf("verify status = %q, want installed: %+v", got, status.Artifacts[0])
	}
}

func TestParseManifestRejectsInvalidSourceBuildPackage(t *testing.T) {
	_, err := ParseManifest([]byte(`{
		"schema_version":"onlava.toolchain.v1",
		"manifest_version":1,
		"source_locks":[],
		"artifacts":[{
			"name":"demo",
			"kind":"binary",
			"version":"dev",
			"default_binary":"demo",
			"source_build":{"kind":"go","package":"../cmd/demo"}
		}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "source_build missing valid package") {
		t.Fatalf("ParseManifest invalid source_build error = %v", err)
	}
}

func TestStoreRejectsArchiveTraversal(t *testing.T) {
	archive := testTarGz(t, map[string]string{"../demo": "#!/bin/sh\nexit 0\n"})
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	store, err := NewStore(t.TempDir(), testManifest(server.URL, hex.EncodeToString(sum[:])))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Platform = Platform{GOOS: "linux", GOARCH: "amd64"}
	err = store.installArtifact(context.Background(), store.Manifest.Artifacts[0], store.Platform)
	if err == nil || !strings.Contains(err.Error(), "did not contain expected path") {
		t.Fatalf("install traversal archive error = %v", err)
	}
}

func TestStoreImageStatusAndSyncUseDockerRunner(t *testing.T) {
	manifest := Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: 1,
		Artifacts: []Artifact{{
			Name:    "postgres",
			Kind:    "image",
			Version: "18",
			Images: []ImageArtifact{{
				Ref:    "postgres:18",
				Digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Usage:  "dev.services.postgres",
			}},
		}},
	}
	store, err := NewStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	docker := &fakeDockerRunner{present: map[string]bool{}}
	store.Docker = docker

	status, err := store.Verify(context.Background(), Options{Tool: "postgres", Images: true})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := status.Artifacts[0].Images[0].Status; got != "missing" {
		t.Fatalf("image status = %q, want missing", got)
	}

	status, err = store.Sync(context.Background(), Options{Tool: "postgres", Images: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(docker.pulls) != 1 || docker.pulls[0] != "postgres@sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("docker pulls = %+v", docker.pulls)
	}
	if got := status.Artifacts[0].Images[0].Status; got != "present" {
		t.Fatalf("post-sync image status = %q, want present", got)
	}
}

func TestStrictImageStatusRejectsTagOnlyRefs(t *testing.T) {
	manifest := Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: 1,
		Artifacts: []Artifact{{
			Name:    "postgres",
			Kind:    "image",
			Version: "18",
			Images: []ImageArtifact{{
				Ref: "postgres:18",
			}},
		}},
	}
	store, err := NewStore(t.TempDir(), manifest)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Docker = &fakeDockerRunner{present: map[string]bool{"postgres:18": true}}
	status, err := store.Verify(context.Background(), Options{Tool: "postgres", Images: true, Strict: true})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := status.Artifacts[0].Images[0].Status; got != "invalid" {
		t.Fatalf("strict tag-only image status = %q, want invalid", got)
	}
}

func testManifest(url, sha string) Manifest {
	return Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ManifestVersion: 1,
		Artifacts: []Artifact{{
			Name:          "demo",
			Kind:          "binary",
			Version:       "1.0.0",
			DefaultBinary: "demo",
			Binaries:      []string{"demo"},
			Platforms: map[string]PlatformArtifact{
				"linux/amd64": {
					Archive: "tar.gz",
					URL:     url,
					SHA256:  sha,
					Extract: "demo",
				},
			},
		}},
	}
}

func testTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}
