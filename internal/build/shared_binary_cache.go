package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	sharedBinaryCacheVersion      = "v2"
	sharedBinaryLinkSlots         = 2
	sharedBinaryRegistrationGrace = time.Minute
)

var sharedBinarySubscriberSequence atomic.Uint64

func sharedBinaryRoot() (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "build", "shared-binaries", sharedBinaryCacheVersion), nil
}

func runSharedGoBuildContext(ctx context.Context, result *Result) error {
	if result == nil || result.Target == nil || result.Target.Role != "development" || result.ProductionAssets {
		return runGoBuildContext(ctx, result)
	}
	return runSharedGoBuildWithInputCheck(ctx, result, func(ctx context.Context) error {
		return verifySharedBinaryInputs(ctx, result, buildInputManifest)
	})
}

// Input discovery is injected at the native tool boundary for in-process tests.
// Every production caller supplies the complete current Go input check above.
func runSharedGoBuildWithInputCheck(ctx context.Context, result *Result, checkInputs func(context.Context) error) error {
	root, err := sharedBinaryRoot()
	if err != nil {
		return err
	}
	// The executable stays private to this build. Keep the fair link budget;
	// cancellation owns only this build.
	digest := sha256.Sum256([]byte(result.Dir))
	releaseSlot, err := acquireSharedBinarySlotObserved(ctx, root, hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	defer releaseSlot()
	if err := runGoBuildContext(ctx, result); err != nil {
		return err
	}
	return observeBuildAction(ctx, "build.shared_input_check", func() error { return checkInputs(ctx) })
}

func acquireSharedBinarySlotObserved(ctx context.Context, root, key string) (func(), error) {
	waitStarted := time.Now()
	release, err := acquireSharedBinarySlot(ctx, root, key)
	queue := time.Since(waitStarted)
	RecordStep(ctx, Step{Name: "build.shared_link_queue", StartedAt: waitStarted, Duration: queue, QueueDuration: queue, Cache: "not_applicable", Reason: "fair_link_slot", OK: err == nil})
	return release, err
}

func acquireSharedBinarySlot(ctx context.Context, root, key string) (func(), error) {
	ticketDirectory := filepath.Join(root, "queue")
	ticketName := fmt.Sprintf("%020d-%010d-%020d.ticket", time.Now().UnixNano(), os.Getpid(), sharedBinarySubscriberSequence.Add(1))
	ticketPath, releaseTicket, err := createSharedBinaryLease(ticketDirectory, ticketName)
	if err != nil {
		return nil, err
	}
	releaseTicketOnReturn := true
	defer func() {
		if releaseTicketOnReturn {
			releaseTicket()
			_ = os.Remove(ticketPath)
		}
	}()

	start := int(key[0]) % sharedBinaryLinkSlots
	for {
		position, err := sharedBinaryTicketPosition(ticketDirectory, filepath.Base(ticketPath))
		if err != nil {
			return nil, err
		}
		if position >= 0 && position < sharedBinaryLinkSlots {
			for offset := 0; offset < sharedBinaryLinkSlots; offset++ {
				path := filepath.Join(root, "slots", fmt.Sprintf("link-%d.lock", (start+offset)%sharedBinaryLinkSlots))
				if releaseSlot, acquired, err := trySharedBinaryLock(path); err != nil {
					return nil, err
				} else if acquired {
					releaseTicketOnReturn = false
					return func() {
						releaseSlot()
						releaseTicket()
						_ = os.Remove(ticketPath)
					}, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func createSharedBinaryLease(directory, name string) (string, func(), error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", nil, err
	}
	temporary, err := os.CreateTemp(directory, ".register-")
	if err != nil {
		return "", nil, err
	}
	temporaryPath := temporary.Name()
	release, acquired, err := trySharedBinaryFileLock(temporary)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return "", nil, err
	}
	if !acquired {
		_ = os.Remove(temporaryPath)
		return "", nil, fmt.Errorf("lock unique shared binary lease")
	}
	path := filepath.Join(directory, name)
	if err := os.Rename(temporaryPath, path); err != nil {
		release()
		_ = os.Remove(temporaryPath)
		return "", nil, err
	}
	return path, release, nil
}

func sharedBinaryTicketPosition(directory, ticket string) (int, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return -1, nil
	}
	if err != nil {
		return -1, err
	}
	active := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".register-") {
			if err := cleanupAbandonedSharedBinaryRegistration(directory, entry, time.Now()); err != nil {
				return -1, err
			}
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ticket" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		release, acquired, exists, lockErr := trySharedBinaryExistingLock(path)
		if lockErr != nil {
			return -1, lockErr
		}
		if !exists {
			continue
		}
		if acquired {
			release()
			_ = os.Remove(path)
			continue
		}
		active = append(active, entry.Name())
	}
	sort.Strings(active)
	for index, name := range active {
		if name == ticket {
			return index, nil
		}
	}
	return -1, nil
}

func cleanupAbandonedSharedBinaryRegistration(directory string, entry os.DirEntry, now time.Time) error {
	if entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if now.Sub(info.ModTime()) < sharedBinaryRegistrationGrace {
		return nil
	}
	path := filepath.Join(directory, entry.Name())
	release, acquired, exists, err := trySharedBinaryExistingLock(path)
	if err != nil || !exists || !acquired {
		return err
	}
	release()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func acquireSharedBinaryLock(ctx context.Context, path string) (func(), error) {
	for {
		if release, acquired, err := trySharedBinaryLock(path); err != nil {
			return nil, err
		} else if acquired {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func regularArtifactPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular(), nil
}
