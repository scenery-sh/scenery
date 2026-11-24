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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
	"scenery.sh/internal/rustbuild"
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

type workerProcessIdentity struct {
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	Target                 string `json:"go_target"`
}

func runWorkerBundleExecutable(args []string) error {
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		return fmt.Errorf("worker gateway requires a private runtime directory")
	}
	directory := args[0]
	options, err := parseDevArgs(args[1:])
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
	return runWorkerBundle(bundle, workerListen(options), frontend, nil, nil, directory)
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

func runWorkerBundle(bundle workerBundle, listen string, frontend http.Handler, onReady func(), dev *workerDevelopment, directory string) error {
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
	var supervisorDone <-chan error
	if directory == "" {
		executable, err := rustbuild.Host(ctx)
		if err != nil {
			return err
		}
		directory, err = os.MkdirTemp("/tmp", "app-workers-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(directory) }()
		done, stopSupervisor, err := startWorkerSupervisor(executable, directory)
		if err != nil {
			return err
		}
		defer stopSupervisor()
		supervisorDone = done
	}
	supervisor, err := openWorkerSupervisor(ctx, directory, supervisorDone)
	if err != nil {
		return err
	}
	defer supervisor.client.CloseIdleConnections()
	control := filepath.Join(directory, "control.sock")
	capabilities := filepath.Join(directory, "capabilities.sock")
	state := filepath.Join(directory, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		return err
	}
	token := supervisor.token
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
	var verify func() error
	if dev != nil {
		verify = func() error { return dev.verify(dev.applied) }
	}
	status, err := supervisor.replace(ctx, bundle, verify)
	if err != nil {
		return err
	}
	if devControl != nil {
		devControl.publish(dev.applied, bundle, status.Current, nil)
	}
	if onReady != nil {
		onReady()
	}
	fmt.Fprintf(os.Stderr, "application ready on %s\n", listen)
	if dev == nil {
		return <-hostDone
	}
	watcher := &workerWatcher{
		app: telemetryInvocationAppFrom(nil, dev.watch.root),
		dev: dev, devControl: devControl, supervisor: supervisor, bundle: bundle, status: status,
	}
	return watcher.run(ctx, hostDone, supervisorDone)
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

func jsWorkerStartup(program workerProgram) (json.RawMessage, error) {
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
	return json.Marshal(startup)
}
