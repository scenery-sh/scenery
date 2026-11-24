package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/storageconfig"
	workerruntime "scenery.sh/runtime"
)

type workerSupervisor struct {
	client *http.Client
	token  string
}

type workerSupervisorStatus struct {
	Pending   *uint64                          `json:"pending"`
	Current   uint64                           `json:"current"`
	Processes map[string]workerSupervisorState `json:"processes"`
	Faults    []workerSupervisorFault          `json:"faults"`
}

type workerSupervisorState struct {
	PID    int    `json:"pid"`
	Digest string `json:"digest"`
}

type workerSupervisorFault struct {
	Sequence   uint64 `json:"sequence"`
	Generation uint64 `json:"generation"`
	Service    string `json:"service"`
	Runtime    string `json:"runtime"`
	ExitCode   *int   `json:"exit_code"`
	Signal     int    `json:"signal"`
}

type workerLaunchProgram struct {
	Service     string                `json:"service"`
	Process     string                `json:"process"`
	Binary      string                `json:"binary"`
	Digest      string                `json:"digest"`
	Runtime     string                `json:"runtime"`
	Identity    workerProcessIdentity `json:"identity"`
	Environment []string              `json:"environment"`
	Startup     json.RawMessage       `json:"startup,omitempty"`
}

func startWorkerSupervisor(executable, directory string) (<-chan error, func(), error) {
	command := exec.Command(executable, "--supervisor", directory)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	command.Env = minimalAppProcessEnv(envpolicy.Environ())
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		close(done)
	}()
	stop := func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		<-done
	}
	return done, stop, nil
}

func openWorkerSupervisor(ctx context.Context, directory string, done <-chan error) (*workerSupervisor, error) {
	client := workerControlClient(filepath.Join(directory, "supervisor.sock"))
	supervisor := &workerSupervisor{client: client}
	err := waitWorkerReady(ctx, func(ctx context.Context) error {
		data, err := os.ReadFile(filepath.Join(directory, "supervisor.token"))
		if err != nil {
			return err
		}
		supervisor.token = string(data)
		_, err = supervisor.status(ctx)
		return err
	})
	if err != nil {
		client.CloseIdleConnections()
		select {
		case exit := <-done:
			err = errors.Join(err, exit)
		default:
		}
		return nil, fmt.Errorf("start worker supervisor: %w", err)
	}
	return supervisor, nil
}

func (supervisor *workerSupervisor) request(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://host"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+supervisor.token)
	response, err := supervisor.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return fmt.Errorf("worker supervisor HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
}

func (supervisor *workerSupervisor) status(ctx context.Context) (workerSupervisorStatus, error) {
	var status workerSupervisorStatus
	err := supervisor.request(ctx, http.MethodGet, "/status", nil, &status)
	return status, err
}

func (supervisor *workerSupervisor) replace(ctx context.Context, bundle workerBundle, verify func() error) (workerSupervisorStatus, error) {
	var status workerSupervisorStatus
	input := struct {
		ContractRevision string                `json:"contract_revision"`
		Programs         []workerLaunchProgram `json:"programs"`
	}{ContractRevision: bundle.ContractRevision}
	for _, program := range bundle.Programs {
		projected, err := projectWorkerLaunch(program)
		if err != nil {
			return status, err
		}
		input.Programs = append(input.Programs, projected)
	}
	var candidate struct {
		Generation uint64 `json:"generation"`
	}
	if err := supervisor.request(ctx, http.MethodPut, "/candidate", input, &candidate); err != nil {
		return status, err
	}
	defer func() {
		abort, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = supervisor.request(abort, http.MethodDelete, "/candidate/selection", candidate, nil)
	}()
	if verify != nil {
		if err := verify(); err != nil {
			return status, err
		}
	}
	commit, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	commitErr := supervisor.request(commit, http.MethodPut, "/candidate/selection", candidate, nil)
	for {
		var statusErr error
		status, statusErr = supervisor.status(commit)
		if statusErr == nil && status.Current >= candidate.Generation && status.matches(bundle) {
			return status, nil
		}
		if statusErr == nil && (status.Pending == nil || *status.Pending != candidate.Generation) {
			return status, errors.Join(commitErr, fmt.Errorf("candidate generation %d was not published", candidate.Generation))
		}
		select {
		case <-commit.Done():
			return status, errors.Join(commitErr, statusErr, commit.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (status workerSupervisorStatus) matches(bundle workerBundle) bool {
	if len(status.Processes) != len(bundle.Programs) {
		return false
	}
	for _, program := range bundle.Programs {
		if status.Processes[program.Process].Digest != program.Digest {
			return false
		}
	}
	return true
}

func projectWorkerLaunch(program workerProgram) (workerLaunchProgram, error) {
	environment := envWithoutKeys(minimalAppProcessEnv(envpolicy.Environ()), "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR",
		"SCENERY_PROCESS_LINK", workerruntime.ConfigSnapshotFDEnv, storageconfig.RuntimeConfigEnv)
	launch := workerLaunchProgram{
		Service: program.Service, Process: program.Process, Binary: program.Binary,
		Digest: program.Digest, Runtime: program.Runtime, Identity: program.Identity,
		Environment: environment,
	}
	if program.Runtime != "go" {
		launch.Environment = envWithoutKeys(environment, appDatabaseURLEnv, postgresdb.RegistryEnv)
	}
	if compiler.IsJSRuntime(program.Runtime) {
		data, err := jsWorkerStartup(program)
		if err != nil {
			return workerLaunchProgram{}, err
		}
		launch.Startup = data
	} else if program.Runtime == "go" && program.config != nil {
		data, err := json.Marshal(program.config)
		if err != nil {
			return workerLaunchProgram{}, err
		}
		launch.Startup = data
	}
	return launch, nil
}
