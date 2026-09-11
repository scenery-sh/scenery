package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/fsnotify/fsnotify"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
	"scenery.sh/internal/watchignore"
)

var (
	watchPollInterval       = 250 * time.Millisecond
	watchBackupPollInterval = 2 * time.Second
	watchSettleDelay        = 100 * time.Millisecond
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
	modTime time.Time
	size    int64
	mode    uint32
	hash    string
	embed   bool
}

// Metadata decides whether to rehash; content decides whether to rebuild.
func (stamp fileStamp) sameContent(other fileStamp) bool {
	return stamp.hash == other.hash && stamp.size == other.size &&
		stamp.mode == other.mode && stamp.embed == other.embed
}

type fileSnapshot struct {
	files            map[string]fileStamp
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

	console := newRunConsole(os.Stdout, os.Stderr, verbose, jsonMode, cfg.AppID(), root)
	defer func() {
		runErr = preserveCLIDiagnostic(runErr)
		console.Finish(runErr)
		if jsonMode && runErr != nil {
			runErr = &silentCLIError{err: runErr, code: cliExitCode(runErr)}
		}
	}()

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

	var snapshot fileSnapshot
	if err := console.Phase("Scanning source files", func() error {
		var err error
		snapshot, err = scanInitialWatchedFiles(root, compiler.Compile)
		return err
	}); err != nil {
		return err
	}

	supervisor, err := newDevSupervisor(ctx, root, cfg, resolvedEnv, backend, console, agentClient, agentSession)
	if err != nil {
		return err
	}
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

	if err := supervisor.RebuildAndRestart(ctx, true, snapshot); err != nil {
		snapshot.retryGenerated = true
		err = preserveCLIDiagnostic(err)
		err = startup.Report(err)
		supervisor.console.InitialBuildFailed(err, supervisor.runURLs())
		// Detached children fail fast so the waiting parent reports the build
		// error instead of hanging until its readiness timeout. Interactive
		// runs keep watching, as InitialBuildFailed just promised.
		if detachedDevChildMode() {
			return err
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

	for {
		nextSnapshot, forced, err := waitForStableChange(ctx, root, snapshot, watcher, supervisor.rebuildRequestChan())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
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
		supervisor.announceRebuild(appPaths)
		if err := supervisor.RebuildAndRestart(ctx, false, snapshot); err != nil {
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

func applyWatchTimingOverridesFromEnv() {
	watchPollInterval = watchDurationFromEnv("SCENERY_TEST_WATCH_POLL_MS", watchPollInterval)
	watchBackupPollInterval = watchDurationFromEnv("SCENERY_TEST_WATCH_BACKUP_POLL_MS", watchBackupPollInterval)
	watchSettleDelay = watchDurationFromEnv("SCENERY_TEST_WATCH_SETTLE_DELAY_MS", watchSettleDelay)
}

func watchDurationFromEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(envpolicy.Get(name))
	if value == "" {
		return fallback
	}
	millis, err := strconv.Atoi(value)
	if err != nil || millis <= 0 {
		return fallback
	}
	return time.Duration(millis) * time.Millisecond
}

func routeNamespaceForConfig(cfg app.Config) localagent.RouteNamespace {
	return localagent.RouteNamespace{
		Workspace:  sanitizeRouteLabel(cfg.AppID()),
		BaseDomain: localagent.DefaultRouteBaseDomain,
	}
}

func normalizeRouteNamespaceHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		value = value[scheme+3:]
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

func sanitizeRouteLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if r == '-' || r == '_' || r == '/' || r == '.' || unicode.IsSpace(r) {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// devSessionAlreadyRunningError reports a live duplicate dev runtime for the
// same app root. `scenery up` entry points treat it as an idempotent success
// and report the existing runtime instead of failing.
type devSessionAlreadyRunningError struct {
	root     string
	ownerPID int
	session  localagent.Session
}

func (e *devSessionAlreadyRunningError) Error() string {
	return fmt.Sprintf("scenery up is already running for app root %s under owner PID %d; stop it with `scenery down --app-root %q`, or use a separate Git worktree for another live code copy", e.root, e.ownerPID, e.root)
}

func rejectLiveDuplicateDevSession(root string, existing []localagent.Session) error {
	if session, pid := findLiveDuplicateDevSession(root, existing); session != nil {
		return &devSessionAlreadyRunningError{root: root, ownerPID: pid, session: *session}
	}
	return nil
}

var devSessionOwnerExitPollInterval = 2 * time.Second

// followAlreadyRunningDevSession attaches a duplicate foreground `scenery up`
// to the live runtime's structured logs, like `docker compose up` against
// running services. Interrupt detaches this follower only; stopping the
// runtime stays explicit through `scenery down`. The follower also exits when
// the owning runtime goes away.
func followAlreadyRunningDevSession(ctx context.Context, console *runConsole, root string) error {
	return followAlreadyRunningDevSessionWith(ctx, console, root, runSceneryLogsFunc, devSessionOwnerGone)
}

func followAlreadyRunningDevSessionWith(
	ctx context.Context,
	console *runConsole,
	root string,
	runLogs func(context.Context, io.Writer, []string) error,
	ownerGone func(context.Context, string) bool,
) error {
	console.printf(console.out, "  %s\n\n", console.palette.Dim("Following the running runtime's logs. Ctrl+C detaches without stopping it."))
	followCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ownerExited := make(chan struct{})
	// watcherDone lets this function outlive nothing: the owner watch reads
	// process state, so it must be stopped and joined before returning rather
	// than left running past the call.
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		if ownerGone(followCtx, root) {
			close(ownerExited)
			cancel()
		}
	}()
	err := runLogs(followCtx, os.Stdout, []string{"--follow", "--app-root", root})
	cancel()
	<-watcherDone
	select {
	case <-ownerExited:
		console.printf(console.out, "\n  %s\n", console.palette.Dim("The running dev runtime stopped; detaching."))
		return nil
	default:
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return fmt.Errorf("scenery up attached to the running runtime but could not follow its logs: %w; retry with `scenery logs --follow --app-root %q`", err, root)
}

// devSessionOwnerGone reports true once the app root no longer has a live
// verified owner, and false when ctx ends first. Transient agent errors keep
// the watch alive instead of misreporting the runtime as stopped.
func devSessionOwnerGone(ctx context.Context, root string) bool {
	return devSessionOwnerGoneWithInterval(ctx, root, devSessionOwnerExitPollInterval)
}

func devSessionOwnerGoneWithInterval(ctx context.Context, root string, interval time.Duration) bool {
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return false
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			held, err := paths.ProbeLiveLock()
			if err != nil {
				continue
			}
			if !held {
				return true
			}
			client, err := commandWorktreeClient(ctx, root)
			if err != nil {
				continue
			}
			sessions, err := client.List(ctx, root)
			if err != nil {
				continue
			}
			if session, _ := findLiveDuplicateDevSession(root, sessions); session == nil {
				return true
			}
		}
	}
}

func findLiveDuplicateDevSession(root string, existing []localagent.Session) (*localagent.Session, int) {
	for i := range existing {
		if cleanAbsPath(existing[i].AppRoot) != cleanAbsPath(root) {
			continue
		}
		if pid, live := sessionOwnerProcessLive(existing[i]); live {
			return &existing[i], pid
		}
	}
	return nil, 0
}

func discoverDevGitBranch(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// waitForStableChange blocks until watched files settle on a new state or a
// wake arrives on the rebuild-request channel. The bool result is true for a
// wake: the caller must rebuild even when no watched file changed, because the
// requester fixed build inputs the watcher cannot see.

func scanWatchedFiles(root string) (fileSnapshot, error) {
	return scanWatchedFilesReusing(root, fileSnapshot{})
}

// scanWatchedFilesReusing rescans the tree while reusing content hashes from
// the previous snapshot for files whose size, permissions, and mtime are
// unchanged, so steady-state watch ticks stat files instead of re-reading and
// re-hashing the whole workspace.
func scanWatchedFilesReusing(root string, previous fileSnapshot) (fileSnapshot, error) {
	snapshot := fileSnapshot{files: make(map[string]fileStamp, len(previous.files))}
	generated, err := compiler.GeneratedPaths(root)
	if err != nil {
		return fileSnapshot{}, err
	}
	snapshot.generated = make(map[string]bool, len(generated))
	snapshot.generatedContent = make(map[string]fileStamp, len(generated))
	snapshot.retryGenerated = previous.retryGenerated
	for rel := range generated {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		snapshot.generated[rel] = err == nil && info.Mode().IsRegular()
		if snapshot.generated[rel] {
			stamp, reused := reusableStamp(previous.generatedContent, rel, info, false)
			if !reused {
				stamp, _, err = stampWatchedFile(filepath.Join(root, filepath.FromSlash(rel)), info, false)
				if err != nil {
					continue
				}
			}
			snapshot.generatedContent[rel] = stamp
		}
	}
	var dirs []string
	ignore := watchignore.New(root)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Tolerate entries vanishing or turning unreadable mid-scan; a
			// transient walk error must not abort the watch loop.
			if path == root && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if shouldIgnoreWatchPathWithMatcher(rel, true, ignore) || isProductionFrontendOutputDir(root, rel) {
				return filepath.SkipDir
			}
			ignore.LoadDir(rel)
			dirs = append(dirs, rel)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if generated[rel] {
			return nil
		}
		if shouldIgnoreWatchPathWithMatcher(rel, false, ignore) {
			return nil
		}
		// Tests and their embed directives do not belong to a runtime build.
		// Explicit runtime embeds can still add these bytes through the owner.
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if !isWatchedFile(rel) && classifyAssistantWatchPath(root, rel) == "" {
			if _, ok := productionFrontendForWatchPath(root, rel); !ok {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		var data []byte
		stamp, reused := reusableStamp(previous.files, rel, info, false)
		if !reused {
			stamp, data, err = stampWatchedFile(path, info, false)
			if err != nil {
				return nil
			}
		}
		snapshot.files[rel] = stamp
		if filepath.Ext(rel) == ".go" {
			patterns, cached := cachedGoEmbedPatterns(path, stamp)
			if !cached {
				if data == nil {
					if data, err = os.ReadFile(path); err != nil {
						return nil
					}
				}
				patterns = parseGoEmbedPatterns(string(data))
				storeGoEmbedPatterns(path, stamp, patterns)
			}
			pkgDir := filepath.Dir(rel)
			for _, pattern := range patterns {
				if err := addEmbeddedSnapshotFiles(root, pkgDir, pattern, snapshot.files, previous.files, ignore); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fileSnapshot{}, err
	}
	// WalkDir visits each directory exactly once, so the list is already
	// unique; DFS pre-order is not string-sorted, so sort stays.
	sort.Strings(dirs)
	snapshot.dirs = dirs
	return snapshot, nil
}

// reusableStamp returns the previous stamp for rel when the file's size,
// permissions, and mtime are unchanged, matching Git's index heuristic. An
// in-place rewrite that preserves all three within mtime resolution is not
// detected until the file is touched again.
func reusableStamp(previous map[string]fileStamp, rel string, info fs.FileInfo, embedded bool) (fileStamp, bool) {
	prev, ok := previous[rel]
	if !ok || prev.embed != embedded || prev.size != info.Size() || prev.mode != uint32(info.Mode().Perm()) || !prev.modTime.Equal(info.ModTime().UTC().Round(0)) {
		return fileStamp{}, false
	}
	return prev, true
}

func stampWatchedFile(path string, info fs.FileInfo, embedded bool) (fileStamp, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileStamp{}, nil, err
	}
	sum := sha256.Sum256(data)
	return fileStamp{
		modTime: info.ModTime().UTC().Round(0),
		size:    info.Size(),
		mode:    uint32(info.Mode().Perm()),
		hash:    hex.EncodeToString(sum[:]),
		embed:   embedded,
	}, data, nil
}

func snapshotsEqual(a, b fileSnapshot) bool {
	if a.retryGenerated && len(changedGeneratedContent(a, b)) > 0 {
		return false
	}
	if len(a.generated) != len(b.generated) {
		return false
	}
	for path, present := range a.generated {
		if other, ok := b.generated[path]; !ok || other != present {
			return false
		}
	}
	if len(a.files) != len(b.files) {
		return false
	}
	for path, stamp := range a.files {
		if other, ok := b.files[path]; !ok || !stamp.sameContent(other) {
			return false
		}
	}
	return true
}

func changedPaths(before, after fileSnapshot) []string {
	seen := make(map[string]bool, len(before.files)+len(after.files))
	paths := make([]string, 0, len(before.files)+len(after.files))
	for path, stamp := range before.files {
		seen[path] = true
		if other, ok := after.files[path]; !ok || !stamp.sameContent(other) {
			paths = append(paths, path)
		}
	}
	for path := range after.files {
		if _, ok := seen[path]; ok {
			continue
		}
		paths = append(paths, path)
		seen[path] = true
	}
	for path, present := range before.generated {
		if other, ok := after.generated[path]; (!ok || other != present) && !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	for path := range after.generated {
		if _, existed := before.generated[path]; !existed && !seen[path] {
			paths = append(paths, path)
		}
	}
	if before.retryGenerated {
		for _, path := range changedGeneratedContent(before, after) {
			if !seen[path] {
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func snapshotFingerprint(snapshot fileSnapshot) string {
	paths := make([]string, 0, len(snapshot.files))
	for path := range snapshot.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	var scratch []byte
	for _, path := range paths {
		stamp := snapshot.files[path]
		scratch = append(scratch[:0], path...)
		scratch = append(scratch, 0)
		scratch = append(scratch, stamp.hash...)
		scratch = append(scratch, 0)
		scratch = strconv.AppendInt(scratch, stamp.size, 10)
		scratch = append(scratch, ':')
		scratch = strconv.AppendUint(scratch, uint64(stamp.mode), 8)
		scratch = append(scratch, ':')
		scratch = strconv.AppendBool(scratch, stamp.embed)
		scratch = append(scratch, 0)
		_, _ = h.Write(scratch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func buildSourceSnapshot(snapshot fileSnapshot) *build.SourceSnapshot {
	files := make(map[string]build.SourceSnapshotFile, len(snapshot.files))
	for rel, stamp := range snapshot.files {
		files[rel] = build.SourceSnapshotFile{
			Size:        stamp.size,
			ModTimeNano: stamp.modTime.UnixNano(),
			Perm:        stamp.mode,
			Hash:        stamp.hash,
			Embedded:    stamp.embed,
		}
	}
	return &build.SourceSnapshot{Files: files}
}

type fileChangeWatcher struct {
	events       chan struct{}
	watcher      *fsnotify.Watcher
	root         string
	resolvedRoot string
	ignore       *watchignore.Matcher
	done         chan struct{}
}

func newFileChangeWatcher(root string, snapshot fileSnapshot) (*fileChangeWatcher, error) {
	underlying, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	fw := &fileChangeWatcher{
		events:       make(chan struct{}, 1),
		watcher:      underlying,
		root:         root,
		resolvedRoot: resolvedRoot,
		ignore:       watchignore.New(root),
		done:         make(chan struct{}),
	}
	if err := fw.addSnapshotDirs(snapshot); err != nil {
		_ = underlying.Close()
		return nil, err
	}
	go fw.run()
	return fw, nil
}

func (fw *fileChangeWatcher) addSnapshotDirs(snapshot fileSnapshot) error {
	if len(snapshot.dirs) == 0 {
		return fw.addTree(fw.root)
	}
	if err := fw.watcher.Add(fw.root); err != nil {
		return err
	}
	for _, rel := range snapshot.dirs {
		if rel == "." || rel == "" {
			continue
		}
		if err := fw.watcher.Add(filepath.Join(fw.root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (fw *fileChangeWatcher) Events() <-chan struct{} {
	if fw == nil {
		return nil
	}
	return fw.events
}

func (fw *fileChangeWatcher) Close() error {
	if fw == nil {
		return nil
	}
	err := fw.watcher.Close()
	<-fw.done
	return err
}

func (fw *fileChangeWatcher) run() {
	defer close(fw.done)
	defer close(fw.events)
	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			fw.handleEvent(event)
		case _, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.signal()
		}
	}
}

// relativeToRoot maps an event path to a root-relative path. fsnotify reports
// symlink-resolved paths (macOS reports /private/tmp/... for /tmp/...), so a
// path that escapes the configured root is retried against the resolved root
// before being treated as foreign.
func (fw *fileChangeWatcher) relativeToRoot(path string) (string, bool) {
	for _, root := range []string{fw.root, fw.resolvedRoot} {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return rel, true
	}
	return "", false
}

func (fw *fileChangeWatcher) handleEvent(event fsnotify.Event) {
	path := filepath.Clean(event.Name)
	rel, ok := fw.relativeToRoot(path)
	if !ok {
		fw.signal()
		return
	}
	if rel == "." {
		return
	}
	rel = filepath.ToSlash(rel)
	if filepath.Base(rel) == ".gitignore" {
		fw.ignore = watchignore.New(fw.root)
		fw.signal()
		return
	}
	if shouldIgnoreWatchPathWithMatcher(rel, false, fw.ignore) {
		return
	}
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			_ = fw.addTree(path)
		}
	}
	fw.signal()
}

func (fw *fileChangeWatcher) signal() {
	select {
	case fw.events <- struct{}{}:
	default:
	}
}

func (fw *fileChangeWatcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if rel, ok := fw.relativeToRoot(path); ok {
			rel = filepath.ToSlash(rel)
			if rel != "." && shouldIgnoreWatchPathWithMatcher(rel, true, fw.ignore) {
				return filepath.SkipDir
			}
			fw.ignore.LoadDir(rel)
		}
		return fw.watcher.Add(path)
	})
}
