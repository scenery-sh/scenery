package main

import (
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
	"scenery.sh/internal/workerabi"
	workerruntime "scenery.sh/runtime"
)

type workerProcessIdentity struct {
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	Target                 string `json:"go_target"`
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

func runWorkerDevelopment(bundle workerBundle, listen string, onReady func(), dev *workerDevelopment) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bundle.setWorkerConfig()
	lease, err := storageTaskLease(ctx, envpolicy.Environ())
	if err != nil {
		return err
	}
	if lease != nil {
		defer func() { _ = lease.Close() }()
	}
	executable, err := rustbuild.Host(ctx)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("/tmp", "app-workers-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	token := workerRandomToken()
	control := filepath.Join(directory, "control.sock")
	devControl, closeControl, err := startWorkerDevControl(dev, directory, control, token)
	if err != nil {
		return err
	}
	defer closeControl()
	planPath := filepath.Join(directory, "host-input.json")
	plan := struct {
		Bundle   workerBundle                  `json:"bundle"`
		Snapshot *workerruntime.ConfigSnapshot `json:"snapshot"`
		Token    string                        `json:"token"`
	}{Bundle: bundle, Snapshot: bundle.config, Token: token}
	if err := writePrivateWorkerJSON(planPath, plan); err != nil {
		return err
	}
	pid, hostDone, stopHost, err := startNativeWorkerHost(executable, directory, planPath, listen)
	if err != nil {
		return err
	}
	defer stopHost()
	supervisor, err := openWorkerSupervisor(ctx, directory, hostDone)
	if err != nil {
		return err
	}
	defer supervisor.client.CloseIdleConnections()
	if err := os.Remove(planPath); err != nil {
		return err
	}
	devControl.hostPID.Store(int64(pid))
	status, err := supervisor.replace(ctx, bundle, func() error { return dev.verify(dev.applied) })
	if err != nil {
		return err
	}
	if err := waitWorkerReady(ctx, func(context.Context) error {
		_, err := os.Stat(filepath.Join(directory, "ready"))
		return err
	}); err != nil {
		select {
		case hostErr := <-hostDone:
			return errors.Join(err, hostErr)
		default:
			return err
		}
	}
	devControl.publish(dev.applied, bundle, status.Current, nil)
	if onReady != nil {
		onReady()
	}
	watcher := &workerWatcher{
		app: telemetryInvocationAppFrom(nil, dev.watch.root),
		dev: dev, devControl: devControl, supervisor: supervisor, bundle: bundle, status: status,
	}
	return watcher.run(ctx, hostDone)
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
