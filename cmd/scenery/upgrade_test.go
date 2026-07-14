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

	opts, err := parseUpgradeArgs([]string{"--target", "/tmp/scenery", "--toolchain", "all", "--force", "--dry-run", "-o", "json"})
	if err != nil {
		t.Fatalf("parseUpgradeArgs() error = %v", err)
	}
	if opts.Target != "/tmp/scenery" || opts.ToolchainMode != "all" || !opts.Force || !opts.DryRun || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	opts, err = parseUpgradeArgs([]string{"--skip-toolchain"})
	if err != nil {
		t.Fatalf("parseUpgradeArgs(skip) error = %v", err)
	}
	if opts.ToolchainMode != "none" {
		t.Fatalf("skip opts = %+v", opts)
	}
	if _, err := parseUpgradeArgs([]string{"--toolchain", "maybe"}); err == nil {
		t.Fatal("expected invalid toolchain mode to fail")
	}
}

func TestRunUpgradeHelpUsesCurrentChannelContract(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"scenery upgrade", "--target <path>", "--toolchain installed|all|none"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "--version") {
		t.Fatalf("help exposes historical release selection:\n%s", help)
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
		"  echo '{\"kind\":\"scenery.toolchain.status\",\"schema_revision\":\"sha256:016d9a4dcfe775dd3847bd0ff320dd889d7945e9df22b8774a1d42b210c3f0f0\",\"artifacts\":[]}'\n" +
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
	if err := runUpgrade(t.Context(), &out, []string{"--target", target, "--toolchain", "all", "-o", "json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
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
	if strings.TrimSpace(string(args)) != "system toolchain sync -o json --images" {
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
	if err := runUpgrade(t.Context(), &out, []string{"--skip-toolchain", "-o", "json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
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
	if err := runUpgrade(t.Context(), &out, []string{"--target", target, "--skip-toolchain", "-o", "json"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
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

func TestRunUpgradeRefusesLegacyRecoveryStateBeforeReplacingBinary(t *testing.T) {
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	archiveData := buildUpgradeArchive(t, []byte("new binary"))
	sum := sha256.Sum256(archiveData)
	server := newUpgradeReleaseServer(t, tag, map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	})
	restore := overrideUpgradeGlobals(t, server, "v0.2.0")
	defer restore()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"legacy-recovery"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, ".scenery", "transactions", "change-apply.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"api_version":"scenery.change-transaction/v1","sentinel":"keep"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	upgradeWorkingDirectory = func() (string, error) { return root, nil }

	target := filepath.Join(t.TempDir(), "scenery")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runUpgrade(t.Context(), &out, []string{"--target", target, "--skip-toolchain", "-o", "json"})
	if err == nil || !strings.Contains(err.Error(), "previous Scenery binary") || !strings.Contains(err.Error(), "no state was modified") {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "old binary" {
		t.Fatalf("upgrade replaced target: got %q, err %v", got, readErr)
	}
	if got, readErr := os.ReadFile(legacyPath); readErr != nil || string(got) != string(legacy) {
		t.Fatalf("upgrade changed legacy state: got %q, err %v", got, readErr)
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
	err := runUpgrade(t.Context(), &out, []string{"--target", target, "--skip-toolchain", "-o", "json"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if jsonErr := decodeCLIJSON(out.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", jsonErr, out.String())
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
			HelperInstalled:  true,
			ActionRequired:   true,
			HelperVersion:    "v0.2.0",
			CurrentVersion:   targetVersion,
			HelperContract:   "",
			ExpectedContract: localagent.EdgeHelperContractRevision,
			Message:          "installed privileged helper predates handoff contract " + localagent.EdgeHelperContractRevision + " and can stop forwarding when target metadata changes",
			SuggestedAction:  "Run `scenery deploy setup` to update the privileged listener (asks for sudo).",
		}
	}

	target := filepath.Join(t.TempDir(), "scenery")
	var out bytes.Buffer
	if err := runUpgrade(t.Context(), &out, []string{"--target", target, "--skip-toolchain"}); err != nil {
		t.Fatalf("runUpgrade() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "deploy helper: Run `scenery deploy setup` to update the privileged listener (asks for sudo).") {
		t.Fatalf("upgrade output missing deploy notice:\n%s", out.String())
	}

	out.Reset()
	if err := runUpgrade(t.Context(), &out, []string{"--target", target, "--force", "--skip-toolchain", "-o", "json"}); err != nil {
		t.Fatalf("runUpgrade json error = %v\n%s", err, out.String())
	}
	var payload upgradeResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("decodeCLIJSON: %v\n%s", err, out.String())
	}
	if payload.Deploy == nil || !payload.Deploy.ActionRequired || payload.Deploy.ExpectedContract != localagent.EdgeHelperContractRevision {
		t.Fatalf("deploy notice = %+v", payload.Deploy)
	}
}

func TestDeployHelperDriftRequiresSetupOnlyForContractDrift(t *testing.T) {
	t.Parallel()

	// An installed helper without a stamped handoff contract predates the
	// tolerant reader: any target-metadata revision change makes it drop
	// every connection, so it must be flagged even though its version string
	// and its target metadata look plausible.
	helper := edgeStatusPrivilegedListener{
		Installed: true,
		Version:   "v1.0.0",
		Listen:    []string{"0.0.0.0:443", "[::]:443", "0.0.0.0:80", "[::]:80"},
	}
	drift := deployHelperDriftFor(helper, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.SuggestedAction, "deploy setup") {
		t.Fatalf("unstamped helper drift = %+v", drift)
	}
	if drift.ExpectedContract != localagent.EdgeHelperContractRevision {
		t.Fatalf("expected contract = %q", drift.ExpectedContract)
	}

	// A stamped but different contract also requires re-setup.
	helper.ContractRevision = "1"
	drift = deployHelperDriftFor(helper, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.Message, "handoff contract 1") {
		t.Fatalf("contract drift = %+v", drift)
	}

	// A loopback-only install points at the loopback installer instead.
	loopback := helper
	loopback.ContractRevision = ""
	loopback.Listen = []string{"127.0.0.1:443", "[::1]:443"}
	drift = deployHelperDriftFor(loopback, "v2.0.0")
	if !drift.ActionRequired || !strings.Contains(drift.SuggestedAction, "system edge privileged install") {
		t.Fatalf("loopback drift = %+v", drift)
	}

	// Version drift with a matching contract stays informational: the frozen
	// handoff contract is exactly what makes scenery upgrades safe without a
	// sudo re-setup.
	helper.ContractRevision = localagent.EdgeHelperContractRevision
	drift = deployHelperDriftFor(helper, "v2.0.0")
	if drift.ActionRequired || !strings.Contains(drift.Message, "helper is v1.0.0") {
		t.Fatalf("version-only drift = %+v", drift)
	}

	// Matching version and contract is clean.
	helper.Version = "v2.0.0"
	drift = deployHelperDriftFor(helper, "v2.0.0")
	if drift.ActionRequired || !strings.Contains(drift.Message, "matches current binary") {
		t.Fatalf("clean drift = %+v", drift)
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
	oldWorkingDirectory := upgradeWorkingDirectory
	upgradeAPIBaseURL = server.URL + "/repos/scenery-sh/scenery"
	upgradeHTTPClient = server.Client()
	upgradeCurrentVersionFn = func() string { return currentVersion }
	upgradeDeployNoticeFunc = func(targetVersion string) *deployHelperDrift { return nil }
	return func() {
		upgradeAPIBaseURL = oldBase
		upgradeHTTPClient = oldClient
		upgradeCurrentVersionFn = oldVersion
		upgradeDeployNoticeFunc = oldNotice
		upgradeWorkingDirectory = oldWorkingDirectory
		server.Close()
	}
}
