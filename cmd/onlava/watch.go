package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"

	"onlava.com/internal/app"
)

const (
	watchPollInterval       = 250 * time.Millisecond
	watchBackupPollInterval = 2 * time.Second
	watchSettleDelay        = 500 * time.Millisecond
	stopTimeout             = 5 * time.Second
)

type fileStamp struct {
	modTime time.Time
	size    int64
}

type fileSnapshot map[string]fileStamp

func runWithWatch(addr string, verbose, jsonMode bool, appRoot string) error {
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
	stopParentMonitor := startParentMonitor(ctx, cancel)
	defer stopParentMonitor()

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		return err
	}

	supervisor, err := newDevSupervisor(ctx, root, cfg, addr, verbose, jsonMode)
	if err != nil {
		return err
	}
	defer supervisor.Close()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}

	if err := supervisor.RebuildAndRestart(ctx, true, snapshot, nil); err != nil {
		supervisor.console.InitialBuildFailed(err)
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
		if err := supervisor.RebuildAndRestart(ctx, false, snapshot, paths); err != nil {
			supervisor.console.RebuildFailed(err)
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
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
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
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
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
	case "node_modules", "onlava_internal_main":
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
		case "node_modules", "onlava_internal_main":
			return true
		}
	}
	return false
}

func isWatchedFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	switch base {
	case ".onlava.json", "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	if isWatchedRootDotFile(rel) {
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

func discoverEmbeddedWatchFiles(root string) (map[string]struct{}, error) {
	files := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
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
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		patterns := parseGoEmbedPatterns(string(data))
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
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
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
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
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
	events  chan struct{}
	watcher *fsnotify.Watcher
	root    string
	done    chan struct{}
}

func newFileChangeWatcher(root string) (*fileChangeWatcher, error) {
	underlying, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	fw := &fileChangeWatcher{
		events:  make(chan struct{}, 1),
		watcher: underlying,
		root:    root,
		done:    make(chan struct{}),
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

func (fw *fileChangeWatcher) handleEvent(event fsnotify.Event) {
	path := filepath.Clean(event.Name)
	rel, err := filepath.Rel(fw.root, path)
	if err != nil {
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
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(fw.root, path)
		if err != nil {
			return err
		}
		if rel != "." && shouldIgnoreWatchPath(rel) {
			return filepath.SkipDir
		}
		return fw.watcher.Add(path)
	})
}
