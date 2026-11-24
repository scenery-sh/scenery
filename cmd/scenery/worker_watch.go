package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/compiler"
)

type workerWatcher struct {
	root, directory, capabilities, link, token string
	client                                     *http.Client
	bundle                                     workerBundle
	active                                     map[string]*workerProcess
	all                                        []*workerProcess
	generation, sequence                       uint64
	retirements                                sync.WaitGroup
}

func (watch *workerWatcher) run(ctx context.Context, initial string, hostDone <-chan error) error {
	defer func() {
		watch.retirements.Wait()
		for _, process := range watch.all {
			process.stop()
		}
	}()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	seen := initial
	for {
		select {
		case err := <-hostDone:
			return err
		case <-ctx.Done():
			return <-hostDone
		case <-ticker.C:
			current, err := workerSourceFingerprint(watch.root)
			if err != nil {
				continue
			}
			if seen != current {
				seen = current
				if err := watch.refresh(ctx, current); err != nil {
					fmt.Fprintf(os.Stderr, "application update failed: %v\n", err)
				}
			} else if watch.hasStoppedWorker() {
				if err := watch.replace(ctx, watch.bundle); err != nil {
					fmt.Fprintf(os.Stderr, "worker restart failed: %v\n", err)
					select {
					case <-ctx.Done():
						return <-hostDone
					case <-time.After(time.Second):
					}
				}
			}
		}
	}
}

func (watch *workerWatcher) hasStoppedWorker() bool {
	for _, process := range watch.active {
		select {
		case <-process.done:
			return true
		default:
		}
	}
	return false
}

func (watch *workerWatcher) refresh(ctx context.Context, expected string) error {
	result, err := compiler.CompileContext(ctx, watch.root)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return fmt.Errorf("invalid application: %s", firstCompilerDiagnostic(result.Diagnostics))
	}
	if result.Manifest.ContractRevision != watch.bundle.ContractRevision {
		return fmt.Errorf("application contract changed; restart the application to load its new manifest")
	}
	bundle, err := buildWorkerBundle(ctx, result, "development", false)
	if err != nil {
		return err
	}
	current, err := workerSourceFingerprint(watch.root)
	if err != nil || current != expected {
		return errors.Join(fmt.Errorf("application changed while workers were compiling"), err)
	}
	return watch.replace(ctx, bundle)
}

func (watch *workerWatcher) replace(ctx context.Context, bundle workerBundle) error {
	next := make(map[string]*workerProcess, len(bundle.Programs))
	watch.sequence++
	var started []*workerProcess
	for index, program := range bundle.Programs {
		previous := watch.active[program.Process]
		if workerStillMatches(previous, program) {
			next[program.Process] = previous
			continue
		}
		id := fmt.Sprintf("%x-%x", watch.sequence, index)
		process, err := startForeignWorker(ctx, watch.directory, watch.capabilities, watch.link, id, program)
		if err != nil {
			stopWorkers(started)
			return err
		}
		started = append(started, process)
		next[program.Process] = process
	}
	if len(started) == 0 && len(next) == len(watch.active) {
		return nil
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
	var retired []*workerProcess
	for name, process := range watch.active {
		if next[name] != process {
			retired = append(retired, process)
		}
	}
	previous := watch.generation
	watch.generation, watch.active, watch.bundle = watch.sequence, next, bundle
	watch.retirements.Add(1)
	go func() {
		defer watch.retirements.Done()
		watch.retire(ctx, previous, retired)
	}()
	fmt.Fprintf(os.Stderr, "application generation %d ready (%d worker replacements)\n", watch.generation, len(started))
	return nil
}

func workerStillMatches(process *workerProcess, program workerProgram) bool {
	if process == nil || process.program.Digest != program.Digest || process.program.ContractRevision != program.ContractRevision || process.program.Runtime != program.Runtime {
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
		return 0, fmt.Errorf("Host did not confirm the generation")
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&status)
	return status.Current, err
}

func (watch *workerWatcher) retire(ctx context.Context, generation uint64, workers []*workerProcess) {
	path := "/__scenery/process/v1/generations/" + strconv.FormatUint(generation, 10)
	drain, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := workerControlRequest(drain, watch.client, watch.token, http.MethodDelete, path, nil, http.StatusNoContent); err == nil {
			stopWorkers(workers)
			return
		}
		select {
		case <-drain.Done():
			force, cancelForce := context.WithTimeout(ctx, time.Second)
			defer cancelForce()
			if err := workerControlRequest(force, watch.client, watch.token, http.MethodDelete, path+"?force=true", nil, http.StatusNoContent); err == nil {
				stopWorkers(workers)
			}
			return
		case <-ticker.C:
		}
	}
}

func stopWorkers(processes []*workerProcess) {
	for _, process := range processes {
		process.stop()
	}
}

func workerSourceFingerprint(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if appwalk.SkipDir(root, path) || path != root && slices.Contains([]string{"scenerycontract", "scenerygen", "target"}, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case "Cargo.toml", "Cargo.lock", "go.mod", "go.sum", ".scenery.json":
		default:
			if !slices.Contains([]string{".go", ".rs", ".scn"}, filepath.Ext(path)) {
				return nil
			}
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("worker input %s must be a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%x\n", relative, digest)
		return nil
	})
	return fmt.Sprintf("%x", hash.Sum(nil)), err
}
