package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
)

const harnessProcessModelProbeName = "process model replacement probe"

// The process-model probe runs testdata/apps/multiservice through the public
// `scenery up` lifecycle with SCENERY_DEV_PROCESS_MODEL=service. Every response
// is attributed to its answering process through the runtime identity header.
func runHarnessProcessModelProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessProcessModelProbeName, Command: []string{"go", "run", "./scripts/verify", "--probe", "process-model", "--summary"}}
	summary, err := runHarnessProcessModelProbe(ctx, repoRoot)
	step.Summary, step.DurationMS = summary, time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = []checkDiagnostic{{
			Stage: step.Name, Severity: "error", Message: step.Error,
			SuggestedAction: "Fix process-model generation, dispatch or supervisor replacement, then rerun `go run ./scripts/verify --probe process-model --summary --write`.",
		}}
		return step
	}
	step.OK = true
	return step
}

type harnessProcessModelResponse struct {
	Message        string
	PID            int
	Implementation string
}

func runHarnessProcessModelProbe(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-process-model-")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(root)) }()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "agent")
	if err := copyHarnessMultiserviceFixture(repoRoot, appRoot); err != nil {
		return nil, err
	}
	env := envWithOverrides(harnessAppEnv(home), "SCENERY_DEV_PROCESS_MODEL=service")
	output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "-o", "json")
	if err != nil {
		return nil, err
	}
	var started detachedDevResult
	if err := decodeCLIJSON(output, &started); err != nil {
		return nil, err
	}
	var seen []int
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json"); err != nil {
			returnErr = errors.Join(returnErr, err)
			return
		}
		returnErr = errors.Join(returnErr, harnessProcessModelCleanup(home, appRoot, seen))
	}()
	host, err := strconv.Atoi(started.Session.AppPID)
	apiURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
	if err != nil || host <= 0 || apiURL == "" || started.LogPath == "" {
		return nil, fmt.Errorf("process-model session did not publish its host process: %s", output)
	}
	seen = append(seen, host)
	call := func(ctx context.Context, path, body string) (harnessProcessModelResponse, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+path, strings.NewReader(body))
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return harnessProcessModelResponse{}, err
		}
		var decoded struct {
			Message string `json:"message"`
		}
		if response.StatusCode != http.StatusOK || json.Unmarshal(data, &decoded) != nil {
			return harnessProcessModelResponse{}, fmt.Errorf("%s answered %d %s", path, response.StatusCode, data)
		}
		pid, _ := strconv.Atoi(response.Header.Get("X-Scenery-Process-ID"))
		if pid > 0 && !slices.Contains(seen, pid) {
			seen = append(seen, pid)
		}
		return harnessProcessModelResponse{Message: decoded.Message, PID: pid, Implementation: response.Header.Get("X-Scenery-Implementation-Revision")}, nil
	}
	waitFor := func(path, body, want string) (harnessProcessModelResponse, time.Duration, error) {
		begin := time.Now()
		for {
			response, err := call(ctx, path, body)
			if err == nil && response.Message == want {
				return response, time.Since(begin), nil
			}
			if ctx.Err() != nil || time.Since(begin) > 90*time.Second {
				return response, 0, fmt.Errorf("%s never answered %q (last %q, %v)", path, want, response.Message, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	logOffset := func() int64 {
		info, err := os.Stat(started.LogPath)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	echoOne, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoOne.Message != "echo:hi" {
		return nil, fmt.Errorf("initial echo = %#v, %v", echoOne, err)
	}
	greeterOne, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterOne.Message != "greeter:echo:hello probe" {
		return nil, fmt.Errorf("initial greet = %#v, %v", greeterOne, err)
	}
	if echoOne.PID <= 0 || greeterOne.PID <= 0 || len(map[int]bool{host: true, echoOne.PID: true, greeterOne.PID: true}) != 3 {
		return nil, fmt.Errorf("host %d, echo %d and greeter %d are not three processes", host, echoOne.PID, greeterOne.PID)
	}

	// A greet request pinned to the first generation stays in flight while
	// echo is replaced; it must still reach the first echo instance.
	pinned := make(chan harnessProcessModelResponse, 1)
	pinnedErr := make(chan error, 1)
	go func() {
		response, err := call(ctx, "/greet", `{"name":"wait:8s:pinned"}`)
		pinned <- response
		pinnedErr <- err
	}()
	if err := harnessWaitContext(ctx, 300*time.Millisecond); err != nil {
		return nil, err
	}
	echoSource := filepath.Join(appRoot, "echo/api.go")
	if err := harnessReplaceInFile(echoSource, `text.Label("echo", input.Message)`, `text.Label("echo-two", input.Message)`); err != nil {
		return nil, err
	}
	edited := time.Now()
	echoTwo, _, err := waitFor("/echo", `{"message":"hi"}`, "echo-two:hi")
	if err != nil {
		return nil, err
	}
	replacementLatency := time.Since(edited)
	var inFlight harnessProcessModelResponse
	select {
	case inFlight = <-pinned:
		if err := <-pinnedErr; err != nil {
			return nil, fmt.Errorf("pinned in-flight request: %w", err)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if inFlight.Message != "greeter:echo:hello pinned" || inFlight.PID != greeterOne.PID {
		return nil, fmt.Errorf("request pinned to the first generation answered %#v; want the first echo through greeter %d", inFlight, greeterOne.PID)
	}
	if !nativeBuildWaitProcessExit(echoOne.PID, 45*time.Second) {
		return nil, fmt.Errorf("replaced echo process %d did not retire after its generation drained", echoOne.PID)
	}
	greeterTwo, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterTwo.Message != "greeter:echo-two:hello probe" || greeterTwo.PID != greeterOne.PID || echoTwo.PID == echoOne.PID || echoTwo.Implementation == echoOne.Implementation {
		return nil, fmt.Errorf("after the echo edit greet = %#v (%v), echo = %#v; want the unchanged greeter %d and a new echo identity", greeterTwo, err, echoTwo, greeterOne.PID)
	}

	// A failing build leaves the published generation serving.
	failedOffset := logOffset()
	original, err := os.ReadFile(echoSource)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(echoSource, append(append([]byte(nil), original...), []byte("\nfunc brokenProbeEdit() { undefinedProbeSymbol() }\n")...), 0o600); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitBuild(ctx, started.LogPath, failedOffset, false); err != nil {
		return nil, err
	}
	served, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || served.Message != "greeter:echo-two:hello probe" || served.PID != greeterOne.PID {
		return nil, fmt.Errorf("after a failed build greet = %#v, %v", served, err)
	}
	restoredOffset := logOffset()
	if err := os.WriteFile(echoSource, original, 0o600); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitBuild(ctx, started.LogPath, restoredOffset, true); err != nil {
		return nil, err
	}
	if restored, err := call(ctx, "/echo", `{"message":"hi"}`); err != nil || restored.PID != echoTwo.PID {
		return nil, fmt.Errorf("restoring the published source replaced echo: %#v, %v", restored, err)
	}

	// A package compiled into both services replaces both in one generation.
	sharedOffset := logOffset()
	if err := harnessReplaceInFile(filepath.Join(appRoot, "internal/text/text.go"), `service + ":" + message`, `service + "|" + message`); err != nil {
		return nil, err
	}
	greeterThree, sharedLatency, err := waitFor("/greet", `{"name":"probe"}`, "greeter|echo-two|hello probe")
	if err != nil {
		return nil, err
	}
	echoThree, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoThree.Message != "echo-two|hi" || echoThree.PID == echoTwo.PID || greeterThree.PID == greeterOne.PID {
		return nil, fmt.Errorf("shared edit answered greet %#v and echo %#v (%v); want both services replaced", greeterThree, echoThree, err)
	}
	rebuilt, err := harnessProcessModelRebuiltSet(started.LogPath, sharedOffset)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(rebuilt, []string{"echo_echo", "greeter_greeter"}) {
		return nil, fmt.Errorf("shared edit rebuilt %v; want both service processes and not the host", rebuilt)
	}
	session, err := harnessLiveSession(ctx, home, appRoot)
	if err != nil {
		return nil, err
	}
	if session.AppPID != started.Session.AppPID {
		return nil, fmt.Errorf("host process changed from %s to %s", started.Session.AppPID, session.AppPID)
	}
	return map[string]any{
		"host_pid":                     host,
		"echo_pids":                    []int{echoOne.PID, echoTwo.PID, echoThree.PID},
		"greeter_pids":                 []int{greeterOne.PID, greeterThree.PID},
		"pinned_in_flight_answer":      inFlight.Message,
		"echo_edit_to_response_ms":     replacementLatency.Milliseconds(),
		"shared_edit_to_response_ms":   sharedLatency.Milliseconds(),
		"failed_build_kept_generation": true,
		"identical_source_kept_echo":   true,
		"shared_edit_rebuilt":          rebuilt,
		"proof":                        "public_scenery_up_process_model_replaced_only_changed_services_with_pinned_generation_and_identity_attribution",
	}, nil
}

func copyHarnessMultiserviceFixture(repoRoot, appRoot string) error {
	source := filepath.Join(repoRoot, "testdata/apps/multiservice")
	generated := map[string]bool{".scenery": true, "echo/scenerycontract": true, "greeter/scenerycontract": true, "internal/scenerygen": true}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if generated[filepath.ToSlash(relative)] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(appRoot, relative), 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if relative == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../../.."), []byte("=> "+repoRoot))
		}
		return os.WriteFile(filepath.Join(appRoot, relative), content, 0o600)
	})
}

func harnessReplaceInFile(path, old, replacement string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Count(data, []byte(old)) != 1 {
		return fmt.Errorf("%s does not contain exactly one %q", path, old)
	}
	return harnessAtomicWatchSave(path, bytes.Replace(data, []byte(old), []byte(replacement), 1))
}

func harnessProcessModelWaitBuild(ctx context.Context, log string, offset int64, ok bool) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		events, err := harnessWatchEvents(log, offset)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Type == "build.step" && event.Data.Name == "build.request" {
				if event.Data.OK != ok {
					return fmt.Errorf("build request ok=%t, want %t", event.Data.OK, ok)
				}
				return nil
			}
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("no build request completed after the edit")
}

func harnessProcessModelRebuiltSet(log string, offset int64) ([]string, error) {
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "runtime.activation" && event.Data.OK {
			rebuilt := append([]string(nil), event.Data.PackagesRebuilt...)
			slices.Sort(rebuilt)
			return rebuilt, nil
		}
	}
	return nil, fmt.Errorf("no successful process activation after the shared edit")
}

// harnessProcessModelCleanup requires every observed process to exit and the
// process-link wiring to disappear with the session.
func harnessProcessModelCleanup(home, appRoot string, pids []int) error {
	for _, pid := range pids {
		if !nativeBuildWaitProcessExit(pid, 15*time.Second) {
			return fmt.Errorf("process %d outlived scenery down", pid)
		}
		if err := syscall.Kill(pid, 0); err == nil {
			return fmt.Errorf("process %d outlived scenery down", pid)
		}
	}
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(paths.Socket))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if name := entry.Name(); name == "d.sock" || name == "process-link.json" || strings.HasPrefix(name, "s") && strings.HasSuffix(name, ".sock") {
			return fmt.Errorf("process-model wiring %s outlived scenery down", name)
		}
	}
	return nil
}
