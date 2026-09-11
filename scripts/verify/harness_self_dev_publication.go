package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

type devPublicationCheckpoint struct {
	PID                                     int
	Binary, Workspace, Graph, Input, Target string
}

// The overlay adds a rendezvous at a genuine production boundary. It changes no
// build, admission, cache, retention or handoff operation, and adds no product API.
func buildDevPublicationCheckpoint(ctx context.Context, repo, dir string) (binary, control, reached, rejected string, err error) {
	control, reached, rejected = filepath.Join(dir, "pause"), filepath.Join(dir, "compiled.json"), filepath.Join(dir, "rejected")
	source := filepath.Join(repo, "cmd/scenery/dev_build_preparation.go")
	original, err := os.ReadFile(source)
	if err != nil {
		return "", "", "", "", err
	}
	anchor := []byte("current, err := scanSourceAdmissionFiles(p.root)")
	if bytes.Count(original, anchor) != 1 {
		return "", "", "", "", fmt.Errorf("source admission checkpoint anchor changed")
	}
	checkpoint := fmt.Sprintf(`if _, checkpointErr := os.Stat(%q); checkpointErr == nil {
  encoded, checkpointErr := json.Marshal(map[string]any{"PID":os.Getpid(),"Binary":p.result.Binary,"Workspace":p.result.Dir,"Graph":p.result.GraphFingerprint,"Input":p.result.BuildInput.Digest,"Target":p.result.Target.Name})
  if checkpointErr != nil { return checkpointErr }
  if checkpointErr = os.WriteFile(%q,encoded,0600); checkpointErr != nil {return checkpointErr}
  for { if _, checkpointErr=os.Stat(%q); os.IsNotExist(checkpointErr){break}; select {case <-ctx.Done():return ctx.Err();case <-time.After(10*time.Millisecond):} }
 }
 current, err := scanSourceAdmissionFiles(p.root)`, control, reached, control)
	changed := bytes.Replace(original, anchor, []byte(checkpoint), 1)
	changed = bytes.Replace(changed, []byte(`"fmt"`), []byte("\"fmt\"\n\"os\"\n\"time\""), 1)
	rejection := []byte(`return fmt.Errorf("application source changed during compilation; edit remains pending")`)
	if bytes.Count(changed, rejection) != 1 {
		return "", "", "", "", fmt.Errorf("source rejection checkpoint anchor changed")
	}
	changed = bytes.Replace(changed, rejection, []byte(fmt.Sprintf(`if checkpointErr:=os.WriteFile(%q,[]byte("rejected"),0600);checkpointErr!=nil{return checkpointErr}
 `, rejected)+string(rejection)), 1)
	replacement := filepath.Join(dir, "dev_build_preparation.go")
	if err = os.WriteFile(replacement, changed, 0600); err != nil {
		return
	}
	overlay := filepath.Join(dir, "overlay.json")
	encoded, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: replacement}})
	if err = os.WriteFile(overlay, encoded, 0600); err != nil {
		return
	}
	sourceIdentity, err := build.FrameworkSourceManifest(repo)
	if err != nil {
		return "", "", "", "", err
	}
	flags, err := build.FrameworkProducerLinkerFlags(sourceIdentity.Digest)
	if err != nil {
		return "", "", "", "", err
	}
	binary = filepath.Join(dir, "scenery-publication-checkpoint")
	command := commandTreeContext(ctx, "go", "build", "-overlay", overlay, "-ldflags="+flags, "-o", binary, "./cmd/scenery")
	command.Dir = repo
	if output, e := command.CombinedOutput(); e != nil {
		return "", "", "", "", fmt.Errorf("build publication checkpoint: %w: %s", e, output)
	}
	return
}

