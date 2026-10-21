package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
	"scenery.sh/internal/rotatinglog"
)

var (
	watchPollInterval       = 250 * time.Millisecond
	watchBackupPollInterval = 2 * time.Second
	// watchSettleDelay is how long the tree must stay quiet before a change is
	// built. It only has to cover the burst of one save: a change that arrives
	// later starts its own build, and no generation activates unless its sources
	// are still the captured ones.
	watchSettleDelay = 50 * time.Millisecond
	// watchScanLead coalesces the events of one save before the scan that
	// runs inside the settle window starts.
	watchScanLead = 10 * time.Millisecond
)

// productionFrontendWatch registers the source dirs of serve-mode
// "production" frontends so the watcher tracks their files for static
// rebuilds instead of ignoring non-Go extensions. It is set once per dev
// process before the initial scan; every other scanWatchedFiles caller sees
// an empty registry and keeps today's Go-only watch set.
var productionFrontendWatch struct {
	sync.RWMutex
	root string
	dirs map[string]string // slash-relative frontend dir -> frontend name
}

const (
	assistantWatchHelperOnly = "helper-only"
	assistantWatchDependency = "dependency-helper"
	assistantWatchApp        = "app"
)

// assistantImplementationWatch is populated before the initial file scan and
// refreshed after graph changes. It is independent from the provider adapter: watch
// decisions are based on authored implementation paths only.
var assistantImplementationWatch struct {
	sync.RWMutex
	root  string
	roots map[string]string // slash-relative source root -> assistant address
}

func setAssistantImplementationWatch(root string, definitions []assistantDefinition) {
	roots := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		rel, err := filepath.Rel(root, definition.SourceRoot)
		if err != nil || rel == "." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			continue
		}
		roots[filepath.ToSlash(rel)] = definition.Address
	}
	assistantImplementationWatch.Lock()
	assistantImplementationWatch.root = filepath.Clean(root)
	assistantImplementationWatch.roots = roots
	assistantImplementationWatch.Unlock()
}

func assistantWatchAddressForPath(root, rel string) (string, bool) {
	assistantImplementationWatch.RLock()
	defer assistantImplementationWatch.RUnlock()
	if filepath.Clean(root) != assistantImplementationWatch.root || len(assistantImplementationWatch.roots) == 0 {
		return "", false
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	for sourceRoot, address := range assistantImplementationWatch.roots {
		if rel == sourceRoot || strings.HasPrefix(rel, sourceRoot+"/") {
			return address, true
		}
	}
	return "", false
}

// classifyAssistantWatchPath returns the independent rebuild lane for an
// authored assistant file. Go files remain ordinary app rebuild inputs even
// when they happen to be beneath an assistant source directory.
func classifyAssistantWatchPath(root, rel string) string {
	address, ok := assistantWatchAddressForPath(root, rel)
	if !ok || address == "" {
		return ""
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	assistantRoot := ""
	assistantImplementationWatch.RLock()
	for sourceRoot, candidate := range assistantImplementationWatch.roots {
		if candidate == address && (rel == sourceRoot || strings.HasPrefix(rel, sourceRoot+"/")) {
			assistantRoot = sourceRoot
			break
		}
	}
	assistantImplementationWatch.RUnlock()
	if assistantRoot == "" {
		return ""
	}
	inside := strings.TrimPrefix(strings.TrimPrefix(rel, assistantRoot), "/")
	// The runtime MCP manifest is graph-derived and consumed by the app-owned
	// gateway. A source edit here therefore takes the app lane so the child
	// gateway is rebuilt with the new manifest; no live gateway refresh API
	// exists yet. Generated provider channels/connections remain helper-only.
	if inside == ".scenery/runtime-manifest.json" {
		return assistantWatchApp
	}
	if strings.HasSuffix(inside, ".go") {
		return assistantWatchApp
	}
	base := filepath.Base(inside)
	if base == "package.json" || base == "package-lock.json" {
		return assistantWatchDependency
	}
	parts := strings.Split(inside, "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "instructions", "skills", "tools":
			return assistantWatchHelperOnly
		}
	}
	// The remainder of a provider source tree is helper implementation code.
	return assistantWatchHelperOnly
}

func splitAssistantWatchPaths(root string, paths []string) (assistantPaths, appPaths []string) {
	for _, path := range paths {
		lane := classifyAssistantWatchPath(root, path)
		if lane == "" || lane == assistantWatchApp {
			appPaths = append(appPaths, path)
			continue
		}
		assistantPaths = append(assistantPaths, path)
	}
	return assistantPaths, appPaths
}

func setProductionFrontendWatch(root string, cfg app.Config) {
	dirs := map[string]string{}
	for name, frontend := range cfg.Frontends {
		if strings.ToLower(strings.TrimSpace(frontend.Serve)) != frontendServeProduction {
			continue
		}
		label := localagentLabel(name)
		if label == "" {
			continue
		}
		absRoot := managedFrontendRoot(root, localproxy.FrontendConfig{Name: name, Root: frontend.Root})
		rel, err := filepath.Rel(root, absRoot)
		if err != nil || rel == "." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			continue
		}
		dirs[filepath.ToSlash(rel)] = label
	}
	productionFrontendWatch.Lock()
	productionFrontendWatch.root = filepath.Clean(root)
	productionFrontendWatch.dirs = dirs
	productionFrontendWatch.Unlock()
}

