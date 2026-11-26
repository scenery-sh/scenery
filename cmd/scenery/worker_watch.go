package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"scenery.sh/internal/devtelemetry"
)

type workerWatcher struct {
	directory, capabilities, token          string
	dev                                     *workerDevelopment
	devControl                              *workerDevControl
	app                                     *devtelemetry.App
	faults                                  map[*workerProcess]struct{}
	exits                                   chan struct{}
	client                                  *http.Client
	bundle                                  workerBundle
	active                                  map[string]*workerProcess
	all                                     []*workerProcess
	generation, sequence                    uint64
	retirements                             sync.WaitGroup
	generations                             workerGenerations
	lastConfigError                         string
	retireDrainTimeout, retireForceInterval time.Duration
}

func (watch *workerWatcher) run(ctx context.Context, hostDone <-chan error) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer func() {
		cancelRun()
		watch.retirements.Wait()
		stopWorkers(watch.all)
		if watch.dev != nil {
			pruneJSWorkersByPaths(watch.dev.watch.root, nil)
		}
	}()
	watch.generations.add(watch.generation, watch.active)
	watch.exits = make(chan struct{}, 1)
	watch.faults = make(map[*workerProcess]struct{})
	for _, process := range watch.active {
		watch.observeExit(runCtx, process)
	}
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	var reconciliation <-chan time.Time
	var configTicks <-chan time.Time
	var configPath string
	var lastConfigInfo os.FileInfo
	var stopConfigTicker func()
	defer func() {
		if stopConfigTicker != nil {
			stopConfigTicker()
		}
	}()
	checkConfig := func() {
		info, err := os.Stat(configPath)
		if err != nil {
			info = nil
		}
		if sameConfigFile(lastConfigInfo, info) {
			return
		}
		if watch.applyConfigurationChange(runCtx) {
			lastConfigInfo = info
		}
	}
	startConfigWatch := func() bool {
		if configTicks != nil || watch.dev == nil || watch.bundle.config == nil {
			return false
		}
		path, ok := workerDevConfigFile(watch.dev.watch.root, watch.dev.envName)
		if !ok {
			return false
		}
		configPath = path
		configTicker := time.NewTicker(devConfigPollInterval)
		stopConfigTicker = configTicker.Stop
		configTicks = configTicker.C
		return true
	}
	var seen workerInputs
	if watch.dev != nil {
		events, watchErrors = watch.dev.watch.fs.Events, watch.dev.watch.fs.Errors
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		reconciliation, seen = ticker.C, watch.dev.applied
		if startConfigWatch() {
			checkConfig()
		}
	}
	debounce := time.NewTimer(time.Hour)
	defer debounce.Stop()
	debounce.Stop()
	retry := time.NewTimer(time.Hour)
	defer retry.Stop()
	retry.Stop()
	pending := make(map[string]struct{})
	var detected time.Time
	var reconcile, retryCandidate bool
	schedule := func() {
		if detected.IsZero() {
			detected = time.Now()
		}
		debounce.Reset(workerEditDebounce)
	}
	for {
		select {
		case err := <-hostDone:
			return err
		case <-runCtx.Done():
			return <-hostDone
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if watch.dev.watch.eventRelevant(event.Name) {
				pending[event.Name] = struct{}{}
				schedule()
			}
		case _, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
			}
			reconcile = true
			schedule()
		case <-reconciliation:
			reconcile = true
			schedule()
		case <-configTicks:
			checkConfig()
		case <-debounce.C:
			current, err := watch.dev.watch.capture(seen, pending, reconcile, true)
			observed := detected
			clear(pending)
			reconcile, detected = false, time.Time{}
			if err != nil {
				fmt.Fprintf(os.Stderr, "application inputs could not be read: %v\n", err)
				continue
			}
			if maps.Equal(current, seen) && !retryCandidate {
				continue
			}
			seen, retryCandidate = current, false
			if applied, err := watch.refresh(runCtx, current, observed); err != nil {
				fmt.Fprintf(os.Stderr, "application update failed: %v\n", err)
				if errors.Is(err, errWorkerInputsChanged) {
					// The next candidate must include edits even if their
					// notifications were dropped or have not arrived yet.
					reconcile, retryCandidate = true, true
					schedule()
				}
			} else if applied != nil {
				seen = applied
				if startConfigWatch() || configTicks != nil {
					checkConfig()
				}
			}
		case <-watch.exits:
			watch.restartStopped(runCtx, retry)
		case <-retry.C:
			watch.restartStopped(runCtx, retry)
		}
	}
}

