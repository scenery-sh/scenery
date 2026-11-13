package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/filemeta"
)

func TestStoreAdoptsSharedArtifactWithoutDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filemeta.Clone(filepath.Join(root, "probe"), filepath.Join(root, "probe-clone")); err != nil {
		t.Skipf("filesystem does not clone: %v", err)
	}
	archive := testTarGz(t, map[string]string{"demo": "#!/bin/sh\nexit 0\n"})
	sum := sha256.Sum256(archive)
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	SetSharedArtifactStore(filepath.Join(root, "shared"))
	t.Cleanup(func() { SetSharedArtifactStore("") })

	manifest := testManifest(server.URL, hex.EncodeToString(sum[:]))
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	sync := func(name string) ArtifactStatus {
		t.Helper()
		store, err := NewStore(filepath.Join(root, name), manifest)
		if err != nil {
			t.Fatal(err)
		}
		store.Platform = platform
		status, err := store.Sync(context.Background(), Options{Platform: platform})
		if err != nil {
			t.Fatalf("Sync(%s) error = %v", name, err)
		}
		if got := status.Artifacts[0].Status; got != "installed" {
			t.Fatalf("Sync(%s) status = %q: %+v", name, got, status.Artifacts[0])
		}
		return status.Artifacts[0]
	}
	first := sync("first")
	second := sync("second")
	if downloads != 1 {
		t.Fatalf("downloads = %d, want only the first store to download", downloads)
	}
	if first.ManagedPath == second.ManagedPath || !isExecutableFile(second.ManagedPath) {
		t.Fatalf("second store has no own executable: %s", second.ManagedPath)
	}

	// A different declared archive names another shared copy.
	other := testManifest(server.URL+"/other", hex.EncodeToString(sum[:]))
	store, err := NewStore(filepath.Join(root, "third"), other)
	if err != nil {
		t.Fatal(err)
	}
	store.Platform = platform
	if _, err := store.Sync(context.Background(), Options{Platform: platform}); err != nil {
		t.Fatal(err)
	}
	if downloads != 2 {
		t.Fatalf("downloads = %d, want a changed platform entry to install from its source", downloads)
	}
}
