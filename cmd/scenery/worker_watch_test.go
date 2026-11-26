package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerabi"
	workerruntime "scenery.sh/runtime"
)

func newTestWorkerControl(t *testing.T, handler http.HandlerFunc) (string, *http.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wrev-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Protocols: workerabi.Protocols(), ReadHeaderTimeout: time.Second, Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := workerControlClient(sock)
	t.Cleanup(client.CloseIdleConnections)
	return dir, client
}

func newTestDummyWorkerProcess(dir string, pid int, program workerProgram) *workerProcess {
	return &workerProcess{
		program: program,
		testPID: pid,
		socket:  filepath.Join(dir, fmt.Sprintf("w-%d.sock", pid)),
		token:   strings.Repeat("a", 32),
		done:    make(chan struct{}),
	}
}

func TestWorkerWatcherRepublishesGenerationOnHostOnlyConfigChangeAndForcedRetire(t *testing.T) {
	var mu sync.Mutex
	var currentGen uint64 = 1
	var published []map[string]any
	var forcedAttempts int
	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			current := currentGen
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			if gen, ok := body["generation"].(float64); ok {
				currentGen = uint64(gen)
			}
			published = append(published, body)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			if r.URL.Query().Get("force") != "true" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			mu.Lock()
			forcedAttempts++
			attempt := forcedAttempts
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	workerCfg := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-1", Consumer: "mod/service/a", Values: map[string]json.RawMessage{}, Secrets: map[string][]byte{}}
	hostCfg1 := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-1", Consumer: "$host", Values: map[string]json.RawMessage{"public.flag": json.RawMessage(`false`)}, Public: []string{"public.flag"}, Secrets: map[string][]byte{}}
	hostCfg2 := &workerruntime.ConfigSnapshot{Kind: workerruntime.ConfigSnapshotKind, AppID: "app", Environment: "local", Revision: "rev-2", Consumer: "$host", Values: map[string]json.RawMessage{"public.flag": json.RawMessage(`false`)}, Public: []string{"public.flag"}, Secrets: map[string][]byte{}}

	aliveProc := newTestDummyWorkerProcess(dir, 1001, workerProgram{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity, config: workerCfg})
	bundle1 := workerBundle{
		ContractRevision: "c1",
		Host:             workerruntime.ProcessHostConfig{DevelopmentCall: true, WorkerConfig: map[string]*workerruntime.ConfigSnapshot{"mod/service/a": workerCfg}},
		Programs:         []workerProgram{aliveProc.program},
		HostConfigKeys:   []string{"public.flag"},
		config:           hostCfg1,
	}
	bundle2 := bundle1
	bundle2.config = hostCfg2

	watch := &workerWatcher{token: "tok", client: client, bundle: bundle1, active: map[string]*workerProcess{"a": aliveProc}, generation: 1, sequence: 1, retireDrainTimeout: 10 * time.Millisecond, retireForceInterval: 5 * time.Millisecond}
	watch.generations.add(1, watch.active)
	if err := watch.replace(context.Background(), bundle2, nil); err != nil {
		t.Fatalf("watch.replace(host revision bump) = %v", err)
	}
	watch.retirements.Wait()
	if watch.generation != 2 || watch.active["a"] != aliveProc {
		t.Fatalf("watch state after host-only change = (gen=%d, active=%p), want gen=2 and reused process", watch.generation, watch.active["a"])
	}
	mu.Lock()
	defer mu.Unlock()
	if forcedAttempts < 2 || len(published) != 1 {
		t.Fatalf("forcedAttempts=%d, published=%d; want >=2, 1", forcedAttempts, len(published))
	}
	if gen, _ := published[0]["generation"].(float64); gen != 2 {
		t.Fatalf("published generation = %v, want 2", published[0]["generation"])
	}
	if hostPayload, _ := published[0]["host_config"].(map[string]any); hostPayload == nil || hostPayload["revision"] != "rev-2" {
		t.Fatalf("published host_config = %#v, want revision rev-2", published[0]["host_config"])
	}
}

