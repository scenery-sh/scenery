package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fsnotify/fsnotify"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/watchignore"
)

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
			// A watcher error, such as an event queue overflow, leaves the
			// directory membership the watcher reported uncertain, so the next
			// scans read every directory again.
			watchListings(fw.root).Invalidate()
			compiler.InvalidateGeneratedPathListings(fw.root)
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
