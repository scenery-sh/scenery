package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/staticfrontend"
	"scenery.sh/internal/workerabi"
	workerruntime "scenery.sh/runtime"
)

type workerLink struct {
	Token        string            `json:"token"`
	Dispatch     map[string]string `json:"dispatch"`
	Capabilities string            `json:"capabilities"`
	HostState    string            `json:"host_state"`
}

type workerLaunch struct {
	WorkerSocket string             `json:"worker_socket"`
	HostSocket   string             `json:"host_socket"`
	Token        string             `json:"token"`
	Identity     workerabi.Identity `json:"identity"`
}

type workerProcess struct {
	program workerProgram
	cmd     *exec.Cmd
	done    chan struct{}
	socket  string
	token   string
}

type workerProcessIdentity struct {
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	Target                 string `json:"go_target"`
}

type workerProcessInstance struct {
	Network  string                `json:"network"`
	Address  string                `json:"address"`
	PID      int                   `json:"pid"`
	Identity workerProcessIdentity `json:"identity"`
	Service  string                `json:"worker_service,omitempty"`
	Token    string                `json:"worker_token,omitempty"`
}

func runWorkerBundleExecutable(args []string) error {
	options, err := parseDevArgs(args)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	file, err := os.Open(filepath.Join(root, "manifest.json"))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var bundle workerBundle
	decoder := json.NewDecoder(io.LimitReader(file, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return err
	}
	if bundle.Version != 1 || bundle.ContractRevision == "" || len(bundle.Programs) == 0 {
		return fmt.Errorf("invalid worker bundle")
	}
	for i, program := range bundle.Programs {
		if !filepath.IsLocal(program.Binary) {
			return fmt.Errorf("worker executable path is outside the bundle")
		}
		bundle.Programs[i].Binary = filepath.Join(root, filepath.FromSlash(program.Binary))
	}
	var frontend http.Handler
	if descriptor := bundle.Frontend; descriptor != nil {
		if !filepath.IsLocal(descriptor.Root) {
			return fmt.Errorf("frontend path is outside the bundle")
		}
		static, err := staticfrontend.Open(filepath.Join(root, filepath.FromSlash(descriptor.Root)), descriptor.Reserved)
		if err != nil {
			return err
		}
		defer func() { _ = static.Close() }()
		frontend = static
	}
	return runWorkerBundle(bundle, workerListen(options), frontend, nil, "", "")
}

func workerListen(options devOptions) string {
	if options.ListenSet {
		return options.Listen
	}
	if options.PortSet {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(options.Port))
	}
	return workerruntime.ListenAddrFromEnv()
}

