package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/devdash"
)

const dashboardUIRootRel = "apps/console"

func runHarnessDashboardFreshnessStep(parent context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: "dashboard ui fresh", Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "up", "--detach", "-o", "json"}}
	step.Summary, step.Error = harnessDashboardFreshness(parent, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), step.Error == ""
	if !step.OK {
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Build the dashboard embed and rebuild the prepared product binary, then rerun the repository verifier."}}
	}
	return step
}

// Ask the prepared product's real dashboard which assets it serves. Embedding
// dashboard assets into the verifier would only prove the verifier's freshness.
func harnessDashboardFreshness(parent context.Context, repoRoot string) (summary map[string]any, failure string) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-dashboard-bundle-*")
	if err != nil {
		return nil, err.Error()
	}
	defer func() {
		if failure == "" {
			_ = os.RemoveAll(root)
		}
	}()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "state")
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return nil, err.Error()
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if _, err := runHarnessAppCLI(cleanup, repoRoot, appRoot, home, "down", "-o", "json"); err != nil {
			failure = errors.Join(errors.New(failure), err).Error()
		}
	}()
	output, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return nil, err.Error()
	}
	var launched detachedDevResult
	if err := decodeCLIJSON(output, &launched); err != nil {
		return nil, err.Error()
	}
	address := launched.Session.RouteManifest.BaseURL + "/console/"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err.Error()
	}
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, err.Error()
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return nil, err.Error()
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("prepared dashboard returned HTTP %d", response.StatusCode)
	}
	running := response.Header.Get("X-Scenery-Dashboard-Bundle-Hash")
	disk, exists, err := devdash.DashboardBundleHashDir(filepath.Join(repoRoot, dashboardUIRootRel, "dist"))
	if err != nil {
		return nil, err.Error()
	}
	stale := !exists || running == "" || running != disk || response.Header.Get("X-Scenery-Dashboard-Bundle-Stale") == "true"
	summary = map[string]any{"product_binary": harnessLocalSceneryBinaryPath(repoRoot), "running_hash": running, "disk_hash": disk, "stale": stale, "dashboard_http": response.StatusCode}
	if stale {
		return summary, "the prepared product dashboard bundle is stale"
	}
	return summary, ""
}
