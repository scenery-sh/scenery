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
	"sync"
	"syscall"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
	"scenery.sh/internal/staticfrontend"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/workerabi"
	workerruntime "scenery.sh/runtime"
)

type workerLink struct {
	Token        string            `json:"token"`
	Dispatch     map[string]string `json:"dispatch"`
	Capabilities string            `json:"capabilities"`
	HostState    string            `json:"host_state"`
}

type workerProcess struct {
	program    workerProgram
	cmd        *exec.Cmd
	done       chan struct{}
	parentPipe *os.File
	pipeOnce   sync.Once
	socket     string
	token      string
}

func (process *workerProcess) closeParentPipe() {
	if process == nil || process.parentPipe == nil {
		return
	}
	process.pipeOnce.Do(func() {
		_ = process.parentPipe.Close()
	})
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
	Protocol string                `json:"protocol"`
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
	return runWorkerBundle(bundle, workerListen(options), frontend, nil, nil)
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

func configureWorkerDevelopmentDatabase(root string, cfg app.Config, requirements compiler.SQLRequirements) error {
	environment, _, err := managedDatabaseEnv(context.Background(), root, cfg, requirements, envpolicy.Environ())
	if err != nil {
		return err
	}
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

func runWorkerBundle(bundle workerBundle, listen string, frontend http.Handler, onReady func(), dev *workerDevelopment) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerruntime.SetAppConfig(workerruntime.AppConfig{Name: bundle.Host.Name, ListenAddr: listen})
	if err := bundle.configureRuntime(dev != nil); err != nil {
		return err
	}
	var err error
	bundle.Host.WorkerStorage, err = prepareWorkerStorage(bundle.Host.Workers)
	if err != nil {
		return err
	}
	lease, err := storageTaskLease(ctx, envpolicy.Environ())
	if err != nil {
		return err
	}
	if lease != nil {
		defer func() { _ = lease.Close() }()
	}
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
	var devControl *workerDevControl
	if dev != nil {
		var closeControl func()
		devControl, closeControl, err = startWorkerDevControl(dev, directory, control, token)
		if err != nil {
			return err
		}
		defer closeControl()
		bundle.Host.DevelopmentCall = true
	}
	linkPath := filepath.Join(directory, "link.json")
	if err := writePrivateWorkerJSON(linkPath, workerLink{
		Token: token, Dispatch: map[string]string{"network": "unix", "address": control, "protocol": "h2c"}, Capabilities: capabilities, HostState: state,
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
		process, err := startBundleWorker(ctx, directory, capabilities, strconv.Itoa(i+1), program)
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
	if dev != nil {
		if err := dev.verify(dev.applied); err != nil {
			return fmt.Errorf("validate application startup: %w", err)
		}
	}
	if err := publishWorkerGeneration(ctx, httpClient, token, bundle, processes, 1); err != nil {
		return err
	}
	if dev != nil {
		pruneJSWorkers(dev.watch.root, bundle.Programs)
		devControl.publish(dev.applied, bundle, 1, nil)
	}
	if onReady != nil {
		onReady()
	}
	fmt.Fprintf(os.Stderr, "application ready on %s\n", listen)
	var app *cliTelemetryApp
	if dev != nil {
		app = telemetryInvocationAppFrom(nil, dev.watch.root)
	}
	watcher := &workerWatcher{
		app: app,
		dev: dev, devControl: devControl, directory: directory, capabilities: capabilities, token: token,
		client: httpClient, bundle: bundle, active: processes, generation: 1, sequence: 1,
	}
	return watcher.run(ctx, hostDone)
}

func workerProcessEnv(inherited []string, runtime string) []string {
	env := envWithoutKeys(
		minimalAppProcessEnv(inherited),
		"SCENERY_LISTEN_NETWORK",
		"SCENERY_LISTEN_ADDR",
		"SCENERY_PROCESS_LINK",
		workerruntime.ConfigSnapshotFDEnv,
		storageconfig.RuntimeConfigEnv,
		workerruntime.AssistantRuntimeConfigEnv,
		workerruntime.AssistantTokenKeyEnv,
		workerruntime.AssistantTokenKeyFileEnv,
	)
	if runtime != "go" {
		env = envWithoutKeys(env, appDatabaseURLEnv, postgresdb.RegistryEnv, "SCENERY_DEV_REPORT_URL", "SCENERY_DEV_REPORT_TOKEN")
	}
	return env
}

func startBundleWorker(ctx context.Context, state, capabilities, number string, program workerProgram) (*workerProcess, error) {
	data, err := os.ReadFile(program.Binary)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if digest != program.Digest {
		return nil, fmt.Errorf("worker %s executable digest changed", program.Service)
	}
	process := &workerProcess{program: program, socket: filepath.Join(state, "w"+number+".sock"), token: workerRandomToken(), done: make(chan struct{})}
	env := workerProcessEnv(envpolicy.Environ(), program.Runtime)
	parentRead, parentWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	process.parentPipe = parentWrite
	config := filepath.Join(state, "w"+number+".json")
	process.cmd = exec.Command(program.Binary, config)
	devprocess.ConfigureChild(process.cmd)
	process.cmd.Env = env
	process.cmd.Stdout, process.cmd.Stderr = os.Stderr, os.Stderr
	var privateInput *devprocess.PrivateInput
	if compiler.IsJSRuntime(program.Runtime) {
		input, err := jsWorkerStartup(program)
		if err != nil {
			_ = parentRead.Close()
			process.closeParentPipe()
			return nil, err
		}
		privateInput = input
	} else if program.Runtime == "go" && program.config != nil {
		data, err := json.Marshal(program.config)
		if err != nil {
			_ = parentRead.Close()
			process.closeParentPipe()
			return nil, err
		}
		privateInput = &devprocess.PrivateInput{Env: workerruntime.ConfigSnapshotFDEnv, Data: data}
	}
	started, abandon, err := devprocess.AttachPrivateInput(process.cmd, privateInput)
	if err != nil {
		_ = parentRead.Close()
		process.closeParentPipe()
		return nil, err
	}
	process.cmd.ExtraFiles = append(process.cmd.ExtraFiles, parentRead)
	launch := workerabi.Launch{WorkerSocket: process.socket, HostSocket: capabilities, Token: process.token, Identity: process.workerIdentity(), ParentDeathFD: 2 + len(process.cmd.ExtraFiles)}
	if err := writePrivateWorkerJSON(config, launch); err != nil {
		abandon()
		_ = parentRead.Close()
		process.closeParentPipe()
		return nil, err
	}
	if err := process.cmd.Start(); err != nil {
		abandon()
		_ = parentRead.Close()
		process.closeParentPipe()
		return nil, err
	}
	started()
	_ = parentRead.Close()
	go func() {
		_ = process.cmd.Wait()
		process.closeParentPipe()
		close(process.done)
	}()
	client := workerabi.NewClient(process.socket, process.token, process.workerIdentity())
	defer client.Close()
	if err := waitWorkerReady(ctx, client.Ready); err != nil {
		process.stop()
		return nil, fmt.Errorf("start worker %s: %w", program.Service, err)
	}
	return process, nil
}

func (process *workerProcess) workerIdentity() workerabi.Identity {
	return workerabi.Identity{
		ABI: workerabi.Version, Service: process.program.Service, ContractRevision: process.program.Identity.ContractRevision, ImplementationRevision: process.program.Identity.ImplementationRevision,
	}
}

func (process *workerProcess) stop() {
	select {
	case <-process.done:
		return
	default:
	}
	process.closeParentPipe()
	_ = devprocess.TerminateTreePID(process.cmd.Process.Pid)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-process.done:
	case <-timer.C:
		_ = devprocess.KillTree(process.cmd)
		<-process.done
	}
}

func publishWorkerGeneration(ctx context.Context, client *http.Client, token string, bundle workerBundle, processes map[string]*workerProcess, number uint64) error {
	instances := make(map[string]workerProcessInstance, len(processes))
	for _, program := range bundle.Programs {
		process := processes[program.Process]
		instance := workerProcessInstance{
			Network: "unix", Address: process.socket, Protocol: "h2c", PID: process.cmd.Process.Pid,
			Identity: program.Identity, Service: program.Service, Token: process.token,
		}
		instances[program.Process] = instance
	}
	digest := workerBundleDigest(bundle)
	body, err := json.Marshal(map[string]any{
		"generation": number, "contract_revision": bundle.ContractRevision, "processes": instances,
		"identity": workerProcessIdentity{ContractRevision: bundle.ContractRevision, ImplementationRevision: digest, BuildInputDigest: digest, Target: "worker"},
	})
	if err != nil {
		return err
	}
	return workerControlRequest(ctx, client, token, http.MethodPut, "/__scenery/process/v1/generations", body, http.StatusNoContent)
}

func workerBundleDigest(bundle workerBundle) string {
	aggregate := sha256.New()
	for _, program := range bundle.Programs {
		_, _ = io.WriteString(aggregate, program.Digest)
	}
	return fmt.Sprintf("sha256:%x", aggregate.Sum(nil))
}

func workerControlClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{Protocols: workerabi.Protocols(), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
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
	ticker := time.NewTicker(2 * time.Millisecond)
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

func jsWorkerStartup(program workerProgram) (*devprocess.PrivateInput, error) {
	startup := struct {
		Config    *workerruntime.ConfigSnapshot `json:"config,omitempty"`
		Databases map[string]string             `json:"databases"`
	}{Config: program.config, Databases: make(map[string]string)}
	for name, schema := range program.Databases {
		override, _ := envpolicy.Lookup(postgresname.ServiceDatabaseURLEnv(schema))
		base, _ := envpolicy.Lookup(appDatabaseURLEnv)
		endpoint, err := postgresdb.ResolveServiceEndpoint(schema, override, base)
		if err != nil || endpoint.URL == "" {
			return nil, fmt.Errorf("JavaScript worker %s database %s has no usable connection URL", program.Service, name)
		}
		startup.Databases[name] = endpoint.URL
	}
	data, err := json.Marshal(startup)
	if err != nil {
		return nil, err
	}
	return &devprocess.PrivateInput{Env: workerruntime.ConfigSnapshotFDEnv, Data: data}, nil
}