func runWorkerBundle(bundle workerBundle, listen string, frontend http.Handler, onReady func(), watchRoot, initialFingerprint string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	directory, err := os.MkdirTemp("/tmp", "app-workers-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	control := filepath.Join(directory, "control.sock")
	capabilities := filepath.Join(directory, "capabilities.sock")
	state := filepath.Join(directory, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		return err
	}
	token := workerRandomToken()
	linkPath := filepath.Join(directory, "link.json")
	if err := writePrivateWorkerJSON(linkPath, workerLink{
		Token: token, Dispatch: map[string]string{"network": "unix", "address": control}, Capabilities: capabilities, HostState: state,
	}); err != nil {
		return err
	}
	if err := envpolicy.Set("SCENERY_PROCESS_LINK", linkPath); err != nil {
		return err
	}
	bundle.Host.ListenAddr = listen
	hostDone := make(chan error, 1)
	go func() {
		defer close(hostDone)
		hostDone <- workerruntime.MainWorkerProcessHost(ctx, bundle.Host, bundle.ContractRevision, bundle.Types.Normalize, frontend)
	}()
	defer func() { stop(); <-hostDone }()
	processes := make(map[string]*workerProcess, len(bundle.Programs))
	defer func() {
		for _, process := range processes {
			process.stop()
		}
	}()
	for i, program := range bundle.Programs {
		process, err := startForeignWorker(ctx, directory, capabilities, linkPath, strconv.Itoa(i+1), program)
		if err != nil {
			return err
		}
		processes[program.Process] = process
	}
	httpClient := workerControlClient(control)
	defer httpClient.CloseIdleConnections()
	if err := waitWorkerReady(ctx, func(ctx context.Context) error {
		return workerControlRequest(ctx, httpClient, token, http.MethodGet, "/__scenery/process/v1/generations", nil, http.StatusOK)
	}); err != nil {
		select {
		case hostErr := <-hostDone:
			return errors.Join(err, hostErr)
		default:
			return err
		}
	}
	if watchRoot != "" {
		actual, err := workerSourceFingerprint(watchRoot)
		if err != nil || actual != initialFingerprint {
			return errors.Join(fmt.Errorf("application changed during startup"), err)
		}
	}
	if err := publishWorkerGeneration(ctx, httpClient, token, bundle, processes, 1); err != nil {
		return err
	}
	if onReady != nil {
		onReady()
	}
	fmt.Fprintf(os.Stderr, "application ready on %s\n", listen)
	if watchRoot != "" {
		watcher := &workerWatcher{
			root: watchRoot, directory: directory, capabilities: capabilities, link: linkPath, token: token,
			client: httpClient, bundle: bundle, active: processes, generation: 1, sequence: 1,
		}
		return watcher.run(ctx, initialFingerprint, hostDone)
	}
	return <-hostDone
}

func startForeignWorker(ctx context.Context, state, capabilities, link, number string, program workerProgram) (*workerProcess, error) {
	data, err := os.ReadFile(program.Binary)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if digest != program.Digest {
		return nil, fmt.Errorf("worker %s executable digest changed", program.Service)
	}
	process := &workerProcess{program: program, socket: filepath.Join(state, "w"+number+".sock"), token: workerRandomToken(), done: make(chan struct{})}
	env := envWithoutKeys(envpolicy.Environ(), "SCENERY_LISTEN_NETWORK", "SCENERY_LISTEN_ADDR", "SCENERY_PROCESS_LINK")
	var argv []string
	if program.Runtime == "rust" {
		config := filepath.Join(state, "w"+number+".json")
		launch := workerLaunch{WorkerSocket: process.socket, HostSocket: capabilities, Token: process.token, Identity: process.workerIdentity()}
		if err := writePrivateWorkerJSON(config, launch); err != nil {
			return nil, err
		}
		argv = []string{config}
	} else {
		env = append(env, "SCENERY_LISTEN_NETWORK=unix", "SCENERY_LISTEN_ADDR="+process.socket, "SCENERY_PROCESS_LINK="+link)
	}
	process.cmd = exec.Command(program.Binary, argv...)
	process.cmd.Env = env
	process.cmd.Stdout, process.cmd.Stderr = os.Stderr, os.Stderr
	if err := process.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = process.cmd.Wait(); close(process.done) }()
	client := workerabi.NewClient(process.socket, process.token, process.workerIdentity())
	defer client.Close()
	if err := waitWorkerReady(ctx, func(ctx context.Context) error {
		if program.Runtime == "rust" {
			return client.Ready(ctx)
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", process.socket)
		if err != nil {
			return err
		}
		return connection.Close()
	}); err != nil {
		process.stop()
		return nil, fmt.Errorf("start worker %s: %w", program.Service, err)
	}
	return process, nil
}

func (process *workerProcess) workerIdentity() workerabi.Identity {
	return workerabi.Identity{
		ABI: workerabi.Version, Service: process.program.Service, ContractRevision: process.program.ContractRevision, ImplementationRevision: process.program.Digest,
	}
}

func (process *workerProcess) stop() {
	select {
	case <-process.done:
		return
	default:
	}
	_ = process.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-process.done:
	case <-timer.C:
		_ = process.cmd.Process.Kill()
		<-process.done
	}
}

func publishWorkerGeneration(ctx context.Context, client *http.Client, token string, bundle workerBundle, processes map[string]*workerProcess, number uint64) error {
	instances := make(map[string]workerProcessInstance, len(processes))
	aggregate := sha256.New()
	for _, program := range bundle.Programs {
		process := processes[program.Process]
		_, _ = io.WriteString(aggregate, program.Digest)
		instance := workerProcessInstance{
			Network: "unix", Address: process.socket, PID: process.cmd.Process.Pid,
			Identity: workerProcessIdentity{ContractRevision: program.ContractRevision, ImplementationRevision: program.Digest, BuildInputDigest: program.Digest, Target: program.Runtime},
		}
		if program.Runtime == "rust" {
			instance.Service, instance.Token = program.Service, process.token
		} else {
			instance.Identity = program.Identity
		}
		instances[program.Process] = instance
	}
	digest := fmt.Sprintf("sha256:%x", aggregate.Sum(nil))
	body, err := json.Marshal(map[string]any{
		"generation": number, "contract_revision": bundle.ContractRevision, "processes": instances, "bindings": bundle.Bindings,
		"identity": workerProcessIdentity{ContractRevision: bundle.ContractRevision, ImplementationRevision: digest, BuildInputDigest: digest, Target: "worker"},
	})
	if err != nil {
		return err
	}
	return workerControlRequest(ctx, client, token, http.MethodPut, "/__scenery/process/v1/generations", body, http.StatusNoContent)
}

func workerControlClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

func workerControlRequest(ctx context.Context, client *http.Client, token, method, path string, body []byte, expected int) error {
	request, err := http.NewRequestWithContext(ctx, method, "http://host"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		return err
	}
	if response.StatusCode != expected {
		return fmt.Errorf("worker Host HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

func waitWorkerReady(ctx context.Context, probe func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := probe(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func workerRandomToken() string {
	var secret [32]byte
	rand.Read(secret[:])
	return hex.EncodeToString(secret[:])
}

func writePrivateWorkerJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
