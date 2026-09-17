package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
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
// `scenery up` lifecycle. Every response is attributed to its answering service
// process, and to the host and build that attest it, through its headers.
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
	Host           int
	Implementation string
	Build          string
	Generation     int
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
	env := harnessAppEnv(home)
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
		// The host attests the generation's build and itself; the answering
		// service instance is named beside it.
		pid, _ := strconv.Atoi(response.Header.Get("X-Scenery-Service-Process-ID"))
		if pid > 0 && !slices.Contains(seen, pid) {
			seen = append(seen, pid)
		}
		host, _ := strconv.Atoi(response.Header.Get("X-Scenery-Process-ID"))
		generation, _ := strconv.Atoi(response.Header.Get("X-Scenery-Process-Generation"))
		return harnessProcessModelResponse{Message: decoded.Message, PID: pid, Host: host, Implementation: response.Header.Get("X-Scenery-Service-Implementation-Revision"), Build: response.Header.Get("X-Scenery-Build-Input-Digest"), Generation: generation}, nil
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
	if echoOne.Host != host || greeterOne.Host != host || echoOne.Build == "" || echoOne.Build != greeterOne.Build || echoOne.Implementation == greeterOne.Implementation {
		return nil, fmt.Errorf("answers of one generation attest hosts %d and %d with builds %q and %q (session host %d)", echoOne.Host, greeterOne.Host, echoOne.Build, greeterOne.Build, host)
	}

	// A service process that stops on its own restarts from its own verified
	// executable, without rebuilding and without disturbing other services.
	if err := syscall.Kill(echoOne.PID, syscall.SIGKILL); err != nil {
		return nil, err
	}
	restarted, restartLatency, err := waitFor("/echo", `{"message":"hi"}`, "echo:hi")
	if err != nil {
		return nil, fmt.Errorf("a killed service process was not restarted: %w", err)
	}
	if restarted.PID == echoOne.PID || restarted.Implementation != echoOne.Implementation {
		return nil, fmt.Errorf("restarted echo = %#v; want a new process running the same implementation as %#v", restarted, echoOne)
	}
	if greet, err := call(ctx, "/greet", `{"name":"probe"}`); err != nil || greet.Message != "greeter:echo:hello probe" || greet.PID != greeterOne.PID {
		return nil, fmt.Errorf("after a restart greet = %#v, %v; want the unchanged greeter %d", greet, err, greeterOne.PID)
	}
	if !harnessWaitProcessExit(echoOne.PID, 15*time.Second) {
		return nil, fmt.Errorf("killed echo process %d did not exit", echoOne.PID)
	}
	echoOne = restarted

	// Three generations: a greet request pinned to the first generation waits
	// while greeter and then echo are replaced. Retiring the second generation
	// must not stop the first echo, which the first generation still names.
	type pinnedResult struct {
		response harnessProcessModelResponse
		err      error
		at       time.Time
	}
	pinned := make(chan pinnedResult, 1)
	go func() {
		response, err := call(ctx, "/greet", `{"name":"wait:15s:pinned"}`)
		pinned <- pinnedResult{response, err, time.Now()}
	}()
	if err := harnessWaitContext(ctx, 300*time.Millisecond); err != nil {
		return nil, err
	}
	greeterSource, echoSource := filepath.Join(appRoot, "greeter/api.go"), filepath.Join(appRoot, "echo/api.go")
	if err := harnessReplaceInFile(greeterSource, `text.Label("greeter", result.Value.Message)`, `text.Label("greeter-two", result.Value.Message)`); err != nil {
		return nil, err
	}
	greeterTwo, _, err := waitFor("/greet", `{"name":"probe"}`, "greeter-two:echo:hello probe")
	if err != nil {
		return nil, err
	}
	echoEditOffset := logOffset()
	if err := harnessReplaceInFile(echoSource, `text.Label("echo", input.Message)`, `text.Label("echo-two", input.Message)`); err != nil {
		return nil, err
	}
	edited := time.Now()
	echoTwo, _, err := waitFor("/echo", `{"message":"hi"}`, "echo-two:hi")
	if err != nil {
		return nil, err
	}
	replacementLatency, thirdPublished := time.Since(edited), time.Now()
	var inFlight pinnedResult
	select {
	case inFlight = <-pinned:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if inFlight.err != nil {
		return nil, fmt.Errorf("pinned in-flight request: %w", inFlight.err)
	}
	if inFlight.at.Before(thirdPublished) {
		return nil, fmt.Errorf("the pinned request finished before the third generation was published")
	}
	if inFlight.response.Message != "greeter:echo:hello pinned" || inFlight.response.PID != greeterOne.PID {
		return nil, fmt.Errorf("request pinned to the first generation answered %#v; want the first greeter %d and the first echo", inFlight.response, greeterOne.PID)
	}
	// Every answer names the generation that served it: the pinned request
	// keeps the generation it entered while later requests name a newer one.
	if inFlight.response.Generation != echoOne.Generation || echoTwo.Generation <= inFlight.response.Generation {
		return nil, fmt.Errorf("answers named generations %d (pinned), %d (when it entered) and %d (after two replacements)", inFlight.response.Generation, echoOne.Generation, echoTwo.Generation)
	}
	for _, pid := range []int{greeterOne.PID, echoOne.PID} {
		if !harnessWaitProcessExit(pid, 45*time.Second) {
			return nil, fmt.Errorf("process %d of the first generation did not retire after its pinned work finished", pid)
		}
	}
	current, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || current.Message != "greeter-two:echo-two:hello probe" || current.PID != greeterTwo.PID || echoTwo.PID == echoOne.PID || echoTwo.Implementation == echoOne.Implementation {
		return nil, fmt.Errorf("after both edits greet = %#v (%v), echo = %#v; want greeter %d and a new echo identity", current, err, echoTwo, greeterTwo.PID)
	}
	// The unchanged greeter answers as part of the latest build, which differs
	// from the build the pinned request attested.
	if current.Build != echoTwo.Build || current.Build == inFlight.response.Build || greeterTwo.Build == echoOne.Build {
		return nil, fmt.Errorf("attested builds: pinned %q, greeter edit %q, echo edit %q, unchanged greeter %q", inFlight.response.Build, greeterTwo.Build, echoTwo.Build, current.Build)
	}
	background, err := harnessProcessModelBackground(started.LogPath, echoEditOffset)
	if err != nil {
		return nil, err
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
	if err := harnessWaitBuildRequest(ctx, started.LogPath, failedOffset, false); err != nil {
		return nil, err
	}
	served, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || served.Message != "greeter-two:echo-two:hello probe" || served.PID != greeterTwo.PID {
		return nil, fmt.Errorf("after a failed build greet = %#v, %v", served, err)
	}
	restoredOffset := logOffset()
	if err := os.WriteFile(echoSource, original, 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, restoredOffset, true); err != nil {
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
	greeterThree, sharedLatency, err := waitFor("/greet", `{"name":"probe"}`, "greeter-two|echo-two|hello probe")
	if err != nil {
		return nil, err
	}
	echoThree, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoThree.Message != "echo-two|hi" || echoThree.PID == echoTwo.PID || greeterThree.PID == greeterTwo.PID {
		return nil, fmt.Errorf("shared edit answered greet %#v and echo %#v (%v); want both services replaced", greeterThree, echoThree, err)
	}
	rebuilt, err := harnessProcessModelRebuiltSet(started.LogPath, sharedOffset)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(rebuilt, []string{"echo_echo", "greeter_greeter"}) {
		return nil, fmt.Errorf("shared edit rebuilt %v; want both service processes and not the host", rebuilt)
	}

	// A contract-changing generation whose echo constructor fails must leave the
	// previous host and services serving. The constructor edit lands first so
	// the contract change is the build that attempts a complete generation.
	// Fixing the constructor then commits that generation with a new host.
	// Contract edits regenerate projections and retry their build, so each step
	// waits for the activation of the expected contract.
	contract, err := harnessProcessModelContract(started.LogPath)
	if err != nil {
		return nil, err
	}
	failingConstructor := [][2]string{{"return &Service{}, nil", `return nil, errors.New("probe constructor failure")`}, {"import (\n\t\"context\"\n", "import (\n\t\"context\"\n\t\"errors\"\n"}}
	constructorOffset := logOffset()
	if err := harnessReplaceEachInFile(echoSource, failingConstructor...); err != nil {
		return nil, err
	}
	if _, err := harnessProcessModelWaitActivation(ctx, started.LogPath, constructorOffset, false, func(revision string) bool { return revision == contract }); err != nil {
		return nil, err
	}
	contractOffset := logOffset()
	if err := harnessReplaceEachInFile(filepath.Join(appRoot, "echo/package.scn"), [2]string{"record \"echo_result\" {\n", "record \"echo_result\" {\n  field \"note\" {\n    type = string\n  }\n\n"}); err != nil {
		return nil, err
	}
	changedContract, err := harnessProcessModelWaitActivation(ctx, started.LogPath, contractOffset, false, func(revision string) bool { return revision != contract })
	if err != nil {
		return nil, err
	}
	kept, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || kept.Message != "greeter-two|echo-two|hello probe" || kept.PID != greeterThree.PID {
		return nil, fmt.Errorf("after a failed contract-changing generation greet = %#v, %v", kept, err)
	}
	if keptEcho, err := call(ctx, "/echo", `{"message":"hi"}`); err != nil || keptEcho.PID != echoThree.PID {
		return nil, fmt.Errorf("after a failed contract-changing generation echo = %#v, %v", keptEcho, err)
	}
	if session, err := harnessLiveSession(ctx, home, appRoot); err != nil || session.AppPID != started.Session.AppPID {
		return nil, fmt.Errorf("a failed contract-changing generation replaced the host %s: %#v, %v", started.Session.AppPID, session.AppPID, err)
	}
	commitOffset := logOffset()
	if err := os.WriteFile(echoSource, original, 0o600); err != nil {
		return nil, err
	}
	if _, err := harnessProcessModelWaitActivation(ctx, started.LogPath, commitOffset, true, func(revision string) bool { return revision == changedContract }); err != nil {
		return nil, err
	}
	// The session record names the new host once the activation is published.
	replacedHost := 0
	for begin := time.Now(); ; {
		session, err := harnessLiveSession(ctx, home, appRoot)
		if err != nil {
			return nil, err
		}
		if replacedHost, _ = strconv.Atoi(session.AppPID); replacedHost > 0 && replacedHost != host {
			break
		}
		if time.Since(begin) > 30*time.Second {
			return nil, fmt.Errorf("the committed contract-changing generation kept host %d (session reports %q)", host, session.AppPID)
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return nil, err
		}
	}
	seen = append(seen, replacedHost)
	// The new host takes over every service instance whose identity the
	// contract change left unchanged; a service with a changed identity starts
	// again, whether this build or an earlier failed one linked it.
	greeterFour, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterFour.Message != "greeter-two|echo-two|hello probe" {
		return nil, fmt.Errorf("after the committed contract-changing generation greet = %#v, %v", greeterFour, err)
	}
	echoFour, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoFour.Message != "echo-two|hi" {
		return nil, fmt.Errorf("after the committed contract-changing generation echo = %#v, %v", echoFour, err)
	}
	if !harnessWaitProcessExit(host, 15*time.Second) {
		return nil, fmt.Errorf("host %d outlived the complete replacement", host)
	}
	var committedStarted []string
	for _, service := range []struct {
		name            string
		before, current harnessProcessModelResponse
	}{{"greeter_greeter", greeterThree, greeterFour}, {"echo_echo", echoThree, echoFour}} {
		if service.current.Implementation == service.before.Implementation {
			if service.current.PID != service.before.PID {
				return nil, fmt.Errorf("unchanged %s moved from process %d to %d when the host was replaced", service.name, service.before.PID, service.current.PID)
			}
			continue
		}
		committedStarted = append(committedStarted, service.name)
		if service.current.PID == service.before.PID || !harnessWaitProcessExit(service.before.PID, 15*time.Second) {
			return nil, fmt.Errorf("changed %s process %d was not replaced by the complete generation (now %d)", service.name, service.before.PID, service.current.PID)
		}
	}

	// A contract change of greeter alone replaces the host and greeter; the new
	// host takes over the running echo instance, which keeps serving greeter's
	// calls.
	takeoverOffset := logOffset()
	if err := harnessReplaceEachInFile(filepath.Join(appRoot, "greeter/package.scn"), [2]string{"record \"greet_result\" {\n", "record \"greet_result\" {\n  field \"note\" {\n    type = string\n  }\n\n"}); err != nil {
		return nil, err
	}
	takeoverContract, err := harnessProcessModelWaitActivation(ctx, started.LogPath, takeoverOffset, true, func(revision string) bool { return revision != changedContract })
	if err != nil {
		return nil, err
	}
	takeoverRebuilt, err := harnessProcessModelRebuiltSet(started.LogPath, takeoverOffset)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(takeoverRebuilt, "greeter_greeter") || slices.Contains(takeoverRebuilt, "echo_echo") {
		return nil, fmt.Errorf("a greeter contract change relinked %v; want greeter and not echo", takeoverRebuilt)
	}
	takeoverHost := 0
	for begin := time.Now(); ; {
		session, err := harnessLiveSession(ctx, home, appRoot)
		if err != nil {
			return nil, err
		}
		if takeoverHost, _ = strconv.Atoi(session.AppPID); takeoverHost > 0 && takeoverHost != replacedHost {
			break
		}
		if time.Since(begin) > 30*time.Second {
			return nil, fmt.Errorf("the greeter contract change kept host %d (session reports %q)", replacedHost, session.AppPID)
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return nil, err
		}
	}
	seen = append(seen, takeoverHost)
	greeterFive, err := call(ctx, "/greet", `{"name":"probe"}`)
	if err != nil || greeterFive.Message != "greeter-two|echo-two|hello probe" || greeterFive.PID == greeterFour.PID || greeterFive.Implementation == greeterFour.Implementation || greeterFive.Host != takeoverHost {
		return nil, fmt.Errorf("after the greeter contract change greet = %#v, %v", greeterFive, err)
	}
	echoFive, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoFive.Message != "echo-two|hi" || echoFive.PID != echoFour.PID || echoFive.Implementation != echoFour.Implementation || echoFive.Host != takeoverHost {
		return nil, fmt.Errorf("the new host did not take over echo process %d: echo = %#v, %v", echoFour.PID, echoFive, err)
	}
	for _, pid := range []int{replacedHost, greeterFour.PID} {
		if !harnessWaitProcessExit(pid, 15*time.Second) {
			return nil, fmt.Errorf("process %d replaced by the greeter contract change is still running", pid)
		}
	}
	if err := syscall.Kill(echoFour.PID, 0); err != nil {
		return nil, fmt.Errorf("the taken-over echo process %d stopped: %w", echoFour.PID, err)
	}
	// A service replacement whose publication the host applies while its answer
	// and every confirmation are lost keeps serving without another edit: the
	// supervisor republishes the known services and retires the unconfirmed
	// generation.
	unknownOffset := logOffset()
	if err := harnessProcessModelFaults(ctx, home, appRoot, `{"faults":[{"path":"/__scenery/process/v1/generations","mode":"abort","count":4}]}`); err != nil {
		return nil, err
	}
	if err := harnessReplaceInFile(echoSource, `text.Label("echo-two", input.Message)`, `text.Label("echo-lost", input.Message)`); err != nil {
		return nil, err
	}
	if err := harnessWaitBuildRequest(ctx, started.LogPath, unknownOffset, false); err != nil {
		return nil, fmt.Errorf("a service replacement with an unknown publication outcome: %w", err)
	}
	if err := harnessProcessModelWaitLog(ctx, started.LogPath, unknownOffset, "process.publication_reconciled"); err != nil {
		return nil, err
	}
	echoSix, err := call(ctx, "/echo", `{"message":"hi"}`)
	if err != nil || echoSix.Message != "echo-two|hi" || echoSix.PID != echoFive.PID || echoSix.Generation <= echoFive.Generation+1 {
		return nil, fmt.Errorf("after the unknown publication was reconciled echo = %#v, %v; want process %d in a generation after %d", echoSix, err, echoFive.PID, echoFive.Generation+1)
	}
	stockLinks, err := harnessProcessModelStockOnly(started.LogPath)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"unknown_publication_reconciled_generation": echoSix.Generation,
		"stock_entrypoint_links":                    stockLinks,
		"host_pids":                                 []int{host, replacedHost, takeoverHost},
		"echo_pids":                                 []int{echoOne.PID, echoTwo.PID, echoThree.PID, echoFour.PID, echoFive.PID},
		"greeter_pids":                              []int{greeterOne.PID, greeterTwo.PID, greeterThree.PID, greeterFour.PID, greeterFive.PID},
		"committed_contract_started":                committedStarted,
		"greeter_contract_relinked":                 takeoverRebuilt,
		"greeter_contract_revision":                 takeoverContract,
		"echo_taken_over_by_new_host":               echoFive.PID,
		"pinned_across_three_generations":           inFlight.response.Message,
		"pinned_generation":                         inFlight.response.Generation,
		"current_generation_after_two_edits":        echoTwo.Generation,
		"crash_restart_to_response_ms":              restartLatency.Milliseconds(),
		"echo_edit_to_response_ms":                  replacementLatency.Milliseconds(),
		"shared_edit_to_response_ms":                sharedLatency.Milliseconds(),
		"background_transfer":                       background,
		"failed_build_kept_generation":              true,
		"identical_source_kept_echo":                true,
		"shared_edit_rebuilt":                       rebuilt,
		"failed_contract_generation_kept_serving":   true,
		"committed_contract_revision":               changedContract,
		"proof":                                     "public_scenery_up_process_model_replaced_only_changed_services_with_retained_generations_background_activation_host_replacement_takeover_and_identity_attribution",
	}, nil
}

// harnessProcessModelFaults replaces the fault rules of the session's process
// host through its private control listener.
func harnessProcessModelFaults(ctx context.Context, home, appRoot, rules string) error {
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(paths.Socket), "process-link.json"))
	if err != nil {
		return err
	}
	var link struct {
		Token    string `json:"token"`
		Dispatch struct {
			Address string `json:"address"`
		} `json:"dispatch"`
	}
	if err := json.Unmarshal(data, &link); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", link.Dispatch.Address)
	}}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://scenery-host/__scenery/process/v1/faults", strings.NewReader(rules))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+link.Token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("process host refused fault rules: HTTP %d", response.StatusCode)
	}
	return nil
}

