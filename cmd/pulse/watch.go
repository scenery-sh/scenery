package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pulse.dev/internal/app"
)

const (
	watchPollInterval = 250 * time.Millisecond
	watchSettleDelay  = 500 * time.Millisecond
	stopTimeout       = 5 * time.Second
)

type fileStamp struct {
	modTime time.Time
	size    int64
}

type fileSnapshot map[string]fileStamp

func runWithWatch(addr string) error {
	root, cfg, err := app.DiscoverRoot(".")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		return err
	}

	supervisor, err := newDevSupervisor(root, cfg, addr)
	if err != nil {
		return err
	}
	defer supervisor.Close()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}

	if err := supervisor.RebuildAndRestart(ctx, true); err != nil {
		supervisor.console.InitialBuildFailed(err)
	}

	for {
		nextSnapshot, err := waitForStableChange(ctx, root, snapshot)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		snapshot = nextSnapshot
		supervisor.announceRebuild()
		if err := supervisor.RebuildAndRestart(ctx, false); err != nil {
			supervisor.console.RebuildFailed(err)
		}
	}
}

func waitForStableChange(ctx context.Context, root string, current fileSnapshot) (fileSnapshot, error) {
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
		return waitForSnapshotToSettle(ctx, root, next)
	}
}

func waitForSnapshotToSettle(ctx context.Context, root string, current fileSnapshot) (fileSnapshot, error) {
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

func scanWatchedFiles(root string) (fileSnapshot, error) {
	snapshot := make(fileSnapshot)
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
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !isWatchedFile(rel) {
			return nil
		}

		info, err := d.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(rel)] = fileStamp{
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
	case "node_modules", "pulse_internal_main":
		return true
	default:
		return false
	}
}

func isWatchedFile(rel string) bool {
	switch filepath.Base(rel) {
	case "pulse.app", "go.mod", "go.sum", "go.work", "go.work.sum", ".env", ".env.local":
		return true
	}
	return filepath.Ext(rel) == ".go"
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