func TestWorkerWatcherCancelsActiveRetirementsOnHostExitAndRetriesFailedConfigApply(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	var mu sync.Mutex
	var currentGen uint64 = 1
	allowPublish := false
	retireForcedSeen := make(chan struct{})
	var retireForcedOnce sync.Once
	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			current := currentGen
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			mu.Lock()
			ok := allowPublish
			if ok {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) == nil {
					if gen, okGen := body["generation"].(float64); okGen {
						currentGen = uint64(gen)
					}
				}
			}
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			if strings.HasSuffix(r.URL.Path, "/1") || strings.HasSuffix(r.URL.Path, "/2") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.Query().Get("force") == "true" {
				retireForcedOnce.Do(func() { close(retireForcedSeen) })
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	root := filepath.Join(dir, "app")
	writeWatchFile(t, root, ".scenery.json", `{"name":"cfg-retry","id":"cfg-retry","envs":{"local":{"default":true}}}`)
	store, err := devConfigStore(app.Config{Name: "cfg-retry", ID: "cfg-retry"})
	if err != nil {
		t.Fatal(err)
	}
	mutateFlag := func(val string) {
		t.Helper()
		if _, err := store.Mutate(context.Background(), "local", appconfig.Mutation{Key: "a.flag", Value: json.RawMessage(val)}); err != nil {
			t.Fatal(err)
		}
	}
	mutateFlag("false")
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &compiler.Result{
		Root: root,
		Manifest: &graph.Manifest{
			ContractRevision: "c1",
			Resources: []graph.Resource{{
				Address: "mod/service/a", Module: "mod", Kind: "scenery.service", Name: "a",
				Spec: map[string]any{"config_schema": []any{map[string]any{"name": "flag", "type": "bool", "public": true}}},
			}},
		},
	}
	inputWatch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inputWatch.fs.Close() }()

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	bundle1 := workerBundle{
		ContractRevision: "c1",
		Host:             workerruntime.ProcessHostConfig{DevelopmentCall: true},
		Programs:         []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity}},
		HostConfigKeys:   []string{"a.flag"},
	}
	if err := bundle1.resolveDevelopmentConfig(context.Background(), compiled, cfg, "local"); err != nil {
		t.Fatal(err)
	}
	bundle1.setWorkerConfig()
	aliveProc := newTestDummyWorkerProcess(dir, 1002, bundle1.Programs[0])
	watch := &workerWatcher{
		token: "tok", client: client, dev: &workerDevelopment{watch: inputWatch, compiled: compiled, envName: "local"},
		bundle: bundle1, active: map[string]*workerProcess{"a": aliveProc}, generation: 1, sequence: 1,
		retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond,
	}

	mutateFlag("true")
	if watch.applyConfigurationChange(context.Background()) {
		t.Fatal("applyConfigurationChange must return false when publish fails")
	}
	mu.Lock()
	allowPublish = true
	mu.Unlock()
	if !watch.applyConfigurationChange(context.Background()) {
		t.Fatal("applyConfigurationChange must return true once Host accepts publish")
	}

	// Bump config revision so watch.run's startup check triggers a real replacement
	// and starts a stuck forced-retirement goroutine under runCtx, then exit via hostDone.
	mutateFlag("false")
	hostDone := make(chan error, 1)
	sentinel := errors.New("host exited")
	runDone := make(chan error, 1)
	go func() { runDone <- watch.run(context.Background(), hostDone) }()
	select {
	case <-retireForcedSeen:
	case <-time.After(time.Second):
		hostDone <- sentinel
		t.Fatal("forced retire did not start within 1s")
	}
	hostDone <- sentinel
	select {
	case runErr := <-runDone:
		if !errors.Is(runErr, sentinel) {
			t.Fatalf("watcher.run = %v, want %v", runErr, sentinel)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher.run deadlocked on active retirements after hostDone")
	}
}

