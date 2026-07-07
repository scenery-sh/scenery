package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestParseUpgradeArgs(t *testing.T) {
	t.Parallel()

	if got := upgradeArchiveName("v9.9.9"); strings.Contains(got, "_v9.9.9_") || !strings.Contains(got, "_9.9.9_") {
		t.Fatalf("upgradeArchiveName kept tag prefix: %q", got)
	}

	opts, err := parseUpgradeArgs([]string{"--version", "v9.9.9", "--target", "/tmp/scenery", "--toolchain", "all", "--force", "--dry-run", "--json"})
	if err != nil {
		t.Fatalf("parseUpgradeArgs() error = %v", err)
	}
	if opts.Version != "v9.9.9" || opts.Target != "/tmp/scenery" || opts.ToolchainMode != "all" || !opts.Force || !opts.DryRun || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	opts, err = parseUpgradeArgs([]string{"--skip-toolchain"})
	if err != nil {
		t.Fatalf("parseUpgradeArgs(skip) error = %v", err)
	}
	if opts.Version != "latest" || opts.ToolchainMode != "none" {
		t.Fatalf("skip opts = %+v", opts)
	}
	if _, err := parseUpgradeArgs([]string{"--toolchain", "maybe"}); err == nil {
		t.Fatal("expected invalid toolchain mode to fail")
	}
}

func TestRunUpgradeInstallsVerifiedReleaseAndSyncsToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake downloaded binary")
	}
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	marker := filepath.Join(t.TempDir(), "sync.args")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"system\" ] && [ \"$2\" = \"toolchain\" ] && [ \"$3\" = \"sync\" ]; then\n" +
		"  printf '%s\\n' \"$*\" > \"$UPGRADE_TEST_MARKER\"\n" +
		"  echo '{\"schema_version\":\"scenery.toolchain.status.v1\",\"artifacts\":[]}'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo 'fake scenery'\n"
	archiveData := buildUpgradeArchive(t, []byte(script))
	sum := sha256.Sum256(archiveData)
	assets := map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	}
	server := newUpgradeReleaseServer(t, tag, assets)
	restore := overrideUpgradeGlobals(t, server, "v0.2.0")
	defer restore()
	t.Setenv("UPGRADE_TEST_MARKER", marker)

	target := filepath.Join(t.TempDir(), "bin", "scenery")
	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--target", target, "--toolchain", "all", "--json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || !payload.Installed || payload.TargetVersion != tag || payload.AssetName != assetName {
		t.Fatalf("payload = %+v", payload)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("installed target: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("installed target is not executable: %v", info.Mode())
	}
	args, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("toolchain marker: %v", err)
	}
	if strings.TrimSpace(string(args)) != "system toolchain sync --json --images" {
		t.Fatalf("toolchain args = %q", string(args))
	}
	if payload.Toolchain == nil || payload.Toolchain.Mode != "all" || len(payload.Toolchain.Synced) != 1 {
		t.Fatalf("toolchain payload = %+v", payload.Toolchain)
	}
}

func TestRunUpgradeSkipsCurrentVersionForDefaultTarget(t *testing.T) {
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	archiveData := buildUpgradeArchive(t, []byte("new binary"))
	sum := sha256.Sum256(archiveData)
	server := newUpgradeReleaseServer(t, tag, map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	})
	restore := overrideUpgradeGlobals(t, server, tag)
	defer restore()

	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--skip-toolchain", "--json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || !payload.Skipped || payload.Installed {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunUpgradeInstallsExplicitTargetEvenWhenCurrentVersionMatches(t *testing.T) {
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	archiveData := buildUpgradeArchive(t, []byte("new binary"))
	sum := sha256.Sum256(archiveData)
	server := newUpgradeReleaseServer(t, tag, map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	})
	restore := overrideUpgradeGlobals(t, server, tag)
	defer restore()
	target := filepath.Join(t.TempDir(), "scenery")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--target", target, "--skip-toolchain", "--json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if !payload.OK || !payload.Installed || payload.Skipped {
		t.Fatalf("payload = %+v", payload)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "old binary" {
		t.Fatalf("target was not replaced: %q", string(data))
	}
}

func TestRunUpgradeRejectsChecksumMismatch(t *testing.T) {
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	server := newUpgradeReleaseServer(t, tag, map[string][]byte{
		assetName:       buildUpgradeArchive(t, []byte("binary")),
		"checksums.txt": []byte(strings.Repeat("0", 64) + "  " + assetName + "\n"),
	})
	restore := overrideUpgradeGlobals(t, server, "v0.2.0")
	defer restore()
	target := filepath.Join(t.TempDir(), "scenery")

	var out bytes.Buffer
	err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--target", target, "--skip-toolchain", "--json"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if jsonErr := json.Unmarshal(out.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", jsonErr, out.String())
	}
	if payload.OK || payload.Error == "" {
		t.Fatalf("payload = %+v", payload)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after failed upgrade: %v", statErr)
	}
}

