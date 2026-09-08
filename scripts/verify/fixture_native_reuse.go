package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"scenery.sh/internal/build"
)

// Re-enter the real dev build pipeline with unchanged source, then require its
// persisted graph and compiled binary to survive a full public down/up cycle.
func verifyHarnessNativeRuntimeReuse(ctx context.Context, repoRoot, appRoot string, env []string) (returnErr error) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json")
		returnErr = errors.Join(returnErr, err)
	}()
	up := func() (*build.LatestBuildManifest, error) {
		if _, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
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
	before, err := os.Stat(first.Build.BinaryPath)
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
	after, err := os.Stat(second.Build.BinaryPath)
	if err != nil {
		return err
	}
	if second.Build.GraphFingerprint != first.Build.GraphFingerprint || second.Build.BinaryPath != first.Build.BinaryPath || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
		return fmt.Errorf("unchanged public restart did not reuse the compiled native graph/binary")
	}
	return nil
}