func TestWorkerWatcherReconcilesUnconfirmedGenerationsWithoutLeakingProcesses(t *testing.T) {
	var mu sync.Mutex
	var currentGen uint64 = 1
	putMode := "fail" // "fail", "ok", "commit_and_fail"
	getOK := false
	conflictFence7 := false
	var retired []string

	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			ok := getOK
			current := currentGen
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			mode := putMode
			if mode == "ok" || mode == "commit_and_fail" {
				if gen, okGen := body["generation"].(float64); okGen {
					currentGen = uint64(gen)
				}
			}
			mu.Unlock()
			if mode == "ok" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			gen := strings.TrimPrefix(r.URL.Path, "/__scenery/process/v1/generations/")
			mu.Lock()
			retired = append(retired, gen)
			if conflictFence7 && gen == "7" {
				conflictFence7 = false
				currentGen = 7
				mu.Unlock()
				w.WriteHeader(http.StatusConflict)
				return
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	makeBundle := func(digest string) workerBundle {
		return workerBundle{
			ContractRevision: "c1",
			Host:             workerruntime.ProcessHostConfig{DevelopmentCall: true},
			Programs:         []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: digest, Identity: identity}},
		}
	}
	bundle1 := makeBundle("sha256:1")
	bundle2 := makeBundle("sha256:2")
	bundle3 := makeBundle("sha256:3")

	p1 := newTestDummyWorkerProcess(dir, 101, bundle1.Programs[0])
	var spawned []*workerProcess
	startCalls := 0
	watch := &workerWatcher{
		token: "tok", client: client, bundle: bundle1, active: map[string]*workerProcess{"a": p1},
		all: []*workerProcess{p1}, generation: 1, sequence: 1,
		retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond,
		startWorker: func(_ context.Context, _, _, _ string, program workerProgram) (*workerProcess, error) {
			startCalls++
			proc := newTestDummyWorkerProcess(dir, 200+startCalls, program)
			spawned = append(spawned, proc)
			return proc, nil
		},
	}
	watch.generations.add(1, watch.active)

	// Case 1: Outage -> Rejection. PUT fails and GET fails: P2 enters unconfirmed.
	if err := watch.replace(context.Background(), bundle2, nil); err == nil {
		t.Fatal("replace during publish+status outage must fail")
	}
	if startCalls != 1 || len(watch.unconfirmed) != 1 || watch.generation != 1 {
		t.Fatalf("after first outage = (startCalls=%d, unconfirmed=%d, gen=%d), want (1, 1, 1)", startCalls, len(watch.unconfirmed), watch.generation)
	}
	p2 := spawned[0]
	// Retrying while Host status is still failing must not spawn another set of processes.
	if err := watch.replace(context.Background(), bundle2, nil); err == nil {
		t.Fatal("retry during ongoing status outage must fail before spawning workers")
	}
	if startCalls != 1 || len(watch.unconfirmed) != 1 {
		t.Fatalf("ongoing outage spawned duplicate workers: startCalls=%d, unconfirmed=%d", startCalls, len(watch.unconfirmed))
	}

	// Host recovers still reporting generation 1 -> Gen 2 is fenced via DELETE /generations/2 before P2 stops, P3 publishes Gen 3, Gen 1 retires.
	mu.Lock()
	getOK = true
	putMode = "ok"
	retired = nil
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle2, nil); err != nil {
		t.Fatalf("replace after recovery (rejected candidate) = %v", err)
	}
	watch.retirements.Wait()
	if startCalls != 2 || watch.generation != 3 {
		t.Fatalf("after recovery = (startCalls=%d, gen=%d), want (2, 3)", startCalls, watch.generation)
	}
	mu.Lock()
	retiredAfterCase1 := slices.Clone(retired)
	mu.Unlock()
	if !slices.Contains(retiredAfterCase1, "2") {
		t.Fatalf("rejected candidate Gen 2 was not fenced before stopping workers: retired=%v", retiredAfterCase1)
	}
	p3 := spawned[1]
	select {
	case <-p2.done:
	default:
		t.Fatal("rejected unconfirmed worker P2 was not stopped")
	}
	select {
	case <-p1.done:
	default:
		t.Fatal("previous generation P1 was not stopped on retire")
	}
	if slices.Contains(watch.all, p2) || !slices.Contains(watch.all, p3) {
		t.Fatalf("watch.all = %v, want P2 pruned and P3 retained", watch.all)
	}

	// Case 2: Outage -> Adoption. Host commits Gen 4 then fails HTTP, GET fails once, then recovers with current=4.
	mu.Lock()
	putMode = "commit_and_fail"
	getOK = false
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle3, nil); err == nil {
		t.Fatal("replace during commit_and_fail + status outage must fail")
	}
	if startCalls != 3 || len(watch.unconfirmed) != 1 || watch.unconfirmed[0].number != 4 {
		t.Fatalf("after commit_and_fail = (startCalls=%d, unconfirmed=%+v), want startCalls=3 num=4", startCalls, watch.unconfirmed)
	}
	p4 := spawned[2]
	mu.Lock()
	getOK = true
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle3, nil); err != nil {
		t.Fatalf("replace after recovery (adopted candidate) = %v", err)
	}
	watch.retirements.Wait()
	if startCalls != 3 || watch.generation != 4 || watch.active["a"] != p4 || len(watch.unconfirmed) != 0 {
		t.Fatalf("after adoption = (startCalls=%d, gen=%d, active=%p, unconfirmed=%d), want (3, 4, %p, 0)", startCalls, watch.generation, watch.active["a"], len(watch.unconfirmed), p4)
	}
	select {
	case <-p3.done:
	default:
		t.Fatal("Gen 3 P3 was not retired and stopped after adopting Gen 4")
	}

	// Case 3: Outage -> Superseded. Host advances to Gen 6 while Gen 5 is unconfirmed.
	mu.Lock()
	putMode = "fail"
	getOK = false
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle1, nil); err == nil {
		t.Fatal("replace Gen 5 during outage must fail")
	}
	p5 := spawned[3]
	mu.Lock()
	currentGen = 6
	getOK = true
	retired = nil
	mu.Unlock()
	if err := watch.reconcileUnconfirmed(context.Background()); err != nil {
		t.Fatalf("reconcileUnconfirmed(superseded) = %v", err)
	}
	watch.retirements.Wait()
	select {
	case <-p5.done:
	default:
		t.Fatal("superseded unconfirmed worker P5 was not retired and stopped")
	}
	mu.Lock()
	retiredCopy := slices.Clone(retired)
	mu.Unlock()
	if !slices.Contains(retiredCopy, "5") {
		t.Fatalf("retired generations = %v, want 5 included", retiredCopy)
	}
	if watch.sequence < 6 {
		t.Fatalf("watch.sequence = %d, want >= 6 so next generation advances past Host", watch.sequence)
	}

	// Case 4: Late PUT wins race against DELETE fence (GET initially sees stale 6, DELETE returns 409 because Gen 7 committed).
	mu.Lock()
	putMode = "fail"
	getOK = true
	conflictFence7 = true
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle2, nil); err != nil {
		t.Fatalf("replace when late PUT wins fence race = %v", err)
	}
	watch.retirements.Wait()
	p7 := spawned[4]
	if watch.generation != 7 || watch.active["a"] != p7 || len(watch.unconfirmed) != 0 {
		t.Fatalf("after winning fence race = (gen=%d, active=%p, unconfirmed=%d), want (7, %p, 0)", watch.generation, watch.active["a"], len(watch.unconfirmed), p7)
	}
	select {
	case <-p7.done:
		t.Fatal("adopted winner P7 was erroneously stopped")
	default:
	}
	select {
	case <-p4.done:
	default:
		t.Fatal("previous Gen 4 P4 was not retired after adopting Gen 7")
	}

	// Case 5: Empty unconfirmed + Host reports higher current > pubSeq -> sequence must still advance past Host.
	mu.Lock()
	putMode = "fail"
	getOK = true
	currentGen = 12
	mu.Unlock()
	if err := watch.replace(context.Background(), bundle3, nil); err == nil {
		t.Fatal("replace behind Host current generation must fail")
	}
	watch.retirements.Wait()
	p8 := spawned[5]
	select {
	case <-p8.done:
	default:
		t.Fatal("superseded Gen 8 worker P8 was not stopped")
	}
	if watch.sequence < 12 {
		t.Fatalf("watch.sequence = %d after current>pubSeq with empty unconfirmed, want >= 12", watch.sequence)
	}
}