func TestUpgradeAddsDeploySetupNoticeWhenHelperContractDrifts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake downloaded binary")
	}
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	archiveData := buildUpgradeArchive(t, []byte("#!/bin/sh\necho fake scenery\n"))
	sum := sha256.Sum256(archiveData)
	server := newUpgradeReleaseServer(t, tag, map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	})
	restore := overrideUpgradeGlobals(t, server, "v0.2.0")
	oldNotice := upgradeDeployNoticeFunc
	defer func() {
		upgradeDeployNoticeFunc = oldNotice
		restore()
	}()
	upgradeDeployNoticeFunc = func(targetVersion string) *deployHelperDrift {
		return &deployHelperDrift{
			HelperInstalled: true,
			ActionRequired:  true,
			HelperVersion:   "v0.2.0",
			CurrentVersion:  targetVersion,
			TargetSchema:    "scenery.edge.target.old",
			ExpectedSchema:  deployHelperContractVersion,
			Message:         "edge target metadata is scenery.edge.target.old; current binary expects " + deployHelperContractVersion,
			SuggestedAction: "Run `scenery deploy setup` to update the privileged listener (asks for sudo).",
		}
	}

	target := filepath.Join(t.TempDir(), "scenery")
	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--target", target, "--skip-toolchain"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "deploy helper: Run `scenery deploy setup` to update the privileged listener (asks for sudo).") {
		t.Fatalf("upgrade output missing deploy notice:\n%s", out.String())
	}

	out.Reset()
	if err := runUpgrade(t.Context(), &out, []string{"--version", tag, "--target", target, "--force", "--skip-toolchain", "--json"}); err != nil {
		t.Fatalf("runUpgrade json error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.Deploy == nil || !payload.Deploy.ActionRequired || payload.Deploy.ExpectedSchema != deployHelperContractVersion {
		t.Fatalf("deploy notice = %+v", payload.Deploy)
	}
}

func TestDeployHelperDriftRequiresSetupOnlyForContractDrift(t *testing.T) {
	paths := localagent.PathsForHome(t.TempDir())
	if err := localagent.WriteEdgeTargetState(paths.EdgeTargetPath, localagent.EdgeTargetState{
		SchemaVersion: "scenery.edge.target.old",
		Kind:          localagent.EdgeKindCaddy,
		TargetAddr:    "127.0.0.1:19443",
	}); err != nil {
		t.Fatalf("WriteEdgeTargetState: %v", err)
	}
	helper := edgeStatusPrivilegedListener{
		Installed:  true,
		Version:    "v1.0.0",
		TargetPath: paths.EdgeTargetPath,
	}
	drift := deployHelperDriftFor(paths, helper, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.SuggestedAction, "deploy setup") {
		t.Fatalf("contract drift = %+v", drift)
	}

	if err := localagent.WriteEdgeTargetState(paths.EdgeTargetPath, localagent.EdgeTargetState{
		SchemaVersion: deployHelperContractVersion,
		Kind:          localagent.EdgeKindCaddy,
		TargetAddr:    "127.0.0.1:19443",
	}); err != nil {
		t.Fatalf("WriteEdgeTargetState current: %v", err)
	}
	drift = deployHelperDriftFor(paths, helper, "v2.0.0")
	if drift.ActionRequired || !strings.Contains(drift.Message, "helper is v1.0.0") {
		t.Fatalf("version-only drift = %+v", drift)
	}
}

func buildUpgradeArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	name := "scenery"
	if runtime.GOOS == "windows" {
		name += ".exe"
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		header.Modified = time.Unix(0, 0)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(binary); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(binary)), ModTime: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newUpgradeReleaseServer(t *testing.T, tag string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/scenery-sh/scenery/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		writeUpgradeRelease(t, w, tag, assets, r.Host)
	})
	mux.HandleFunc("/repos/scenery-sh/scenery/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		writeUpgradeRelease(t, w, tag, assets, r.Host)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/download/")
		data, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	return httptest.NewServer(mux)
}

func writeUpgradeRelease(t *testing.T, w http.ResponseWriter, tag string, assets map[string][]byte, host string) {
	t.Helper()
	type asset struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}
	payload := struct {
		TagName string  `json:"tag_name"`
		Assets  []asset `json:"assets"`
	}{
		TagName: tag,
	}
	for name := range assets {
		payload.Assets = append(payload.Assets, asset{Name: name, BrowserDownloadURL: fmt.Sprintf("http://%s/download/%s", host, name)})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

func overrideUpgradeGlobals(t *testing.T, server *httptest.Server, currentVersion string) func() {
	t.Helper()
	oldBase := upgradeAPIBaseURL
	oldClient := upgradeHTTPClient
	oldVersion := upgradeCurrentVersionFn
	oldNotice := upgradeDeployNoticeFunc
	upgradeAPIBaseURL = server.URL + "/repos/scenery-sh/scenery"
	upgradeHTTPClient = server.Client()
	upgradeCurrentVersionFn = func() string { return currentVersion }
	upgradeDeployNoticeFunc = func(targetVersion string) *deployHelperDrift { return nil }
	return func() {
		upgradeAPIBaseURL = oldBase
		upgradeHTTPClient = oldClient
		upgradeCurrentVersionFn = oldVersion
		upgradeDeployNoticeFunc = oldNotice
		server.Close()
	}
}
