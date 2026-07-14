package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/fsnotify/fsnotify"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/watchignore"
)

var (
	watchPollInterval       = 250 * time.Millisecond
	watchBackupPollInterval = 2 * time.Second
	watchSettleDelay        = 500 * time.Millisecond
)

const stopTimeout = 5 * time.Second

type fileStamp struct {
	modTime time.Time
	size    int64
	mode    uint32
	hash    string
	embed   bool
}

type fileSnapshot struct {
	files map[string]fileStamp
	dirs  []string
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

func runWithWatch(listen devListenRequest, verbose, jsonMode bool, appRoot string) (runErr error) {
	applyWatchTimingOverridesFromEnv()

	start, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
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
		console.Finish(runErr)
		if jsonMode && runErr != nil {
			runErr = &silentCLIError{err: runErr, code: cliExitCode(runErr)}
		}
	}()

	var snapshot fileSnapshot
	if err := console.Phase("Scanning source files", func() error {
		var err error
		snapshot, err = scanWatchedFiles(root)
		return err
	}); err != nil {
		return err
	}

	preparedSession, err := prepareDevAgentSessionDetailed(ctx, root, cfg, listen, console)
	if err != nil {
		if preparedSession != nil && preparedSession.Cleanup != nil {
			preparedSession.Cleanup()
		}
		var already *devSessionAlreadyRunningError
		if errors.As(err, &already) && !detachedDevChildMode() {
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

	supervisor, err := newDevSupervisor(ctx, root, cfg, backend, console, agentClient, agentSession)
	if err != nil {
		return err
	}
	supervisor.adoptManagedFrontends(preparedSession.FrontendProcesses)
	defer supervisor.Close()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}
	supervisor.addStartupReady(preparedSession.FrontendReady)

	if err := supervisor.RebuildAndRestart(ctx, true, snapshot); err != nil {
		supervisor.console.InitialBuildFailed(err, supervisor.runURLs())
		if len(preparedSession.FrontendProcesses) > 0 {
			return err
		}
	}

	watcher, err := newFileChangeWatcher(root, snapshot)
	if err != nil {
		if verbose {
			supervisor.console.printf(supervisor.console.err, "  %s\n\n", err.Error())
		}
	}
	if watcher != nil {
		defer watcher.Close()
	}

	for {
		nextSnapshot, err := waitForStableChange(ctx, root, snapshot, watcher)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		paths := changedPaths(snapshot, nextSnapshot)
		snapshot = nextSnapshot
		supervisor.announceRebuild(paths)
		if err := supervisor.RebuildAndRestart(ctx, false, snapshot); err != nil {
			supervisor.console.RebuildFailed(err)
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
	console.printf(console.out, "  %s\n\n", console.palette.Dim("Following the running runtime's logs. Ctrl+C detaches without stopping it."))
	followCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ownerExited := make(chan struct{})
	go func() {
		if devSessionOwnerGone(followCtx, root) {
			close(ownerExited)
			cancel()
		}
	}()
	err := runSceneryLogsFunc(followCtx, os.Stdout, []string{"--follow", "--app-root", root})
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
	client, err := localagent.DefaultClient()
	if err != nil {
		return false
	}
	ticker := time.NewTicker(devSessionOwnerExitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
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

func sessionOwnerProcessLive(session localagent.Session) (int, bool) {
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return 0, false
	}
	owner := session.Owner
	if owner.PID != ownerPID {
		owner = localagent.CaptureOwner(ownerPID, "scenery up")
	} else if owner.PID <= 0 {
		owner.PID = ownerPID
	}
	if localagent.VerifyOwner(owner) == nil {
		return ownerPID, true
	}
	_, ok := inspectProcess(ownerPID)
	return ownerPID, ok
}

func discoverDevGitBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ensureDevAgentDashboardBackend(ctx context.Context, client *localagent.Client) error {
	if client == nil {
		return nil
	}
	health, err := client.Health(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(health.DashboardBackend.Addr) != "" {
		return nil
	}
	if health.PID == os.Getpid() {
		return fmt.Errorf("scenery agent did not expose dashboard backend")
	}
	if health.PID > 0 {
		if err := signalAgentPID(health.PID); err != nil {
			return fmt.Errorf("stop stale scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentStop(ctx, client, health.PID); err != nil {
			return err
		}
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	opts := localagent.StartOptions{RouterAddr: health.RouterAddr}
	switch health.RouterScheme {
	case "https":
		opts.RouterTLS = true
	case "http":
		opts.RouterHTTP = true
	}
	logOffset := fileSize(paths.LogPath)
	if err := localagent.StartProcess(paths, opts); err != nil {
		return err
	}
	restarted, err := waitForAgentStart(ctx, client, health.PID, paths.LogPath, logOffset)
	if err != nil {
		return err
	}
	if strings.TrimSpace(restarted.DashboardBackend.Addr) == "" {
		return fmt.Errorf("restarted scenery agent did not expose dashboard backend")
	}
	return nil
}

func waitForStableChange(ctx context.Context, root string, current fileSnapshot, watcher *fileChangeWatcher) (fileSnapshot, error) {
	if watcher != nil {
		return waitForStableChangeEvents(ctx, root, current, watcher.Events())
	}
	return waitForStableChangePolling(ctx, root, current)
}

func waitForStableChangePolling(ctx context.Context, root string, current fileSnapshot) (fileSnapshot, error) {
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case <-ticker.C:
		}

		next, err := scanWatchedFiles(root)
		if err != nil {
			return fileSnapshot{}, err
		}
		if snapshotsEqual(current, next) {
			continue
		}
		return waitForSnapshotToSettlePolling(ctx, root, next)
	}
}

func waitForStableChangeEvents(ctx context.Context, root string, current fileSnapshot, events <-chan struct{}) (fileSnapshot, error) {
	ticker := time.NewTicker(watchBackupPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case _, ok := <-events:
			if !ok {
				return waitForStableChangePolling(ctx, root, current)
			}
		case <-ticker.C:
			next, err := scanWatchedFiles(root)
			if err != nil {
				return fileSnapshot{}, err
			}
			if snapshotsEqual(current, next) {
				continue
			}
			return waitForSnapshotToSettlePolling(ctx, root, next)
		}

		next, err := waitForSnapshotToSettleEvents(ctx, root, events)
		if err != nil {
			return fileSnapshot{}, err
		}
		if snapshotsEqual(current, next) {
			continue
		}
		return next, nil
	}
}

func waitForSnapshotToSettlePolling(ctx context.Context, root string, current fileSnapshot) (fileSnapshot, error) {
	timer := time.NewTimer(watchSettleDelay)
	defer timer.Stop()
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case <-timer.C:
			return current, nil
		case <-ticker.C:
			next, err := scanWatchedFiles(root)
			if err != nil {
				return fileSnapshot{}, err
			}
			if snapshotsEqual(current, next) {
				continue
			}
			current = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(watchSettleDelay)
		}
	}
}

func waitForSnapshotToSettleEvents(ctx context.Context, root string, events <-chan struct{}) (fileSnapshot, error) {
	timer := time.NewTimer(watchSettleDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return fileSnapshot{}, ctx.Err()
		case <-timer.C:
			return scanWatchedFiles(root)
		case _, ok := <-events:
			if !ok {
				return scanWatchedFiles(root)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(watchSettleDelay)
		}
	}
}

func scanWatchedFiles(root string) (fileSnapshot, error) {
	snapshot := fileSnapshot{files: make(map[string]fileStamp)}
	dirs := map[string]struct{}{}
	ignore := watchignore.New(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
			if shouldIgnoreWatchPathWithMatcher(rel, true, ignore) {
				return filepath.SkipDir
			}
			ignore.LoadDir(rel)
			dirs[rel] = struct{}{}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if shouldIgnoreWatchPathWithMatcher(rel, false, ignore) {
			return nil
		}
		if generate.IsManagedEditorWorkFile(root, rel) {
			return nil
		}
		if !isWatchedFile(rel) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		stamp, data, err := stampWatchedFile(path, info, false)
		if err != nil {
			return nil
		}
		snapshot.files[rel] = stamp
		if filepath.Ext(rel) == ".go" {
			pkgDir := filepath.Dir(rel)
			for _, pattern := range parseGoEmbedPatterns(string(data)) {
				if err := addEmbeddedSnapshotFiles(root, pkgDir, pattern, snapshot.files, ignore); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fileSnapshot{}, err
	}
	snapshot.dirs = make([]string, 0, len(dirs))
	for dir := range dirs {
		snapshot.dirs = append(snapshot.dirs, dir)
	}
	sort.Strings(snapshot.dirs)
	return snapshot, nil
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

func shouldIgnoreWatchPathWithMatcher(rel string, isDir bool, ignore *watchignore.Matcher) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return false
	}
	if shouldIgnoreWatchPathBuiltin(rel, isDir) {
		return true
	}
	if ignore != nil && ignore.Ignored(rel, isDir) {
		return true
	}
	return false
}

func shouldIgnoreWatchPathBuiltin(rel string, isDir bool) bool {
	if !isDir && isWatchedRootDotFile(rel) {
		return false
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if !isDir && i == len(parts)-1 && part == ".gitignore" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
		switch part {
		case "node_modules", "scenery_internal_main":
			return true
		}
	}
	return false
}

func isWatchedFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	switch base {
	case ".gitignore", "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if app.IsConfigFilename(base) {
		return true
	}
	if isWatchedRootDotFile(rel) {
		return true
	}
	if strings.HasSuffix(rel, ".worker.ts") {
		return true
	}
	if strings.HasSuffix(rel, "/db/schema.hcl") {
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".m", ".mm", ".s", ".S", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

func isWatchedRootDotFile(rel string) bool {
	switch filepath.ToSlash(rel) {
	case ".env", ".env.local":
		return true
	default:
		return app.IsConfigFilename(rel)
	}
}

func snapshotsEqual(a, b fileSnapshot) bool {
	if len(a.files) != len(b.files) {
		return false
	}
	for path, stamp := range a.files {
		if other, ok := b.files[path]; !ok || other != stamp {
			return false
		}
	}
	return true
}

func changedPaths(before, after fileSnapshot) []string {
	seen := make(map[string]struct{}, len(before.files)+len(after.files))
	paths := make([]string, 0, len(before.files)+len(after.files))
	for path, stamp := range before.files {
		seen[path] = struct{}{}
		if other, ok := after.files[path]; !ok || other != stamp {
			paths = append(paths, path)
		}
	}
	for path := range after.files {
		if _, ok := seen[path]; ok {
			continue
		}
		paths = append(paths, path)
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
	for _, path := range paths {
		stamp := snapshot.files[path]
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(stamp.hash))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(fmt.Sprintf("%d:%d:%o:%t", stamp.size, stamp.modTime.UnixNano(), stamp.mode, stamp.embed)))
		_, _ = h.Write([]byte{0})
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
	if generate.IsManagedEditorWorkFile(fw.root, rel) {
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