func (watch *workerWatcher) observeExit(ctx context.Context, process *workerProcess) {
	go func() {
		select {
		case <-ctx.Done():
		case <-process.done:
			select {
			case watch.exits <- struct{}{}:
			default:
			}
		}
	}()
}

func (watch *workerWatcher) restartStopped(ctx context.Context, retry *time.Timer) {
	if watch.noteStoppedWorkers() {
		if err := watch.replace(ctx, watch.bundle, nil); err != nil {
			fmt.Fprintf(os.Stderr, "worker restart failed: %v\n", err)
			retry.Reset(time.Second)
		} else if watch.devControl != nil {
			watch.devControl.publish(watch.dev.applied, watch.bundle, watch.generation, nil)
		}
	}
}

func (watch *workerWatcher) noteStoppedWorkers() bool {
	stopped := false
	for _, process := range watch.active {
		select {
		case <-process.done:
			stopped = true
			if _, reported := watch.faults[process]; watch.app != nil && !reported {
				watch.faults[process] = struct{}{}
				module, _, _ := strings.Cut(process.program.Service, "/service/")
				recordDevWorkerFault(watch.app, watch.generation, module, process.program.Runtime, process.cmd.ProcessState)
			}
		default:
		}
	}
	return stopped
}

var errWorkerInputsChanged = errors.New("application changed while workers were compiling")

func (dev *workerDevelopment) verify(expected workerInputs) error {
	actual, err := dev.watch.capture(nil, nil, true, false)
	if err != nil {
		return err
	}
	if !maps.Equal(actual, expected) {
		return errWorkerInputsChanged
	}
	return nil
}

type workerRebuildTiming struct {
	Generation     uint64   `json:"generation"`
	Declarations   bool     `json:"declarations"`
	Go             bool     `json:"go"`
	GoLinks        []string `json:"go_links,omitempty"`
	JS             []string `json:"js,omitempty"`
	Cargo          []string `json:"cargo,omitempty"`
	Rebuilt        []string `json:"rebuilt"`
	RebuiltModules []string `json:"rebuilt_modules"`
	SkippedModules []string `json:"skipped_modules"`
	Skipped        int      `json:"skipped"`
	DetectMS       float64  `json:"detect_ms"`
	ContractMS     float64  `json:"contract_ms"`
	GoMS           float64  `json:"go_ms"`
	JSMS           float64  `json:"js_ms"`
	CargoMS        float64  `json:"cargo_ms"`
	LinkMS         float64  `json:"-"`
	CompileMS      float64  `json:"compile_ms"`
	ReadyMS        float64  `json:"ready_ms"`
	TotalMS        float64  `json:"total_ms"`
}

func workerMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func (watch *workerWatcher) refresh(ctx context.Context, current workerInputs, detected time.Time) (applied workerInputs, returnErr error) {
	paths := changedWorkerInputs(watch.dev.applied, current)
	if len(paths) == 0 {
		return current, nil
	}
	started := time.Now()
	edit := watch.dev.classify(paths)
	prevDeps := watch.dev.jsDependencies()
	nextDeps := prevDeps
	appliedCurrent := current
	timing := workerRebuildTiming{
		Declarations: edit.declarations,
		DetectMS:     workerMilliseconds(started.Sub(detected)), Rebuilt: []string{},
		RebuiltModules: []string{}, SkippedModules: []string{},
	}
	defer func() {
		if returnErr != nil && !slices.Equal(prevDeps, nextDeps) {
			watch.dev.watch.setExplicit(prevDeps, false)
		}
		if errors.Is(returnErr, errWorkerInputsChanged) {
			return
		}
		timing.TotalMS = workerMilliseconds(time.Since(detected))
		timing.Generation = watch.generation
		if returnErr != nil {
			g := agentGraph{Result: watch.dev.compiled}
			modules, _ := g.modules()
			failure := describeDevBuildFailure(returnErr, watch.dev.watch.root, modules, paths)
			watch.devControl.failure(current, failure, &timing)
		} else {
			watch.devControl.publish(appliedCurrent, watch.bundle, watch.generation, &timing)
		}
		trigger := "body_only"
		if edit.declarations || edit.cargoGraph {
			trigger = "contract_or_config"
		}
		var code string
		if returnErr != nil {
			if diagnostic, ok := errors.AsType[*cliDiagnosticError](returnErr); ok {
				code = diagnostic.diagnostic.Code
			} else if timing.ReadyMS == 0 {
				code = workerCheckDiagnostic(watch.dev.compiled, returnErr).Code
			} else {
				code = cliErrorDiagnostic(returnErr).Code
			}
		}
		recordDevRebuild(watch.app, timing, trigger, code)
		if data, err := json.Marshal(timing); err == nil {
			fmt.Fprintf(os.Stderr, "worker rebuild %s\n", data)
		}
	}()
	candidate := *watch.dev
	candidate.js = slices.Clone(candidate.js)
	candidate.rust.artifacts = maps.Clone(candidate.rust.artifacts)
	bundle, err := candidate.rebuild(ctx, watch.bundle, current, paths, &timing)
	timing.CompileMS = workerMilliseconds(time.Since(started))
	if err != nil {
		return nil, err
	}
	nextDeps = candidate.jsDependencies()
	if !slices.Equal(prevDeps, nextDeps) {
		appliedCurrent, err = reconcileExplicitJSDependencies(watch.dev.watch, current, prevDeps, nextDeps)
		if err != nil {
			return nil, err
		}
	}
	dbSetup, dbChanged, err := candidate.checkPendingMigrations(ctx)
	if err != nil {
		return nil, err
	}
	for _, program := range bundle.Programs {
		module, _, _ := strings.Cut(program.Service, "/service/")
		if workerStillMatches(watch.active[program.Process], program) {
			timing.Skipped++
			timing.SkippedModules = append(timing.SkippedModules, module)
		} else {
			timing.Rebuilt = append(timing.Rebuilt, program.Service)
			timing.RebuiltModules = append(timing.RebuiltModules, module)
		}
	}
	slices.Sort(timing.Rebuilt)
	slices.Sort(timing.RebuiltModules)
	slices.Sort(timing.SkippedModules)
	readyStarted := time.Now()
	err = watch.replace(ctx, bundle, func() error {
		if err := candidate.verify(appliedCurrent); err != nil {
			return err
		}
		if dbChanged {
			return candidate.applyVerifiedDatabaseSetup(ctx, dbSetup)
		}
		return nil
	})
	timing.ReadyMS = workerMilliseconds(time.Since(readyStarted))
	if err != nil {
		return nil, err
	}
	if dbChanged {
		candidate.dbSetupFingerprint = dbSetup.Fingerprint
	}
	candidate.applied = appliedCurrent
	candidate.ensureAssistantCompiled().Store(candidate.compiled)
	if edit.declarations && candidate.assistantState != nil {
		state := candidate.assistantState
		nextDefs := assistantDefinitionsFromResult(candidate.compiled, candidate.watch.root)
		state.mu.Lock()
		prevDefs := slices.Clone(state.defs)
		var untracked []string
		for _, def := range nextDefs {
			if def.SourceRoot != "" {
				if _, ok := state.digests[def.SourceRoot]; !ok {
					untracked = append(untracked, def.SourceRoot)
				}
			}
		}
		state.mu.Unlock()
		if !slices.Equal(prevDefs, nextDefs) {
			preDigests := make(map[string]string, len(untracked))
			for _, source := range untracked {
				if digest, digestErr := workerAssistantSourceDigest(source); digestErr == nil {
					preDigests[source] = digest
				}
			}
			if err := state.supervisor.Reconcile(ctx, candidate.compiled); err != nil {
				fmt.Fprintf(os.Stderr, "assistant reconcile failed: %v\n", err)
			} else {
				if err := state.writeRuntimeConfig(); err != nil {
					fmt.Fprintf(os.Stderr, "assistant runtime config update failed: %v\n", err)
				}
				activeRoots := make(map[string]bool, len(nextDefs))
				for _, def := range nextDefs {
					if def.SourceRoot != "" {
						activeRoots[def.SourceRoot] = true
					}
				}
				state.mu.Lock()
				state.defs = nextDefs
				for source := range state.digests {
					if !activeRoots[source] {
						delete(state.digests, source)
					}
				}
				for source, digest := range preDigests {
					if _, exists := state.digests[source]; !exists {
						state.digests[source] = digest
					}
				}
				state.mu.Unlock()
			}
		}
	}
	*watch.dev = candidate
	return appliedCurrent, nil
}