// harnessProcessModelWaitLog waits for a supervisor event after offset.
func harnessProcessModelWaitLog(ctx context.Context, log string, offset int64, event string) error {
	for begin := time.Now(); time.Since(begin) < 90*time.Second; {
		data, err := os.ReadFile(log)
		if err != nil {
			return err
		}
		if offset <= int64(len(data)) && bytes.Contains(data[offset:], []byte(`"`+event+`"`)) {
			return nil
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("the session never reported %s", event)
}

// harnessProcessModelStockOnly requires every service entrypoint of the session
// to have been linked by stock Go: no recipe was recorded and no entrypoint was
// linked from one. It reports the number of stock entrypoint links.
func harnessProcessModelStockOnly(log string) (int, error) {
	events, err := harnessWatchEvents(log, 0)
	if err != nil {
		return 0, err
	}
	links := 0
	for _, event := range events {
		if event.Type != "build.step" {
			continue
		}
		if event.Data.Name == "build.recipe_capture" || strings.HasPrefix(event.Data.Reason, "retained_process_") || strings.HasPrefix(event.Data.Reason, "retained_development_process_") {
			return 0, fmt.Errorf("the process-model session used retained entrypoint recipes: %s %s", event.Data.Name, event.Data.Reason)
		}
		if event.Data.Name == "build.artifact" && strings.HasPrefix(event.Data.Reason, "linked_development_process_") {
			links++
		}
	}
	if links == 0 {
		return 0, fmt.Errorf("the process-model session reported no stock entrypoint link")
	}
	return links, nil
}

// harnessProcessModelBackground requires the echo replacement to drain the
// replaced instance before activating its successor.
func harnessProcessModelBackground(log string, offset int64) ([]string, error) {
	events, err := harnessWatchEvents(log, offset)
	if err != nil {
		return nil, err
	}
	var transfer []string
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "process.background" {
			if !event.Data.OK {
				return nil, fmt.Errorf("process background %s failed", event.Data.Reason)
			}
			transfer = append(transfer, event.Data.Reason)
		}
	}
	if !slices.Equal(transfer, []string{"drain", "activate"}) {
		return nil, fmt.Errorf("echo replacement transferred background work as %v; want drain then activate", transfer)
	}
	return transfer, nil
}

// harnessProcessModelContract returns the contract revision of the latest
// successful activation.
func harnessProcessModelContract(log string) (string, error) {
	events, err := harnessWatchEvents(log, 0)
	if err != nil {
		return "", err
	}
	contract := ""
	for _, event := range events {
		if event.Type == "build.step" && event.Data.Name == "runtime.activation" && event.Data.OK && event.Data.ContractRevision != "" {
			contract = event.Data.ContractRevision
		}
	}
	if contract == "" {
		return "", fmt.Errorf("no successful activation recorded a contract revision")
	}
	return contract, nil
}

// harnessProcessModelWaitActivation waits for the next activation after
// offset, skipping builds a regenerated projection superseded, and requires its
// outcome and contract revision.
func harnessProcessModelWaitActivation(ctx context.Context, log string, offset int64, ok bool, contract func(string) bool) (string, error) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		events, err := harnessWatchEvents(log, offset)
		if err != nil {
			return "", err
		}
		for _, event := range events {
			if event.Type != "build.step" || event.Data.Name != "runtime.activation" {
				continue
			}
			if event.Data.OK != ok || event.Data.ContractRevision == "" || !contract(event.Data.ContractRevision) {
				return "", fmt.Errorf("activation ok=%t with contract %q after offset %d does not match the expected outcome ok=%t", event.Data.OK, event.Data.ContractRevision, offset, ok)
			}
			return event.Data.ContractRevision, nil
		}
		if err := harnessWaitContext(ctx, 20*time.Millisecond); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no activation followed the edit")
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

// harnessReplaceEachInFile applies every unique replacement in one save.
func harnessReplaceEachInFile(path string, replacements ...[2]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, replacement := range replacements {
		if bytes.Count(data, []byte(replacement[0])) != 1 {
			return fmt.Errorf("%s does not contain exactly one %q", path, replacement[0])
		}
		data = bytes.Replace(data, []byte(replacement[0]), []byte(replacement[1]), 1)
	}
	return harnessAtomicWatchSave(path, data)
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
		if !harnessWaitProcessExit(pid, 15*time.Second) {
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
		name := entry.Name()
		for _, pattern := range []string{"d.sock", "s[0-9]*.sock", "process-link.json", "host-state"} {
			if matched, _ := filepath.Match(pattern, name); matched {
				return fmt.Errorf("process-model wiring %s outlived scenery down", name)
			}
		}
	}
	return nil
}

// harnessWaitProcessExit reports whether the process exited within timeout.
func harnessWaitProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}
