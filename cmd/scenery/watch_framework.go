package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	appcfg "scenery.sh/internal/app"
)

var (
	frameworkWatchPollInterval = time.Second
	frameworkWatchDebounce     = 100 * time.Millisecond
)

// frameworkSourceWatcher observes .scenery/framework/link.json and, while a
// local Scenery checkout is linked, watches its source tree for edits so
// `scenery up` automatically rebuilds and hands off to the updated producer.
type frameworkSourceWatcher struct {
	events chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func newFrameworkSourceWatcher(parent context.Context, appRoot string) *frameworkSourceWatcher {
	ctx, cancel := context.WithCancel(parent)
	w := &frameworkSourceWatcher{
		events: make(chan struct{}, 1),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go w.run(ctx, appRoot)
	return w
}

func (w *frameworkSourceWatcher) Events() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.events
}

func (w *frameworkSourceWatcher) Close() {
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (w *frameworkSourceWatcher) signal() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

func (w *frameworkSourceWatcher) run(ctx context.Context, appRoot string) {
	defer close(w.done)
	defer close(w.events)

	var (
		origin         string
		watcher        *fsnotify.Watcher
		watchedLinkDir bool
		debounce       *time.Timer
		debounceCh     <-chan time.Time
	)
	stopDebounce := func() {
		if debounce != nil {
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce = nil
			debounceCh = nil
		}
	}
	scheduleSignal := func() {
		if debounce == nil {
			debounce = time.NewTimer(frameworkWatchDebounce)
			debounceCh = debounce.C
			return
		}
		if !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(frameworkWatchDebounce)
	}
	closeWatcher := func() {
		stopDebounce()
		if watcher != nil {
			_ = watcher.Close()
			watcher = nil
		}
		watchedLinkDir = false
	}
	defer closeWatcher()

	linkDir := filepath.Dir(appcfg.FrameworkLinkPath(appRoot))
	linkPath := appcfg.FrameworkLinkPath(appRoot)
	syncTarget := func(initial bool) {
		nextOrigin := ""
		if link, linked, err := appcfg.ReadFrameworkLink(appRoot); err == nil && linked {
			nextOrigin = link.SourceOrigin
		}
		if !initial && nextOrigin != origin {
			w.signal()
		}
		if watcher != nil && nextOrigin == origin {
			if !watchedLinkDir {
				if info, err := os.Stat(linkDir); err == nil && info.IsDir() {
					if watcher.Add(linkDir) == nil {
						watchedLinkDir = true
					}
				}
			}
			return
		}
		closeWatcher()
		origin = nextOrigin
		nextWatcher, err := fsnotify.NewWatcher()
		if err != nil {
			return
		}
		if info, err := os.Stat(linkDir); err == nil && info.IsDir() {
			if nextWatcher.Add(linkDir) == nil {
				watchedLinkDir = true
			}
		}
		if origin != "" {
			_ = addFrameworkSourceWatchTree(nextWatcher, origin)
		}
		watcher = nextWatcher
	}

	syncTarget(true)
	ticker := time.NewTicker(frameworkWatchPollInterval)
	defer ticker.Stop()

	for {
		var fsEvents <-chan fsnotify.Event
		var fsErrors <-chan error
		if watcher != nil {
			fsEvents = watcher.Events
			fsErrors = watcher.Errors
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncTarget(false)
		case <-debounceCh:
			stopDebounce()
			w.signal()
		case event, ok := <-fsEvents:
			if !ok {
				closeWatcher()
				continue
			}
			clean := filepath.Clean(event.Name)
			if clean == linkPath || filepath.Dir(clean) == linkDir && filepath.Base(clean) == filepath.Base(linkPath) {
				syncTarget(false)
				w.signal()
				continue
			}
			if origin == "" {
				continue
			}
			if shouldIgnoreFrameworkWatchEvent(origin, clean) {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(clean); err == nil && info.IsDir() {
					_ = addFrameworkSourceWatchTree(watcher, clean)
				}
			}
			scheduleSignal()
		case _, ok := <-fsErrors:
			if !ok {
				closeWatcher()
				continue
			}
			if origin != "" {
				scheduleSignal()
			}
		}
	}
}

func addFrameworkSourceWatchTree(watcher *fsnotify.Watcher, root string) error {
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
		if path != root && shouldSkipFrameworkWatchDir(filepath.Base(path)) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func shouldSkipFrameworkWatchDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "target", "dist", "build", "coverage", "testdata", "vendor", "tmp", "var":
		return true
	}
	return false
}

func shouldIgnoreFrameworkWatchEvent(origin, path string) bool {
	rel, err := filepath.Rel(origin, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if shouldSkipFrameworkWatchDir(part) {
			return true
		}
	}
	base := parts[len(parts)-1]
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "~") {
		return true
	}
	return false
}