func (watch *workerWatcher) applyConfigurationChange(ctx context.Context) bool {
	if watch.dev == nil || watch.bundle.config == nil {
		return false
	}
	reportError := func(prefix string, err error) bool {
		msg := prefix + ": " + err.Error()
		if watch.lastConfigError != msg {
			watch.lastConfigError = msg
			fmt.Fprintln(os.Stderr, msg)
		}
		return false
	}
	_, cfg, err := discoverConfiguredApp(watch.dev.watch.root)
	if err != nil {
		return reportError("configuration reload failed", err)
	}
	env, err := cfg.ResolveEnv(watch.dev.envName)
	if err != nil {
		return reportError("configuration reload failed", err)
	}
	store, err := devConfigStore(cfg)
	if err != nil {
		return reportError("configuration reload failed", err)
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return reportError("configuration reload failed", err)
	}
	if document.Revision == watch.bundle.config.Revision {
		watch.lastConfigError = ""
		return true
	}
	nextBundle := watch.bundle
	nextBundle.Programs = slices.Clone(watch.bundle.Programs)
	if err := nextBundle.resolveDevelopmentConfig(ctx, watch.dev.compiled, cfg, watch.dev.envName); err != nil {
		return reportError("configuration reload failed", err)
	}
	nextBundle.setWorkerConfig()
	if watch.bundle.config != nil && nextBundle.config != nil && nextBundle.config.Revision == watch.bundle.config.Revision {
		watch.lastConfigError = ""
		return true
	}
	if err := watch.replace(ctx, nextBundle, nil); err != nil {
		return reportError("configuration apply failed", err)
	}
	watch.lastConfigError = ""
	if watch.devControl != nil {
		watch.devControl.publish(watch.dev.applied, watch.bundle, watch.generation, nil)
	}
	return true
}

func (watch *workerWatcher) replace(ctx context.Context, bundle workerBundle, verify func() error) error {
	if ctx.Err() == nil {
		watch.noteStoppedWorkers()
	}
	next := make(map[string]*workerProcess, len(bundle.Programs))
	watch.sequence++
	started := make([]*workerProcess, len(bundle.Programs))
	errs := make([]error, len(bundle.Programs))
	var group sync.WaitGroup
	for index, program := range bundle.Programs {
		previous := watch.active[program.Process]
		if workerStillMatches(previous, program) {
			next[program.Process] = previous
			continue
		}
		id := fmt.Sprintf("%x-%x", watch.sequence, index)
		group.Go(func() {
			started[index], errs[index] = startBundleWorker(ctx, watch.directory, watch.capabilities, id, program)
		})
	}
	group.Wait()
	started = slices.DeleteFunc(started, func(process *workerProcess) bool { return process == nil })
	if err := errors.Join(errs...); err != nil {
		stopWorkers(started)
		return err
	}
	if verify != nil {
		if err := verify(); err != nil {
			stopWorkers(started)
			return err
		}
	}
	if len(started) == 0 && len(next) == len(watch.active) && workerBundleDigest(bundle) == workerBundleDigest(watch.bundle) {
		watch.bundle = bundle
		return nil
	}
	for _, process := range started {
		next[process.program.Process] = process
	}
	watch.all = append(watch.all, started...)
	if err := publishWorkerGeneration(ctx, watch.client, watch.token, bundle, next, watch.sequence); err != nil {
		current, statusErr := watch.currentGeneration(ctx)
		if statusErr != nil || current != watch.sequence {
			if statusErr == nil && current < watch.sequence {
				stopWorkers(started)
			}
			return errors.Join(err, statusErr)
		}
	}
	watch.all = slices.DeleteFunc(watch.all, func(process *workerProcess) bool {
		if next[process.program.Process] == process {
			return false
		}
		select {
		case <-process.done:
			return true
		default:
			return false
		}
	})
	for name, process := range watch.active {
		if next[name] != process {
			delete(watch.faults, process)
		}
	}
	watch.generations.add(watch.sequence, next)
	if watch.dev != nil {
		pruneJSWorkersByPaths(watch.dev.watch.root, watch.generations.binaries())
	}
	previous := watch.generation
	watch.generation, watch.active, watch.bundle = watch.sequence, next, bundle
	for _, process := range started {
		watch.observeExit(ctx, process)
	}
	watch.retirements.Add(1)
	go func() {
		defer watch.retirements.Done()
		watch.retire(ctx, previous)
	}()
	fmt.Fprintf(os.Stderr, "application generation %d ready (%d worker replacements)\n", watch.generation, len(started))
	return nil
}

