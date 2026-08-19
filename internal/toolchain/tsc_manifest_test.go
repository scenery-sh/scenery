package toolchain

import "testing"

func TestBundledManifestPinsManagedTypeScript7Tsc(t *testing.T) {
	manifest, err := LoadBundledManifest()
	if err != nil {
		t.Fatalf("LoadBundledManifest() error = %v", err)
	}
	if _, ok := manifest.Artifact("tsgo"); ok {
		t.Fatal("bundled manifest still declares leftover tsgo artifact")
	}
	tsc, ok := manifest.Artifact("tsc")
	if !ok {
		t.Fatal("bundled manifest has no tsc artifact")
	}
	if tsc.Kind != "binary" || tsc.Version != "7.0.2" || tsc.DefaultBinary != "tsc" {
		t.Fatalf("tsc artifact identity = %+v", tsc)
	}
	if len(tsc.Binaries) != 1 || tsc.Binaries[0] != "tsc" {
		t.Fatalf("tsc binaries = %v", tsc.Binaries)
	}
	wants := map[string]struct {
		url string
		sha string
	}{
		"linux/amd64": {
			url: "https://registry.npmjs.org/@typescript/typescript-linux-x64/-/typescript-linux-x64-7.0.2.tgz",
			sha: "7ecad6f67377e831856367ab062ef394f21506a611405bf8ac0ff039348637d3",
		},
		"darwin/arm64": {
			url: "https://registry.npmjs.org/@typescript/typescript-darwin-arm64/-/typescript-darwin-arm64-7.0.2.tgz",
			sha: "902e2fe1cf0799198ef902c6b8c310a450fef629a6baba41d45641ef75c04ebd",
		},
	}
	if len(tsc.Platforms) != len(wants) {
		t.Fatalf("tsc platforms = %+v", tsc.Platforms)
	}
	for platform, want := range wants {
		entry, ok := tsc.Platforms[platform]
		if !ok {
			t.Fatalf("tsc platform %s is missing", platform)
		}
		if entry.URL != want.url || entry.SHA256 != want.sha || entry.Archive != "tar.gz" || entry.Extract != "lib/tsc" || !entry.Home || entry.StripComponents != 1 {
			t.Fatalf("tsc platform %s = %+v", platform, entry)
		}
	}
}
