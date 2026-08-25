package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
	"time"

	"scenery.sh/internal/deploydiag"
)

const harnessUpgradeProcessProbeName = "upgrade process probe"

type harnessUpgradeProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessUpgradeProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessUpgradeProcessProbeStepWithCheck(ctx, repoRoot, runHarnessUpgradeProcessProbeCheck)
}

func runHarnessUpgradeProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessUpgradeProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessUpgradeProcessProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the upgrade install/process boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessUpgradeProcessProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{
			"proof":  "not_applicable_on_windows",
			"reason": "the release probe executes a POSIX fake installed binary",
		}, nil, nil
	}
	root, err := os.MkdirTemp("", "scenery-upgrade-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)

	marker := filepath.Join(root, "toolchain.args")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"system\" ] && [ \"$2\" = \"toolchain\" ] && [ \"$3\" = \"sync\" ]; then\n" +
		"  printf '%s\\n' \"$*\" > \"" + marker + "\"\n" +
		"  echo '{\"kind\":\"scenery.toolchain.status\",\"schema_revision\":\"sha256:016d9a4dcfe775dd3847bd0ff320dd889d7945e9df22b8774a1d42b210c3f0f0\",\"artifacts\":[]}'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo 'fake scenery'\n"
	archiveData, err := buildHarnessUpgradeArchive([]byte(script))
	if err != nil {
		return nil, nil, err
	}
	tag := "v9.9.9"
	assetName := upgradeArchiveName(tag)
	sum := sha256.Sum256(archiveData)
	assets := map[string][]byte{
		assetName:       archiveData,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/scenery-sh/scenery/releases/latest", func(w http.ResponseWriter, req *http.Request) {
		release := upgradeRelease{TagName: tag}
		for name := range assets {
			release.Assets = append(release.Assets, upgradeReleaseAsset{
				Name:               name,
				BrowserDownloadURL: "http://" + req.Host + "/download/" + name,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(release); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/download/")
		data, ok := assets[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	target := filepath.Join(root, "bin", "scenery")
	deps := upgradeDependencies{
		apiBaseURL:          server.URL + "/repos/scenery-sh/scenery",
		httpClient:          server.Client(),
		currentVersion:      func() string { return "v0.2.0" },
		deployNotice:        func(string) *deploydiag.HelperDrift { return nil },
		workingDirectory:    func() (string, error) { return root, nil },
		runToolchainCommand: runUpgradeToolchainCommand,
	}
	var stdout bytes.Buffer
	if err := runUpgradeWithDependencies(ctx, &stdout, []string{"--target", target, "--toolchain", "all", "-o", "json"}, deps); err != nil {
		return nil, nil, fmt.Errorf("upgrade process probe: %w\n%s", err, stdout.String())
	}
	var payload upgradeResponse
	if err := decodeCLIJSON(stdout.Bytes(), &payload); err != nil {
		return nil, nil, err
	}
	if !payload.OK || !payload.Installed || payload.TargetVersion != tag || payload.Toolchain == nil || len(payload.Toolchain.Synced) != 1 {
		return nil, nil, fmt.Errorf("upgrade payload = %+v", payload)
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&0o111 == 0 {
		return nil, nil, fmt.Errorf("installed target is not executable: %v", info.Mode())
	}
	args, err := os.ReadFile(marker)
	if err != nil {
		return nil, nil, err
	}
	if got := strings.TrimSpace(string(args)); got != "system toolchain sync -o json --images" {
		return nil, nil, fmt.Errorf("toolchain arguments = %q", got)
	}
	return map[string]any{
		"proof":          "verified_release_installed_and_executed_for_toolchain_sync",
		"target_version": payload.TargetVersion,
		"asset":          payload.AssetName,
		"toolchain_args": strings.TrimSpace(string(args)),
	}, nil, nil
}

func buildHarnessUpgradeArchive(binary []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "scenery", Mode: 0o755, Size: int64(len(binary)), ModTime: time.Unix(0, 0)}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(binary); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