func TestWorkerWatcherRecoversNonDevRestartAfterOutage(t *testing.T) {
	var mu sync.Mutex
	var currentGen uint64 = 1
	putOK := false
	getOK := false
	getFailed := make(chan struct{}, 16)
	published3 := make(chan struct{}, 1)

	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			ok := getOK
			current := currentGen
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				select {
				case getFailed <- struct{}{}:
				default:
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			ok := putOK
			gen, _ := body["generation"].(float64)
			if ok {
				currentGen = uint64(gen)
			}
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			if uint64(gen) == 3 {
				select {
				case published3 <- struct{}{}:
				default:
				}
			}
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	bundle := workerBundle{
		ContractRevision: "c1",
		Programs:         []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity}},
	}
	p1 := newTestDummyWorkerProcess(dir, 301, bundle.Programs[0])
	unconfirmedSpawned := make(chan *workerProcess, 1)
	var spawned []*workerProcess
	watch := &workerWatcher{
		token: "tok", client: client, bundle: bundle, active: map[string]*workerProcess{"a": p1},
		all: []*workerProcess{p1}, generation: 1, sequence: 1,
		retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond, retryDelay: 10 * time.Millisecond,
		startWorker: func(_ context.Context, _, _, _ string, program workerProgram) (*workerProcess, error) {
			mu.Lock()
			pid := 310 + len(spawned)
			proc := newTestDummyWorkerProcess(dir, pid, program)
			spawned = append(spawned, proc)
			mu.Unlock()
			if pid == 310 {
				unconfirmedSpawned <- proc
			}
			return proc, nil
		},
	}

	hostDone := make(chan error, 1)
	runDone := make(chan error, 1)
	go func() { runDone <- watch.run(context.Background(), hostDone) }()

	// Worker P1 exits during a control outage -> watcher spawns P2 into unconfirmed and retries on timer.
	p1.closeDone()
	var p2 *workerProcess
	select {
	case p2 = <-unconfirmedSpawned:
	case <-time.After(time.Second):
		hostDone <- nil
		t.Fatal("non-dev watcher did not spawn replacement P2 after P1 exit")
	}
	// Wait for initial GET failure + at least one timer retry failure so Gen 2 is guaranteed in unconfirmed.
	for range 2 {
		select {
		case <-getFailed:
		case <-time.After(time.Second):
			hostDone <- nil
			t.Fatal("non-dev watcher did not fail GET /generations as expected during outage")
		}
	}

	// Host control recovers with no new exit events -> retry timer must reconcile Gen 2, stop P2, start P3, and publish Gen 3.
	mu.Lock()
	getOK = true
	putOK = true
	mu.Unlock()
	select {
	case <-published3:
	case <-time.After(time.Second):
		hostDone <- nil
		t.Fatal("non-dev watcher did not retry and publish replacement generation 3 after outage")
	}

	hostDone <- nil
	if err := <-runDone; err != nil {
		t.Fatalf("watcher.run = %v, want nil", err)
	}
	select {
	case <-p2.done:
	default:
		t.Fatal("rejected unconfirmed worker P2 was not stopped after retry recovery")
	}
	if watch.generation != 3 || len(watch.unconfirmed) != 0 {
		t.Fatalf("final watcher state = (gen=%d, unconfirmed=%d), want (3, 0)", watch.generation, len(watch.unconfirmed))
	}
}

