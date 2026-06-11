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
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
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
}

type fileSnapshot map[string]fileStamp

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

func runWithWatch(listen devListenRequest, verbose, jsonMode bool, appRoot string) error {
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

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		return err
	}

	agentClient, agentSession, backend, restoreAgentEnv, err := prepareDevAgentSession(ctx, root, cfg, listen)
	if err != nil {
		return err
	}
	defer restoreAgentEnv()

	supervisor, err := newDevSupervisor(ctx, root, cfg, backend, verbose, jsonMode)
	if err != nil {
		return err
	}
	supervisor.agent = agentClient
	supervisor.agentSession = agentSession
	defer supervisor.Close()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}

	if err := supervisor.RebuildAndRestart(ctx, true, snapshot); err != nil {
		supervisor.console.InitialBuildFailed(err, supervisor.runURLs())
	}

	watcher, err := newFileChangeWatcher(root)
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
	hosts := map[string]string{}
	addHost := func(route, host string) {
		route = sanitizeRouteLabel(route)
		host = normalizeRouteNamespaceHost(host)
		if route == "" || host == "" {
			return
		}
		hosts[route] = host
	}
	addHost(localagent.RouteAPI, cfg.Proxy.APIHost)
	addHost("console", cfg.Proxy.ConsoleHost)
	addHost(localagent.RouteTemporal, cfg.Proxy.TemporalHost)
	addHost(localagent.RouteGrafana, cfg.Proxy.GrafanaHost)
	for name, frontend := range cfg.Proxy.Frontends {
		addHost(name, frontend.Host)
	}
	if len(hosts) == 0 {
		hosts = nil
	}
	workspace := sanitizeRouteLabel(cfg.Proxy.Workspace)
	if workspace == "" && len(hosts) == 0 {
		workspace = sanitizeRouteLabel(cfg.AppID())
	}
	baseDomain := normalizeRouteNamespaceHost(cfg.Proxy.RouteBaseDomain)
	if baseDomain == "" {
		baseDomain = localagent.DefaultRouteBaseDomain
	}
	return localagent.RouteNamespace{
		Workspace:  workspace,
		BaseDomain: baseDomain,
		Hosts:      hosts,
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

func rejectLiveDuplicateDevSession(root string, existing []localagent.Session) error {
	for _, session := range existing {
		if cleanAbsPath(session.AppRoot) != cleanAbsPath(root) {
			continue
		}
		pid, live := sessionOwnerProcessLive(session)
		if live {
			return fmt.Errorf("scenery up is already running for app root %s under owner PID %d; stop it with `scenery down --app-root %q`, or use a separate Git worktree for another live code copy", root, pid, root)
		}
	}
	return nil
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
	substrates, _ := client.ListSubstrates(ctx)
	if health.PID > 0 {
		if err := signalAgentPID(health.PID); err != nil {
			return fmt.Errorf("stop stale scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentStop(ctx, client, health.PID); err != nil {
			return err
		}
		waitForSubstrateProcessesExit(ctx, substrates, 3*time.Second)
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

func waitForSubstrateProcessesExit(ctx context.Context, substrates []localagent.Substrate, timeout time.Duration) {
	if len(substrates) == 0 || timeout <= 0 {
		return
	}
	pids := map[int]bool{}
	for _, substrate := range substrates {
		for _, pid := range substrate.PIDs {
			if pid > 0 && pid != os.Getpid() {
				pids[pid] = true
			}
		}
	}
	if len(pids) == 0 {
		return
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive := false
		for pid := range pids {
			if _, ok := inspectProcess(pid); ok {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
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
			return nil, ctx.Err()
		case <-ticker.C:
		}

		next, err := scanWatchedFiles(root)
		if err != nil {
			return nil, err
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
			return nil, ctx.Err()
		case _, ok := <-events:
			if !ok {
				return waitForStableChangePolling(ctx, root, current)
			}
		case <-ticker.C:
			next, err := scanWatchedFiles(root)
			if err != nil {
				return nil, err
			}
			if snapshotsEqual(current, next) {
				continue
			}
			return waitForSnapshotToSettlePolling(ctx, root, next)
		}

		next, err := waitForSnapshotToSettleEvents(ctx, root, events)
		if err != nil {
			return nil, err
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
			return nil, ctx.Err()
		case <-timer.C:
			return current, nil
		case <-ticker.C:
			next, err := scanWatchedFiles(root)
			if err != nil {
				return nil, err
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
			return nil, ctx.Err()
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
	snapshot := make(fileSnapshot)
	embeddedFiles, err := discoverEmbeddedWatchFiles(root)
	if err != nil {
		return nil, err
	}
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

		if d.IsDir() {
			if shouldSkipWatchDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !isWatchedFile(rel) {
			if _, ok := embeddedFiles[rel]; !ok {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		snapshot[rel] = fileStamp{
			modTime: info.ModTime().UTC().Round(0),
			size:    info.Size(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func shouldSkipWatchDir(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "scenery_internal_main":
		return true
	default:
		return false
	}
}

func shouldIgnoreWatchPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return false
	}
	if isWatchedRootDotFile(rel) {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
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
	case ".scenery.json", "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if isWatchedRootDotFile(rel) {
		return true
	}
	if strings.HasSuffix(rel, ".worker.ts") {
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
		return false
	}
}

type embedPatternCacheEntry struct {
	stamp    fileStamp
	patterns []string
}

// embedPatternCache memoizes parsed //go:embed patterns per Go file so repeated
// watch scans stat files instead of re-reading every .go file in the app.
var embedPatternCache sync.Map

func embedPatternsForFile(path string, info fs.FileInfo) []string {
	stamp := fileStamp{modTime: info.ModTime().UTC().Round(0), size: info.Size()}
	if cached, ok := embedPatternCache.Load(path); ok {
		entry := cached.(embedPatternCacheEntry)
		if entry.stamp == stamp {
			return entry.patterns
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	patterns := parseGoEmbedPatterns(string(data))
	embedPatternCache.Store(path, embedPatternCacheEntry{stamp: stamp, patterns: patterns})
	return patterns
}

func discoverEmbeddedWatchFiles(root string) (map[string]struct{}, error) {
	files := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
		if d.IsDir() {
			if shouldSkipWatchDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(rel) != ".go" || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		patterns := embedPatternsForFile(path, info)
		if len(patterns) == 0 {
			return nil
		}
		pkgDir := filepath.Dir(rel)
		for _, pattern := range patterns {
			if err := addEmbeddedPatternFiles(root, pkgDir, pattern, files); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func parseGoEmbedPatterns(src string) []string {
	var patterns []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:embed") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "//go:embed"))
		for rest != "" {
			token, next, ok := nextEmbedToken(rest)
			if !ok {
				break
			}
			if token != "" {
				patterns = append(patterns, token)
			}
			rest = next
		}
	}
	return patterns
}

func nextEmbedToken(input string) (string, string, bool) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", "", false
	}
	if quote, _ := utf8.DecodeRuneInString(input); quote == '"' || quote == '`' {
		for i := 1; i <= len(input); i++ {
			token, err := strconv.Unquote(input[:i])
			if err == nil {
				return token, input[i:], true
			}
		}
		return "", "", false
	}
	i := 0
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return input[:i], input[i:], true
}

func addEmbeddedPatternFiles(root, pkgDir, pattern string, files map[string]struct{}) error {
	includeHidden := false
	if strings.HasPrefix(pattern, "all:") {
		includeHidden = true
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return nil
	}
	search := filepath.Join(root, filepath.FromSlash(pkgDir), filepath.FromSlash(pattern))
	matches, err := filepath.Glob(search)
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := addEmbeddedPath(root, match, includeHidden, files); err != nil {
			return err
		}
	}
	return nil
}

func addEmbeddedPath(root, path string, includeHidden bool, files map[string]struct{}) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if includeHidden || !hasHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && child != path {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, child)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if !includeHidden && hasHiddenOrUnderscorePart(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if includeHidden || !hasHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	})
}

func hasHiddenOrUnderscorePart(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}

func snapshotsEqual(a, b fileSnapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for path, stamp := range a {
		if other, ok := b[path]; !ok || other != stamp {
			return false
		}
	}
	return true
}

func changedPaths(before, after fileSnapshot) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	paths := make([]string, 0, len(before)+len(after))
	for path, stamp := range before {
		seen[path] = struct{}{}
		if other, ok := after[path]; !ok || other != stamp {
			paths = append(paths, path)
		}
	}
	for path := range after {
		if _, ok := seen[path]; ok {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func snapshotFingerprint(snapshot fileSnapshot) string {
	paths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		stamp := snapshot[path]
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(stamp.modTime.Format(time.RFC3339Nano)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(fmt.Sprintf("%d", stamp.size)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type fileChangeWatcher struct {
	events       chan struct{}
	watcher      *fsnotify.Watcher
	root         string
	resolvedRoot string
	done         chan struct{}
}

func newFileChangeWatcher(root string) (*fileChangeWatcher, error) {
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
		done:         make(chan struct{}),
	}
	if err := fw.addTree(root); err != nil {
		_ = underlying.Close()
		return nil, err
	}
	go fw.run()
	return fw, nil
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
	if shouldIgnoreWatchPath(rel) {
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
		if rel, ok := fw.relativeToRoot(path); ok && rel != "." && shouldIgnoreWatchPath(rel) {
			return filepath.SkipDir
		}
		return fw.watcher.Add(path)
	})
}