// productionFrontendForWatchPath returns the production frontend owning a
// watched rel path. Files under the frontend's dist/ build output are
// excluded so rebuild artifacts never retrigger the watcher.
func productionFrontendForWatchPath(root, rel string) (string, bool) {
	productionFrontendWatch.RLock()
	defer productionFrontendWatch.RUnlock()
	if len(productionFrontendWatch.dirs) == 0 || filepath.Clean(root) != productionFrontendWatch.root {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	for dir, name := range productionFrontendWatch.dirs {
		if rel != dir && !strings.HasPrefix(rel, dir+"/") {
			continue
		}
		if rel == dir+"/dist" || strings.HasPrefix(rel, dir+"/dist/") {
			return "", false
		}
		return name, true
	}
	return "", false
}

func isProductionFrontendOutputDir(root, rel string) bool {
	productionFrontendWatch.RLock()
	defer productionFrontendWatch.RUnlock()
	if len(productionFrontendWatch.dirs) == 0 || filepath.Clean(root) != productionFrontendWatch.root {
		return false
	}
	rel = filepath.ToSlash(rel)
	for dir := range productionFrontendWatch.dirs {
		if rel == dir+"/dist" {
			return true
		}
	}
	return false
}

// splitProductionFrontendPaths partitions changed paths into production
// frontends to rebuild and app paths for the ordinary Go rebuild. Paths the
// core watcher already owns (Go, .scn, config) stay app paths even inside a
// frontend root.
func splitProductionFrontendPaths(root string, paths []string) ([]string, []string) {
	var names, appPaths []string
	seen := map[string]bool{}
	for _, rel := range paths {
		// A Go test file reaches this list only if runtime code embeds it.
		if !isWatchedFile(rel) && filepath.Ext(rel) != ".go" {
			if name, ok := productionFrontendForWatchPath(root, rel); ok {
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
				continue
			}
		}
		appPaths = append(appPaths, rel)
	}
	sort.Strings(names)
	return names, appPaths
}

type fileStamp struct {
	modTime    time.Time
	changeTime int64
	device     uint64
	inode      uint64
	size       int64
	mode       uint32
	hash       string
	embed      bool
	data       []byte
}

// Metadata decides whether to rehash; content decides whether to rebuild.
func (stamp fileStamp) sameContent(other fileStamp) bool {
	return stamp.hash == other.hash && stamp.size == other.size &&
		stamp.mode == other.mode && stamp.embed == other.embed
}

type fileSnapshot struct {
	// contract is the accepted graph, and contractFiles, contractCompiler and
	// contractCompilerAbsent are exactly the bytes it was compiled from. Only
	// that pairing authorizes reusing the graph instead of compiling again.
	contract               *compiler.Result
	contractFiles          map[string]fileStamp
	contractCompiler       map[string]fileStamp
	contractCompilerAbsent map[string]bool
	// membership is a provisional graph a declaration edit discovered. It only
	// widens which files a scan captures and never describes accepted bytes.
	membership *compiler.Result
	// scanStartedAt and capturedAt bound the scan that captured the snapshot.
	scanStartedAt    time.Time
	capturedAt       time.Time
	scanStats        watchScanStats
	files            map[string]fileStamp
	compilerFiles    map[string]fileStamp
	compilerImpl     map[string]bool
	compilerAbsent   map[string]bool
	compilerValid    bool
	dirs             []string
	generated        map[string]bool
	generatedContent map[string]fileStamp
	retryGenerated   bool
}

type devBackend struct {
	Network string
	Addr    string
}

func (b devBackend) normalized() devBackend {
	if strings.TrimSpace(b.Network) == "" {
		b.Network = "tcp"
	}
	return b
}

func runWithWatch(listen devListenRequest, verbose, jsonMode, desktop bool, appRoot, envName string, onReady func()) (runErr error) {
	startup, err := openDetachedDevStartupReporter()
	if err != nil {
		return err
	}
	defer func() {
		runErr = startup.Report(runErr)
		_ = startup.Close()
	}()
	applyWatchTimingOverridesFromEnv()
	readyReported := false
	reportReady := func() {
		if readyReported {
			return
		}
		readyReported = true
		_ = startup.Close()
		if onReady != nil {
			onReady()
		}
	}

	start, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	if err := checkStorageStartup(context.Background(), root, cfg); err != nil {
		return err
	}
	resolvedEnv, err := cfg.ResolveEnv(envName)
	if err != nil {
		return &codedCLIError{err: err, code: 3}
	}
	cfg.Frontends = resolvedEnv.Frontends
	if desktop {
		if _, err := configuredDesktopShells(root, cfg); err != nil {
			return err
		}
	}
	setProductionFrontendWatch(root, cfg)
	uiCatalogDir, uiCatalogMissing, err := resolvedEnv.UICatalogDir(root)
	if err != nil {
		return err
	}
	if uiCatalogMissing {
		fmt.Fprintf(os.Stderr, "scenery: envs.%s.ui_catalog %q not found; using the embedded UI catalog\n", resolvedEnv.Name, resolvedEnv.UICatalog)
	}

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		stopSignals()
		cancel()
	}()
	go func() {
		select {
		case <-sigCtx.Done():
			stopSignals()
			cancel()
		case <-ctx.Done():
		}
	}()
	stopParentMonitor := func() {}
	if !detachedDevChildMode() {
		stopParentMonitor = startParentMonitor(ctx, cancel)
	}
	defer stopParentMonitor()

	var logWriter *rotatinglog.Writer
	var output, errorOutput io.Writer = os.Stdout, os.Stderr
	if detachedDevChildMode() {
		logWriter, err = rotatinglog.Open(envpolicy.Get(detachedDevChildEnv))
		if err != nil {
			return err
		}
		defer func() { _ = logWriter.Close() }()
		output, errorOutput = logWriter, logWriter
	}
	console := newRunConsole(output, errorOutput, verbose, jsonMode, cfg.AppID(), root)
	defer func() {
		if _, handoff := errors.AsType[*frameworkHandoff](runErr); handoff {
			console.Finish(runErr)
			return
		}
		runErr = preserveCLIDiagnostic(runErr)
		console.Finish(runErr)
		if jsonMode && runErr != nil {
			runErr = &silentCLIError{err: runErr, code: cliExitCode(runErr)}
		}
	}()

	ctx = context.WithValue(ctx, workspaceWaitObserverKey{}, func(event, cause string) {
		console.Event(event, map[string]any{"reason": "workspace_transaction_active", "cause": cause})
	})
	if err := waitForWorkspaceRead(ctx, root); err != nil {
		return err
	}
	preparedSession, err := prepareDevAgentSessionDetailed(ctx, root, cfg, resolvedEnv, listen, console)
	if err != nil {
		if preparedSession != nil && preparedSession.Cleanup != nil {
			preparedSession.Cleanup()
		}
		var already *devSessionAlreadyRunningError
		if errors.As(err, &already) {
			if detachedDevChildMode() {
				return &codedCLIError{code: 3, err: already}
			}
			console.AlreadyRunning(already.ownerPID, already.session.Status, detachedDevRunURLs(already.session),
				fmt.Sprintf("scenery logs --follow --app-root %q", root),
				fmt.Sprintf("scenery down --app-root %q", root))
			if jsonMode {
				return nil
			}
			return followAlreadyRunningDevSession(ctx, console, root)
		}
		return err
	}
	agentClient := preparedSession.Client
	agentSession := preparedSession.Session
	backend := preparedSession.Backend
	restoreAgentEnv := preparedSession.Cleanup
	if restoreAgentEnv == nil {
		restoreAgentEnv = func() {}
	}
	defer restoreAgentEnv()
	if !detachedDevChildMode() {
		foregroundLogPath := detachedDevLogPath(preparedSession.Paths, root, time.Now())
		if foregroundLog, openErr := rotatinglog.Open(foregroundLogPath); openErr == nil {
			defer func() {
				console.SetEventLog(nil)
				_ = foregroundLog.Close()
			}()
			console.SetEventLog(foregroundLog)
			if err := rotatinglog.Prune(foregroundLogPath); err != nil {
				console.Event("log.retention_error", map[string]any{"error": err.Error()})
			}
		}
	} else if logWriter != nil {
		if err := rotatinglog.Prune(envpolicy.Get(detachedDevChildEnv)); err != nil {
			console.Event("log.retention_error", map[string]any{"error": err.Error()})
		}
	}

	snapshot, err := preparedSession.Owner.startupScan.wait()
	preparedSession.Owner.startupScan = nil
	if err != nil {
		return err
	}

	supervisor, err := newDevSupervisor(ctx, root, cfg, resolvedEnv, backend, console, agentClient, agentSession)
	if err != nil {
		return err
	}
	preparedSession.Owner.dashboard.bindServingSupervisor(supervisor)
	supervisor.devDomainURL = preparedSession.DomainURL
	supervisor.invocationEnvironment = preparedSession.Environment
	supervisor.worktreeControlPaths = &preparedSession.Paths
	supervisor.worktreeRootPaths = &preparedSession.Owner.paths
	supervisor.adoptManagedFrontends(preparedSession.FrontendProcesses)
	defer func() { _ = supervisor.Close() }()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}
	if desktop {
		supervisor.addStartupReady(supervisor.startDesktopShellsAfterFrontends(ctx, preparedSession.FrontendReady))
	} else {
		supervisor.addStartupReady(preparedSession.FrontendReady)
	}
	// The control service belongs to this supervisor; never repair or replace a
	// machine agent if it fails. Owner failure terminates this worktree only.
	ownerFailure := make(chan error, 1)
	defer func() {
		select {
		case err := <-ownerFailure:
			runErr = errors.Join(runErr, err)
		default:
		}
	}()
	go func() {
		select {
		case ownerErr := <-preparedSession.Owner.failure:
			if ownerErr == nil {
				ownerErr = fmt.Errorf("worktree control stopped unexpectedly")
			}
			ownerFailure <- ownerErr
			cancel()
		case <-ctx.Done():
		}
	}()
	if uiCatalogDir != "" {
		if !console.json {
			console.printSetupDone("ui catalog dev mode: " + uiCatalogDir)
		}
		startUICatalogDevSync(ctx, console, supervisor, root, uiCatalogDir, resolvedEnv)
	}

	ctx = context.WithValue(ctx, watchSourceOwnerKey{}, true)
	if err := rebuildAfterTransaction(ctx, supervisor, true, &snapshot); err != nil {
		snapshot.retryGenerated = true
		if errors.Is(err, errBuildSuperseded) {
			supervisor.console.RebuildFailed(err)
		} else {
			err = preserveCLIDiagnostic(err)
			err = startup.Report(err)
			supervisor.console.InitialBuildFailed(err, supervisor.runURLs())
			// Detached children fail fast so the waiting parent reports the build
			// error instead of hanging until its readiness timeout. Interactive
			// runs keep watching, as InitialBuildFailed just promised.
			if detachedDevChildMode() {
				return err
			}
		}
	} else {
		if err := acceptGeneratedSnapshot(root, &snapshot); err != nil {
			return err
		}
		reportReady()
	}

	watcher, err := newFileChangeWatcher(root, snapshot)
	if err != nil {
		if verbose {
			supervisor.console.printf(supervisor.console.err, "  %s\n\n", err.Error())
		}
	}
	if watcher != nil {
		defer func() { _ = watcher.Close() }()
	}

	go supervisor.watchConfiguration(ctx)
	var failedHandoff build.DesiredFramework
	for {
		nextSnapshot, forced, err := waitForStableChange(ctx, root, snapshot, watcher, supervisor.rebuildRequestChan())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			var lost *watchSourceLostError
			if errors.As(err, &lost) {
				supervisor.console.Event("source.lost", map[string]any{"reason": "source_missing", "cause": lost.Error(), "retained_data": true})
			}
			return err
		}
		paths := changedPaths(snapshot, nextSnapshot)
		snapshot = nextSnapshot
		frontendNames, appPaths := splitProductionFrontendPaths(root, paths)
		if len(frontendNames) > 0 {
			supervisor.RebuildProductionFrontends(ctx, frontendNames)
		}
		assistantPaths, appPaths := splitAssistantWatchPaths(root, appPaths)
		if len(assistantPaths) > 0 && supervisor.assistants != nil {
			supervisor.assistants.HandleChanges(ctx, assistantPaths)
		}
		if len(appPaths) == 0 && !forced {
			continue
		}
		// Checked before the config reload: a newly selected framework may use
		// a configuration language this producer cannot decode.
		if handoff := supervisor.frameworkHandoffBeforeBuild(ctx, &failedHandoff); handoff != nil {
			return handoff
		}
		// A build blocked by a cause no ordinary edit resolves is not
		// repeated until the inputs that can resolve it change; an explicit
		// rebuild request still builds.
		if !forced {
			if block, prevented := supervisor.preventBlockedBuild(snapshot); prevented {
				supervisor.console.BuildBlocked(block)
				_ = supervisor.persistStatus(ctx)
				continue
			}
		}
		supervisor.announceRebuild(appPaths)
		if err := rebuildAfterTransaction(context.WithValue(ctx, devEditClassKey{}, classifyEditPaths(appPaths)), supervisor, false, &snapshot); err != nil {
			snapshot.retryGenerated = true
			supervisor.console.RebuildFailed(err)
		} else {
			if err := acceptGeneratedSnapshot(root, &snapshot); err != nil {
				return err
			}
			reportReady()
		}
	}
}