func reconcileExplicitJSDependencies(watch *workerInputWatch, current workerInputs, prevDeps, nextDeps []string) (workerInputs, error) {
	updated := maps.Clone(current)
	watch.setExplicit(nextDeps, true)
	for _, dep := range prevDeps {
		if !slices.Contains(nextDeps, dep) && watch.skippedByWalk(dep) {
			delete(updated, dep)
		}
	}
	for _, dep := range nextDeps {
		if !slices.Contains(prevDeps, dep) {
			if _, exists := updated[dep]; !exists {
				if err := watch.scanExplicitFile(dep, updated, false); err != nil {
					return nil, err
				}
			}
		}
	}
	return updated, nil
}

func workerStillMatches(process *workerProcess, program workerProgram) bool {
	if process == nil || process.program.Digest != program.Digest || process.program.Identity != program.Identity || process.program.Runtime != program.Runtime {
		return false
	}
	if workerConfigIdentity(process.program.config) != workerConfigIdentity(program.config) {
		return false
	}
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

func (watch *workerWatcher) currentGeneration(ctx context.Context) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host/__scenery/process/v1/generations", nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+watch.token)
	response, err := watch.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	var status struct{ Current uint64 }
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("host did not confirm the generation")
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&status)
	return status.Current, err
}

func workerControlRetireGeneration(ctx context.Context, client *http.Client, token, path string) error {
	err := workerControlRequest(ctx, client, token, http.MethodDelete, path, nil, http.StatusNoContent)
	if err == nil || strings.HasPrefix(err.Error(), "worker Host HTTP 404:") {
		return nil
	}
	return err
}

func (watch *workerWatcher) retire(ctx context.Context, generation uint64) {
	drainTimeout := watch.retireDrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 5 * time.Second
	}
	forceInterval := watch.retireForceInterval
	if forceInterval <= 0 {
		forceInterval = 200 * time.Millisecond
	}
	watch.retireWith(ctx, generation, drainTimeout, forceInterval)
}

func (watch *workerWatcher) retireWith(ctx context.Context, generation uint64, drainTimeout, forceRetryInterval time.Duration) {
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	drain, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	ticker := time.NewTicker(min(50*time.Millisecond, max(time.Millisecond, drainTimeout/2)))
	defer ticker.Stop()
	for {
		if err := workerControlRetireGeneration(drain, watch.client, watch.token, path); err == nil {
			stopWorkers(watch.generations.remove(generation))
			return
		}
		select {
		case <-drain.Done():
			if ctx.Err() != nil {
				return
			}
			forceTicker := time.NewTicker(forceRetryInterval)
			defer forceTicker.Stop()
			for {
				forceCtx, cancelForce := context.WithTimeout(ctx, time.Second)
				err := workerControlRetireGeneration(forceCtx, watch.client, watch.token, path+"?force=true")
				cancelForce()
				if err == nil {
					stopWorkers(watch.generations.remove(generation))
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-forceTicker.C:
				}
			}
		case <-ticker.C:
		}
	}
}

// workerGenerations owns the references to processes in published generations,
// including generations still draining on the Host.
type workerGenerations struct {
	mu        sync.Mutex
	instances map[uint64]map[string]*workerProcess
	refs      map[*workerProcess]int
}

func (g *workerGenerations) add(number uint64, processes map[string]*workerProcess) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.instances == nil {
		g.instances = make(map[uint64]map[string]*workerProcess)
		g.refs = make(map[*workerProcess]int)
	}
	g.instances[number] = processes
	for _, process := range processes {
		g.refs[process]++
	}
}

func (g *workerGenerations) remove(number uint64) []*workerProcess {
	g.mu.Lock()
	defer g.mu.Unlock()
	var stopped []*workerProcess
	for _, process := range g.instances[number] {
		g.refs[process]--
		if g.refs[process] == 0 {
			delete(g.refs, process)
			stopped = append(stopped, process)
		}
	}
	delete(g.instances, number)
	return stopped
}

func (g *workerGenerations) binaries() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	binaries := make([]string, 0, len(g.refs))
	for process := range g.refs {
		if process.program.Binary != "" {
			binaries = append(binaries, process.program.Binary)
		}
	}
	return binaries
}

func stopWorkers(processes []*workerProcess) {
	for _, process := range processes {
		process.stop()
	}
}