func TestWorkerWatcherOnAdoptUpdatesDevStateAndClearsFailedAttempt(t *testing.T) {
	var mu sync.Mutex
	var currentGen uint64 = 1
	getOK := false

	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			ok := getOK
			current := currentGen
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			mu.Lock()
			currentGen = 2
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	inputsA := workerInputs{"a.go": {Digest: [32]byte{1}, Mode: 0o644}}
	inputsB := workerInputs{"a.go": {Digest: [32]byte{2}, Mode: 0o644}}
	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	bundle1 := workerBundle{ContractRevision: "c1", Programs: []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity}}}
	bundle2 := workerBundle{ContractRevision: "c1", Programs: []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:2", Identity: identity}}}

	p1 := newTestDummyWorkerProcess(dir, 401, bundle1.Programs[0])
	control := &workerDevControl{}
	dev := &workerDevelopment{applied: inputsA}
	watch := &workerWatcher{
		token: "tok", client: client, dev: dev, devControl: control,
		bundle: bundle1, active: map[string]*workerProcess{"a": p1}, all: []*workerProcess{p1},
		generation: 1, sequence: 1, retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond,
		startWorker: func(_ context.Context, _, _, _ string, program workerProgram) (*workerProcess, error) {
			return newTestDummyWorkerProcess(dir, 402, program), nil
		},
	}
	watch.generations.add(1, watch.active)

	err := watch.replaceWithAdopt(context.Background(), bundle2, nil, func(context.Context) {
		watch.dev.applied = inputsB
		timing := workerRebuildTiming{Generation: watch.generation}
		watch.devControl.publish(inputsB, watch.bundle, watch.generation, &timing)
	})
	if err == nil {
		t.Fatal("replaceWithAdopt during commit_and_fail outage must return error")
	}
	control.failure(inputsB, &devBuildFailure{Stderr: "outage"}, &workerRebuildTiming{})

	mu.Lock()
	getOK = true
	mu.Unlock()
	if err := watch.reconcileUnconfirmed(context.Background()); err != nil {
		t.Fatalf("reconcileUnconfirmed(adopt) = %v", err)
	}
	watch.retirements.Wait()
	if !maps.Equal(watch.dev.applied, inputsB) {
		t.Fatalf("watch.dev.applied was not updated on adoption: %+v", watch.dev.applied)
	}
	control.mu.RLock()
	serving := control.serving
	attempt := control.attempt
	control.mu.RUnlock()
	if attempt != nil {
		t.Fatalf("control.attempt was not cleared on adoption: %+v", attempt)
	}
	if serving == nil || serving.Generation != 2 || serving.SourceSnapshotDigest != workerSourceDigest(inputsB) {
		t.Fatalf("control.serving = %+v, want generation 2 with digest for inputsB", serving)
	}
}

