package toolchain

import "testing"

func TestBundledManifestPinsManagedNodeHome(t *testing.T) {
	manifest, err := LoadBundledManifest()
	if err != nil {
		t.Fatalf("LoadBundledManifest() error = %v", err)
	}
	node, ok := manifest.Artifact("node")
	if !ok {
		t.Fatal("bundled manifest has no node artifact")
	}
	if node.Kind != "binary" || node.Version != "24.18.0" || node.DefaultBinary != "node" {
		t.Fatalf("node artifact identity = %+v", node)
	}
	if len(node.Binaries) != 2 || node.Binaries[0] != "node" || node.Binaries[1] != "npm" {
		t.Fatalf("node binaries = %v", node.Binaries)
	}
	wants := map[string]struct {
		url string
		sha string
	}{
		"linux/amd64": {
			url: "https://nodejs.org/download/release/v24.18.0/node-v24.18.0-linux-x64.tar.gz",
			sha: "783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8",
		},
		"darwin/arm64": {
			url: "https://nodejs.org/download/release/v24.18.0/node-v24.18.0-darwin-arm64.tar.gz",
			sha: "e1a97e14c99c803e96c7339403282ea05a499c32f8d83defe9ef5ec66f979ed1",
		},
	}
	if len(node.Platforms) != len(wants) {
		t.Fatalf("node platforms = %+v", node.Platforms)
	}
	for platform, want := range wants {
		entry, ok := node.Platforms[platform]
		if !ok {
			t.Fatalf("node platform %s is missing", platform)
		}
		if entry.URL != want.url || entry.SHA256 != want.sha || entry.Archive != "tar.gz" || entry.Extract != "bin/node" || !entry.Home || entry.StripComponents != 1 {
			t.Fatalf("node platform %s = %+v", platform, entry)
		}
	}
}
