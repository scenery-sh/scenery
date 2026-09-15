package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scenery.sh/internal/build"
)

// Re-enter the real dev build pipeline with unchanged source, then require its
// persisted graph to survive a full public down/up cycle. The external framework
// excludes whole-executable reuse; each start must compile with current checks.
func verifyHarnessNativeRuntimeReuse(ctx context.Context, repoRoot, appRoot string, env []string) (returnErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json")
		returnErr = errors.Join(returnErr, err)
	}()
	up := func() (*build.LatestBuildManifest, error) {
		output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "-o", "json")
		if err != nil {
			return nil, err
		}
		var started detachedDevResult
		if err := decodeCLIJSON(output, &started); err != nil {
			return nil, err
		}
		events, err := harnessWatchEvents(started.LogPath, 0)
		if err != nil {
			return nil, err
		}
		if _, err := harnessPrivateExternalBuildEvidence(events); err != nil {
			return nil, err
		}
		manifest, ok, err := build.ReadLatestBuildManifest(appRoot)
		if err != nil {
			return nil, err
		}
		if !ok || manifest.Build.Phase != "compiled" || !manifest.Build.BinaryExists || !manifest.Build.BuildStateExists || manifest.Build.GraphFingerprint == "" || !manifest.Build.MetadataPresent || !manifest.Build.APIEncodingPresent {
			return nil, fmt.Errorf("public runtime did not persist reusable build state: %+v", manifest)
		}
		return manifest, nil
	}
	first, err := up()
	if err != nil {
		return err
	}
	if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "down", "-o", "json"); err != nil {
		return err
	}
	second, err := up()
	if err != nil {
		return err
	}
	if second.Build.GraphFingerprint != first.Build.GraphFingerprint || second.Build.BinaryPath != first.Build.BinaryPath {
		return fmt.Errorf("unchanged public restart changed the native graph/build identity")
	}
	return nil
}

// Both real-app probes have an external Scenery source replacement. Require a
// current-operation stock private build or retained default build, plus the
// common link budget. An old generation's events cannot satisfy this assertion.
func harnessPrivateExternalBuildEvidence(events []harnessWatchEvent) (map[string]any, error) {
	operation := ""
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "build.request" && event.Data.OK {
			operation = event.Data.OperationID
		}
	}
	builds, artifacts, retainedArtifacts := 0, 0, 0
	bypassed, checked, queued, sharedAction := false, false, false, false
	retainedBootstrap, retainedCompile, retainedBackend := false, false, ""
	for _, event := range events {
		data := event.Data
		if event.Type != "build.step" || data.OperationID != operation {
			continue
		}
		switch data.Name {
		case "go.command":
			if data.Reason == "build" && data.OK {
				if data.Cache == "retained_recipe" {
					retainedCompile = true
				} else {
					builds++
				}
			}
			if data.Reason == "retained_bootstrap" && data.Cache == "miss" && data.OK {
				retainedBootstrap = true
			}
		case "build.backend":
			if data.OK && data.Cache == "hit" && data.Reason == "retained_compiler" {
				retainedBackend = "retained_compiler"
			}
			if data.OK && data.Cache == "miss" && (data.Reason == "missing_recipe" || data.Reason == "invalid_recipe" || data.Reason == "incompatible_recipe") {
				retainedBackend = "retained_bootstrap"
			}
		case "build.artifact":
			if data.OK && (data.Cache == "retained_recipe" || data.Cache == "retained_bootstrap") && data.ExecutableBytes > 0 {
				retainedArtifacts++
			}
		case "build.shared_input_check":
			checked = data.OK
		case "build.shared_link_queue":
			queued = data.OK
		case "build.shared_queue":
			sharedAction = true
		case "build.shared_artifact":
			artifacts++
			bypassed = data.OK && data.Cache == "bypass" && data.Reason == "inputs_outside_shared_reuse_domain" && data.ExecutableBytes > 0
		}
	}
	stock := builds == 1 && artifacts == 1 && bypassed && checked
	retained := retainedArtifacts == 1 && ((retainedBootstrap && retainedBackend == "retained_bootstrap") || (retainedCompile && retainedBackend == "retained_compiler"))
	if operation == "" || !queued || sharedAction || (!stock && !retained) {
		return nil, fmt.Errorf("external-input build did not prove private or retained compilation: operation=%q builds=%d shared_artifacts=%d retained_artifacts=%d bypass=%t checked=%t retained_bootstrap=%t retained_compile=%t retained_backend=%q link_queued=%t shared_action=%t", operation, builds, artifacts, retainedArtifacts, bypassed, checked, retainedBootstrap, retainedCompile, retainedBackend, queued, sharedAction)
	}
	backend := "stock_private"
	if retained {
		backend = retainedBackend
	}
	return map[string]any{"operation_id": operation, "build_backend": backend, "shared_artifact_published": false, "live_input_check": true, "link_budget_queued": true}, nil
}
