package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime"
)

type workerAssistantProcess struct {
	PID         int    `json:"assistant_pid,omitempty"`
	RuntimePath string `json:"assistant_runtime_path,omitempty"`
	KeyPath     string `json:"assistant_key_path,omitempty"`
}

func startWorkerAssistants(ctx context.Context, cli, root, environment, directory string) (workerAssistantProcess, func(), error) {
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return workerAssistantProcess{}, func() {}, err
	}
	state := filepath.Join(paths.Directory, "assistants")
	key, err := ensureAssistantTokenKey(state)
	if err != nil {
		return workerAssistantProcess{}, func() {}, err
	}
	runtimePath := filepath.Join(directory, "assistant-runtime.json")
	command := exec.Command(cli, "worker-assistants", root, environment, directory, state)
	command.Dir, command.Stdout, command.Stderr = root, os.Stderr, os.Stderr
	command.Env = envpolicy.Environ()
	devprocess.ConfigureChild(command)
	if err := command.Start(); err != nil {
		return workerAssistantProcess{}, func() {}, err
	}
	waited := make(chan struct{})
	var processErr error
	go func() {
		processErr = command.Wait()
		close(waited)
	}()
	stop := func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		<-waited
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waited:
			return workerAssistantProcess{}, func() {}, fmt.Errorf("assistant supervisor exited during preparation: %v", processErr)
		case <-ctx.Done():
			stop()
			return workerAssistantProcess{}, func() {}, ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(runtimePath); err == nil {
				return workerAssistantProcess{command.Process.Pid, runtimePath, key}, stop, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				stop()
				return workerAssistantProcess{}, func() {}, err
			}
		}
	}
}

func runWorkerAssistants(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("invalid worker assistant supervisor arguments")
	}
	root, environment, directory, state := args[0], args[1], args[2], args[3]
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go watchWorkerAssistantParent(ctx, os.Getppid(), stop)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		return err
	}
	env, err := cfg.ResolveEnv(environment)
	if err != nil {
		return err
	}
	result, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return fmt.Errorf("assistant declarations are invalid")
	}
	provider, err := assistantProviderEnvFromConfig(ctx, cfg, env)
	if err != nil {
		return err
	}
	supervisor := newAssistantSupervisor(ctx, assistantSupervisorConfig{
		Root: root, StateRoot: state, ProviderEnv: provider, UseAppGateway: true,
		OnStatus: func(statuses []AssistantStatusRecord) {
			_ = writeAssistantLiveStatusSnapshot(root, result, statuses)
		},
		Output: os.Stdout, ErrOutput: os.Stderr,
	})
	defer func() { _ = supervisor.Close() }()
	if err := supervisor.Prepare(ctx, result); err != nil {
		return err
	}
	if err := runtime.WriteAssistantRuntimeConfig(filepath.Join(directory, "assistant-runtime.json"), supervisor.RuntimeConfig()); err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
				_ = supervisor.StartPrepared(ctx)
				superviseWorkerAssistantSources(ctx, supervisor, assistantDefinitionsFromResult(result, root))
				return nil
			}
		}
	}
}

func watchWorkerAssistantParent(ctx context.Context, parent int, stop context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if parent <= 1 || os.Getppid() != parent {
			stop()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func superviseWorkerAssistantSources(ctx context.Context, supervisor *assistantSupervisor, definitions []assistantDefinition) {
	digests := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		digests[definition.SourceRoot], _ = assistantOverlayInputDigest(definition.SourceRoot)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var changed []string
			for _, definition := range definitions {
				source := definition.SourceRoot
				digest, err := assistantOverlayInputDigest(source)
				if err != nil || digests[source] == digest {
					continue
				}
				digests[source] = digest
				if relative, err := filepath.Rel(supervisor.config.Root, source); err == nil {
					changed = append(changed, filepath.ToSlash(relative))
				}
			}
			supervisor.HandleChanges(ctx, changed)
		}
	}
}
