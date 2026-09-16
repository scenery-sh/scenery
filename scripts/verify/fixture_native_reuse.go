package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"scenery.sh/internal/build"
)

// Restart the unchanged application through the public lifecycle twice. Each
// start re-enters the real dev build pipeline with current checks, persists the
// same graph and runtime bundle, and links nothing: every process executable of
// the linked build is reused by its recorded digest.
func verifyHarnessNativeRuntimeReuse(ctx context.Context, repoRoot, appRoot string, env []string, linked build.RuntimeBundleDescriptor) (returnErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json")
		returnErr = errors.Join(returnErr, err)
	}()
	up := func() (*build.LatestBuildManifest, error) {
		if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "down", "-o", "json"); err != nil {
			return nil, err
		}
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
		if err := harnessProcessReuseEvidence(events); err != nil {
			return nil, err
		}
		manifest, ok, err := build.ReadLatestBuildManifest(appRoot)
		if err != nil {
			return nil, err
		}
		if !ok || manifest.Build.Phase != "compiled" || !manifest.Build.BuildStateExists || manifest.Build.GraphFingerprint == "" || !manifest.Build.MetadataPresent || !manifest.Build.APIEncodingPresent {
			return nil, fmt.Errorf("public runtime did not persist reusable build state: %+v", manifest)
		}
		bundle, err := build.ReadRuntimeBundle(appRoot, "development")
		if err != nil {
			return nil, err
		}
		if bundle.ImplementationRevision != linked.ImplementationRevision || bundle.BuildInput.Digest != linked.BuildInput.Digest {
			return nil, fmt.Errorf("unchanged public restart changed the runtime bundle identity")
		}
		return manifest, nil
	}
	first, err := up()
	if err != nil {
		return err
	}
	second, err := up()
	if err != nil {
		return err
	}
	if second.Build.GraphFingerprint != first.Build.GraphFingerprint {
		return fmt.Errorf("unchanged public restart changed the native graph")
	}
	return nil
}

// harnessProcessReuseEvidence requires the successful build of a start to have
// reused every process executable and linked none.
func harnessProcessReuseEvidence(events []harnessWatchEvent) error {
	operation := ""
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "build.request" && event.Data.OK {
			operation = event.Data.OperationID
		}
	}
	reused, linked := -1, 0
	for _, event := range events {
		data := event.Data
		if event.Type != "build.step" || data.OperationID != operation {
			continue
		}
		switch {
		case data.Name == "process.reuse" && data.OK && data.CacheMisses == 0:
			reused = data.Actions
		case data.Name == "build.artifact" && strings.HasPrefix(data.Reason, "linked_development_process_"):
			linked++
		}
	}
	if operation == "" || reused <= 0 || linked != 0 {
		return fmt.Errorf("unchanged restart did not reuse every process executable: operation=%q reused=%d linked=%d", operation, reused, linked)
	}
	return nil
}

// Both real-app probes have an external Scenery source replacement. Require a
// current-operation stock Go link of the changed process entrypoints under the
// host-wide link budget, and no publication to the shared executable cache,
// which never admits inputs outside its reuse domain. An old generation's
// events cannot satisfy this assertion.
func harnessPrivateExternalBuildEvidence(events []harnessWatchEvent) (map[string]any, error) {
	operation := ""
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "build.request" && event.Data.OK {
			operation = event.Data.OperationID
		}
	}
	builds := 0
	var linked []string
	queued, shared := false, false
	for _, event := range events {
		data := event.Data
		if event.Type != "build.step" || data.OperationID != operation {
			continue
		}
		switch data.Name {
		case "go.command":
			if data.Reason == "build" && data.OK {
				builds++
			}
		case "build.artifact":
			if process, ok := strings.CutPrefix(data.Reason, "linked_development_process_"); ok && data.OK && data.ExecutableBytes > 0 {
				linked = append(linked, process)
			}
		case "build.shared_link_queue":
			queued = data.OK
		case "build.shared_artifact", "build.shared_queue":
			shared = true
		}
	}
	if operation == "" || builds != 1 || len(linked) == 0 || !queued || shared {
		return nil, fmt.Errorf("external-input build did not prove a private process link: operation=%q builds=%d linked=%v link_queued=%t shared_cache=%t", operation, builds, linked, queued, shared)
	}
	return map[string]any{"operation_id": operation, "build_backend": "stock_process_link", "linked_processes": linked, "shared_artifact_published": false, "link_budget_queued": true}, nil
}