func TestWorkerWatcherOnAdoptRestoresExplicitDepsAndRevertReconcilesFirst(t *testing.T) {
	var mu sync.Mutex
	var currentGen uint64 = 1
	putMode := "commit_and_fail"
	getOK := false

	dir, client := newTestWorkerControl(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodGet:
			mu.Lock()
			ok := getOK
			current := currentGen
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": current})
		case r.URL.Path == "/__scenery/process/v1/generations" && r.Method == http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			mode := putMode
			if mode == "ok" || mode == "commit_and_fail" {
				if gen, okGen := body["generation"].(float64); okGen {
					currentGen = uint64(gen)
				}
			}
			mu.Unlock()
			if mode == "ok" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/__scenery/process/v1/generations/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	root := filepath.Join(dir, "app")
	extraRel := filepath.Join("node_modules", "extra", "index.js")
	writeWatchFile(t, root, ".scenery.json", `{"name":"adopt-deps","id":"adopt-deps","envs":{"local":{"default":true}}}`)
	writeWatchFile(t, root, extraRel, "export const v = 1;\n")
	writeWatchFile(t, root, "a.txt", "v2\n")

	inputWatch, err := openWorkerInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inputWatch.fs.Close() }()

	baseInputs, err := inputWatch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	inputsWithExtra, err := reconcileExplicitJSDependencies(inputWatch, baseInputs, nil, []string{extraRel})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate refresh's defer rollback after publish enters unconfirmed.
	inputWatch.setExplicit(nil, false)

	compiled := &compiler.Result{Root: root, Manifest: &graph.Manifest{ContractRevision: "c1"}}
	identity := workerProcessIdentity{ContractRevision: "c1", ImplementationRevision: "i1", BuildInputDigest: "b1", Target: "go"}
	bundle1 := workerBundle{ContractRevision: "c1", Programs: []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:1", Identity: identity}}}
	bundle2 := workerBundle{ContractRevision: "c1", Programs: []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:2", Identity: identity}}}
	bundle3 := workerBundle{ContractRevision: "c1", Programs: []workerProgram{{Service: "mod/service/a", Process: "a", Runtime: "go", Digest: "sha256:3", Identity: identity}}}

	p1 := newTestDummyWorkerProcess(dir, 501, bundle1.Programs[0])
	control := &workerDevControl{watch: inputWatch}
	dev := &workerDevelopment{watch: inputWatch, compiled: compiled, envName: "local", applied: baseInputs}
	watch := &workerWatcher{
		token: "tok", client: client, dev: dev, devControl: control,
		bundle: bundle1, active: map[string]*workerProcess{"a": p1}, all: []*workerProcess{p1},
		generation: 1, sequence: 1, retireDrainTimeout: 5 * time.Millisecond, retireForceInterval: 5 * time.Millisecond,
		startWorker: func(_ context.Context, _, _, _ string, program workerProgram) (*workerProcess, error) {
			return newTestDummyWorkerProcess(dir, 502, program), nil
		},
	}
	watch.generations.add(1, watch.active)

	candidateB := *dev
	if err := watch.replaceWithAdopt(context.Background(), bundle2, nil, func(adoptCtx context.Context) {
		timing := workerRebuildTiming{Generation: watch.generation}
		watch.commitRefreshCandidate(adoptCtx, candidateB, nil, []string{extraRel}, false, "", workerEdit{}, inputsWithExtra, time.Now(), &timing)
	}); err == nil {
		t.Fatal("replaceWithAdopt during commit_and_fail outage must fail")
	}
	control.failure(inputsWithExtra, &devBuildFailure{Stderr: "outage"}, &workerRebuildTiming{})

	mu.Lock()
	getOK = true
	mu.Unlock()
	if err := watch.reconcileUnconfirmed(context.Background()); err != nil {
		t.Fatalf("reconcileUnconfirmed(explicit deps adopt) = %v", err)
	}
	watch.retirements.Wait()
	if !slices.Equal(inputWatch.explicitList(), []string{extraRel}) {
		t.Fatalf("explicit deps after adoption = %v, want [%s]", inputWatch.explicitList(), extraRel)
	}
	captured, err := inputWatch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	control.mu.RLock()
	servingDigest := control.serving.SourceSnapshotDigest
	attempt := control.attempt
	control.mu.RUnlock()
	if attempt != nil || servingDigest != workerSourceDigest(captured) {
		t.Fatalf("after explicit dep adoption: attempt=%v digest=%q want nil & %q", attempt, servingDigest, workerSourceDigest(captured))
	}

	// Revert during unconfirmed: refresh(inputsWithExtra) where current == watch.dev.applied must still reconcile Gen 3 first.
	inputsC := maps.Clone(inputsWithExtra)
	inputsC["a.txt"] = workerInput{Digest: [32]byte{9}, Mode: 0o644}
	p3 := newTestDummyWorkerProcess(dir, 503, bundle3.Programs[0])
	watch.sequence = 3
	watch.generations.add(3, map[string]*workerProcess{"a": p3})
	watch.unconfirmed = []unconfirmedWorkerGeneration{{
		number: 3, bundle: bundle3, processes: map[string]*workerProcess{"a": p3}, started: []*workerProcess{p3},
		onAdopt: func(context.Context) { watch.dev.applied = inputsC },
	}}
	mu.Lock()
	getOK = false
	mu.Unlock()
	if _, err := watch.refresh(context.Background(), inputsWithExtra, time.Now()); err == nil {
		t.Fatal("refresh with unconfirmed generation must fail while Host status is unavailable even when current == applied")
	}
	mu.Lock()
	getOK = true
	currentGen = 3
	putMode = "ok"
	mu.Unlock()
	applied, err := watch.refresh(context.Background(), inputsWithExtra, time.Now())
	if err != nil {
		t.Fatalf("refresh after revert & unconfirmed adoption = %v", err)
	}
	watch.retirements.Wait()
	control.mu.RLock()
	servingAfterRevert := control.serving.SourceSnapshotDigest
	control.mu.RUnlock()
	if watch.generation != 3 || !maps.Equal(applied, inputsWithExtra) || !maps.Equal(watch.dev.applied, inputsWithExtra) || servingAfterRevert != workerSourceDigest(inputsWithExtra) {
		t.Fatalf("after revert refresh = (gen=%d, appliedEq=%v, devAppliedEq=%v, digestEq=%v)", watch.generation, maps.Equal(applied, inputsWithExtra), maps.Equal(watch.dev.applied, inputsWithExtra), servingAfterRevert == workerSourceDigest(inputsWithExtra))
	}
}

func TestPrepareWorkerDevAssistantsStartsEmptyAndReconcilesFirstAssistant(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{"name":"assist-empty","id":"assist-empty","envs":{"local":{"default":true}}}`)

	emptyResult := &compiler.Result{Root: root, Manifest: &graph.Manifest{ContractRevision: "c1"}}
	watch := &workerInputWatch{root: root}
	dev := &workerDevelopment{watch: watch, compiled: emptyResult, envName: "local"}

	assistants, configPath, defs, digests, restoreEnv, err := prepareWorkerDevAssistants(context.Background(), dev, dir)
	if err != nil {
		t.Fatalf("prepareWorkerDevAssistants(0 assistants) = %v", err)
	}
	t.Cleanup(restoreEnv)
	t.Cleanup(func() { _ = assistants.Close() })
	if len(defs) != 0 || len(digests) != 0 {
		t.Fatalf("initial 0-assistant state = (defs=%d, digests=%d), want (0, 0)", len(defs), len(digests))
	}
	cfg, err := workerruntime.LoadAssistantRuntimeConfig(configPath)
	if err != nil || len(cfg.Assistants) != 0 {
		t.Fatalf("initial runtime config = (%+v, %v), want empty assistants", cfg, err)
	}

	assistants.config.NodeResolver = func(context.Context) (string, string, string, error) {
		return "/managed/node", "/managed/npm", "/managed/home", nil
	}
	assistants.config.InstallDeps = func(context.Context, string, string, string) error { return nil }
	assistants.config.BuildOverlay = func(context.Context, string, string, string, string) error { return nil }
	assistants.config.ProcessFactory = func(context.Context, devProcessStartRequest) (*devManagedProcess, error) {
		return nil, errors.New("stub helper exit")
	}

	writeWatchFile(t, root, filepath.Join("assistants", "support", "package.json"), "{}\n")
	writeWatchFile(t, root, filepath.Join("assistants", "support", "package-lock.json"), "{}\n")
	writeWatchFile(t, root, filepath.Join("assistants", "support", "index.ts"), "export {};\n")

	oneAssistantResult := &compiler.Result{
		Root: root,
		Manifest: &graph.Manifest{
			ContractRevision: "c1",
			Resources: []graph.Resource{
				{
					Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support",
					Spec: map[string]any{
						"mcp_server":     "mcp_server.support",
						"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
					},
				},
				{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Name: "support", Module: "app", Spec: map[string]any{"max_input_bytes": 1024, "max_result_bytes": 1024}},
			},
		},
	}

	state := &workerAssistantWatchState{supervisor: assistants, configPath: configPath, defs: slices.Clone(defs), digests: maps.Clone(digests)}
	reconcileWorkerAssistants(context.Background(), state, oneAssistantResult, root)

	cfg, err = workerruntime.LoadAssistantRuntimeConfig(configPath)
	if err != nil || len(cfg.Assistants) != 1 || cfg.Assistants[0].AssistantAddress != "app/assistant/support" {
		t.Fatalf("runtime config after adding first assistant = (%+v, %v)", cfg, err)
	}
	sourceRoot := filepath.Join(root, "assistants", "support")
	state.mu.Lock()
	gotDefs := len(state.defs)
	gotDigest := state.digests[sourceRoot]
	state.mu.Unlock()
	if gotDefs != 1 || gotDigest == "" {
		t.Fatalf("assistant watch state = (defs=%d, digest=%q), want (1, non-empty)", gotDefs, gotDigest)
	}

	// Removing the assistant must clear defs/digests and prevent scans from re-inserting removed roots.
	reconcileWorkerAssistants(context.Background(), state, emptyResult, root)
	scanWorkerAssistantSources(context.Background(), state, nil)
	state.mu.Lock()
	remainingDefs := len(state.defs)
	remainingDigests := len(state.digests)
	state.mu.Unlock()
	if remainingDefs != 0 || remainingDigests != 0 {
		t.Fatalf("after removal = (defs=%d, digests=%d), want (0, 0)", remainingDefs, remainingDigests)
	}
}