func runHarnessDevPublicationProbe(parent context.Context, repo string) (summary map[string]any, returnedErr error) {
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "scn-publication-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	binary, control, reached, rejected, err := buildDevPublicationCheckpoint(ctx, repo, root)
	if err != nil {
		return nil, err
	}
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "agent")
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
		contents, err := os.ReadFile(filepath.Join(repo, "testdata/apps/basic", name))
		if err != nil {
			return nil, err
		}
		if name == "go.mod" {
			contents = bytes.ReplaceAll(contents, []byte("=> ../../.."), []byte("=> "+repo))
		}
		path := filepath.Join(appRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, contents, 0600); err != nil {
			return nil, err
		}
	}
	if err := prepareHarnessHandoffService(appRoot); err != nil {
		return nil, err
	}
	env := envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_DASHBOARD_ADDR", "SCENERY_DEV_CACHE_DIR", "DATABASE_URL", detachedDevChildEnv), "SCENERY_AGENT_HOME="+home, "SCENERY_DEV_CACHE_DIR="+filepath.Join(root, "cache"), "SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
	run := func(runCtx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(runCtx, binary, args...)
		cmd.Env, cmd.Dir = env, appRoot
		return cmd.CombinedOutput()
	}
	var owner localagent.Session
	defer func() {
		_ = os.Remove(control)
		stopCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		output, err := run(stopCtx, "down", "--app-root", appRoot, "-o", "json")
		if err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("publication probe cleanup: %w: %s", err, output)
		}
		for owner.OwnerPID > 0 {
			if _, live := localagent.SessionOwnerProcessLive(owner); !live {
				break
			}
			if err := harnessWaitContext(stopCtx, 20*time.Millisecond); err != nil {
				if returnedErr == nil {
					returnedErr = fmt.Errorf("publication supervisor remained live: %w", err)
				}
				break
			}
		}
		if summary != nil {
			summary["cleanup_confirmed"] = returnedErr == nil
		}
	}()
	output, err := run(ctx, "up", "--detach", "--wait", "ready", "--app-root", appRoot, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("publication probe startup: %w: %s", err, output)
	}
	var started detachedDevResult
	if err := decodeCLIJSON(output, &started); err != nil {
		return nil, err
	}
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return nil, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	readSession := func() (localagent.Session, error) {
		sessions, err := client.List(ctx, paths.AppRoot)
		if err != nil {
			return localagent.Session{}, err
		}
		if len(sessions) != 1 || sessions[0].OwnerPID != started.PID {
			return localagent.Session{}, fmt.Errorf("publication probe lost supervisor ownership")
		}
		return sessions[0], nil
	}
	owner, err = readSession()
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(owner.RouteManifest.Routes[localagent.RouteAPI].URL, "/") + "/echo"
	waitResponse := func(previous, want string) (localagent.Session, error) {
		for {
			session, err := readSession()
			if err == nil && session.Status == "running" && session.AppPID != "" && session.AppPID != previous {
				if err = harnessHandoffEcho(ctx, url, want, session.AppPID); err == nil {
					return session, nil
				}
			}
			if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
				return localagent.Session{}, err
			}
		}
	}
	initial, found, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !found {
		return nil, fmt.Errorf("initial build manifest: %v", err)
	}
	sourcePath := filepath.Join(appRoot, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	edited := func(prefix string) []byte {
		return bytes.Replace(source, []byte(`Message: "echo:"`), []byte(`Message: "`+prefix+`"`), 1)
	}
	if err := os.WriteFile(sourcePath, edited("previous:"), 0600); err != nil {
		return nil, err
	}
	previous, err := waitResponse(owner.AppPID, "previous:handoff")
	if err != nil {
		return nil, err
	}
	prior, found, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !found {
		return nil, fmt.Errorf("previous build manifest: %v", err)
	}
	retained, err := devPublicationRetainedBytes(previous.StateRoot)
	if err != nil || len(retained) == 0 {
		return nil, fmt.Errorf("previous retained artifacts: %v", err)
	}
	if err := os.WriteFile(control, []byte("pause"), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sourcePath, edited("rejected:"), 0600); err != nil {
		return nil, err
	}
	if err := devPublicationWaitFile(ctx, reached); err != nil {
		return nil, err
	}
	encoded, err := os.ReadFile(reached)
	if err != nil {
		return nil, err
	}
	var checkpoint devPublicationCheckpoint
	if err := json.Unmarshal(encoded, &checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.PID != started.PID || checkpoint.Graph == prior.Build.GraphFingerprint {
		return nil, fmt.Errorf("checkpoint did not follow a new real compilation")
	}
	published, found, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !found {
		return nil, fmt.Errorf("published build manifest: %v", err)
	}
	if published.Build.Phase != "compiled" || published.Build.BinaryPath != checkpoint.Binary || published.Build.GraphFingerprint != checkpoint.Graph {
		return nil, fmt.Errorf("compiled candidate publication identity disagrees with checkpoint")
	}
	stateBytes, err := os.ReadFile(published.Build.BuildStatePath)
	if err != nil {
		return nil, err
	}
	var state struct {
		GraphFingerprint string `json:"graph_fingerprint"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		return nil, err
	}
	if state.GraphFingerprint != checkpoint.Graph {
		return nil, fmt.Errorf("successful build state did not publish the candidate graph")
	}
	bundleBytes, err := os.ReadFile(build.RuntimeBundlePath(appRoot, checkpoint.Target))
	if err != nil {
		return nil, err
	}
	bundle, err := build.ReadRuntimeBundle(appRoot, checkpoint.Target)
	if err != nil || bundle.BuildInput == nil || bundle.BuildInput.Digest != checkpoint.Input {
		return nil, fmt.Errorf("published runtime bundle identity: %v", err)
	}
	if _, err := os.Stat(initial.Build.BinaryPath); !os.IsNotExist(err) {
		return nil, fmt.Errorf("older cache executable was not pruned: %v", err)
	}
	for _, path := range []string{prior.Build.BinaryPath, checkpoint.Binary} {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("current/previous cache executable lost: %w", err)
		}
	}
	if err := harnessHandoffEcho(ctx, url, "previous:handoff", previous.AppPID); err != nil {
		return nil, err
	}
	// The later edit is invalid so its ensuing retry cannot replace published
	// successful state before we inspect the rejected revision's ownership.
	if err := os.WriteFile(sourcePath, []byte("package service\nfunc invalid source( {\n"), 0600); err != nil {
		return nil, err
	}
	if err := os.Remove(control); err != nil {
		return nil, err
	}
	if err := devPublicationWaitFile(ctx, rejected); err != nil {
		return nil, err
	}
	for {
		log, err := os.ReadFile(started.LogPath)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(log, []byte("SCN6202")) {
			break
		}
		if err := harnessWaitContext(ctx, 25*time.Millisecond); err != nil {
			return nil, err
		}
	}
	for path, want := range map[string][]byte{published.Build.BuildStatePath: stateBytes, build.RuntimeBundlePath(appRoot, checkpoint.Target): bundleBytes} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			return nil, fmt.Errorf("rejection/invalid retry changed successful artifact %s: %v", path, err)
		}
	}
	for path, want := range retained {
		got, err := worktreeProbeFileSHA(path)
		if err != nil || got != want {
			return nil, fmt.Errorf("rejection changed retained executable %s: %v", path, err)
		}
	}
	if err := harnessHandoffEcho(ctx, url, "previous:handoff", previous.AppPID); err != nil {
		return nil, err
	}
	failed, found, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !found {
		return nil, fmt.Errorf("failed retry manifest: %v", err)
	}
	if failed.Build.Phase != "prepared" && failed.Build.Phase != "compiled" {
		return nil, fmt.Errorf("unexpected failed retry phase %s", failed.Build.Phase)
	}
	if err := os.WriteFile(sourcePath, edited("repaired:"), 0600); err != nil {
		return nil, err
	}
	repaired, err := waitResponse(previous.AppPID, "repaired:handoff")
	if err != nil {
		return nil, err
	}
	final, found, err := build.ReadLatestBuildManifest(appRoot)
	if err != nil || !found {
		return nil, fmt.Errorf("repaired build manifest: %v", err)
	}
	if final.Build.Phase != "compiled" || final.Build.GraphFingerprint == checkpoint.Graph || final.Build.BinaryPath == checkpoint.Binary {
		return nil, fmt.Errorf("repair reused rejected source identity")
	}
	finalBundle, err := build.ReadRuntimeBundle(appRoot, checkpoint.Target)
	if err != nil || finalBundle.BuildInput == nil || finalBundle.BuildInput.Digest == checkpoint.Input {
		return nil, fmt.Errorf("repair reused rejected build inputs: %v", err)
	}
	proofSHA, err := worktreeProbeFileSHA(binary)
	if err != nil {
		return nil, err
	}
	return map[string]any{"checkpoint_binary_sha256": proofSHA, "real_compile_published_before_admission": true, "older_cache_pruned": true, "previous_and_candidate_cache_preserved": true, "previous_retained_bytes_unchanged": true, "previous_http_served_during_pause_and_rejection": true, "invalid_retry_preserved_success_state_and_bundle": true, "failed_latest_manifest_phase": failed.Build.Phase, "repaired_http_and_new_identity": true, "supervisor_pid": started.PID, "previous_pid": previous.AppPID, "repaired_pid": repaired.AppPID, "rejected_graph": checkpoint.Graph, "repaired_graph": final.Build.GraphFingerprint}, nil
}

func devPublicationWaitFile(ctx context.Context, path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := harnessWaitContext(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}

func devPublicationRetainedBytes(stateRoot string) (map[string]string, error) {
	paths, err := filepath.Glob(filepath.Join(stateRoot, "run", "app", "scenery-app-*"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, path := range paths {
		digest, err := worktreeProbeFileSHA(path)
		if err != nil {
			return nil, err
		}
		out[path] = digest
	}
	return out, nil
}
