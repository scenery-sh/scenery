package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"golang.org/x/sys/unix"
	"path/filepath"
	"scenery.sh/internal/agent"
	"strconv"
	"strings"
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

func runWorkerDevelopment(bundle workerBundle, listen string, dev *workerDevelopment, live *agent.ProcessLock, frontendPID int, environment string, telemetry *cliTelemetryInvocation) error {
	ctx := context.Background()
	lease, err := storageTaskLease(ctx, envpolicy.Environ())
	if err != nil {
		return err
	}
	executable, err := rustbuild.Host(ctx)
	if err != nil {
		return err
	}
	files := []*os.File{live.File()}
	if lease != nil {
		defer func() { _ = lease.Close() }()
		held, ok := lease.(interface{ File() *os.File })
		if !ok {
			return fmt.Errorf("storage lease does not support host handoff")
		}
		files = append(files, held.File())
	}
	directory, err := os.MkdirTemp("/tmp", "app-workers-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	reportListener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer func() { _ = reportListener.Close() }()
	reportFile, err := reportListener.File()
	if err != nil {
		return err
	}
	defer func() { _ = reportFile.Close() }()
	files = append(files, reportFile)
	token, session := workerRandomToken(), workerRandomToken()
	for key, value := range map[string]string{
		"SCENERY_DEV_REPORT_URL":   "http://" + reportListener.Addr().String() + "/report",
		"SCENERY_DEV_REPORT_TOKEN": workerRandomToken(),
		"SCENERY_APP_ID":           dev.compiled.Manifest.Application.Name,
		"SCENERY_SESSION_ID":       session,
	} {
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	cli, err := os.Executable()
	if err != nil {
		return err
	}
	rebuild, err := dev.project(bundle)
	if err != nil {
		return err
	}
	telemetryPath, _ := cliTelemetryPath()
	app := telemetryInvocationAppFrom(nil, dev.watch.root)
	var startup *cliTelemetryRecord
	if telemetry != nil && !telemetry.suppressed {
		record := telemetry.recordData(cliTelemetryMeasurementStartup, 0)
		startup = &record
	}
	endpoints := map[string]string{}
	if stack := resolveLogsVictoriaStack(ctx, dev.watch.root); stack != nil {
		for _, signal := range []string{"logs", "metrics", "traces"} {
			if endpoint := stack.Endpoint(signal); endpoint != "" {
				endpoints[signal] = endpoint
			}
		}
	}
	config := struct {
		Victoria       map[string]string   `json:"victoria,omitempty"`
		Startup        *cliTelemetryRecord `json:"startup,omitempty"`
		TelemetryPath  string              `json:"telemetry_path"`
		App            *cliTelemetryApp    `json:"app"`
		Root           string              `json:"root"`
		CLI            string              `json:"cli"`
		CLIEnvironment []string            `json:"cli_environment"`
		Environment    string              `json:"environment"`
		FrontendPID    int                 `json:"frontend_pid"`
		ReportFD       int                 `json:"report_fd"`
		LockFDs        []int               `json:"lock_fds"`
		SessionID      string              `json:"session_id"`
		WaitSchema     string              `json:"wait_schema"`
		Rebuild        workerRebuildPlan   `json:"rebuild"`
	}{
		TelemetryPath: telemetryPath, App: app, Startup: startup, Victoria: endpoints,
		Root: dev.watch.root, CLI: cli, CLIEnvironment: envpolicy.Environ(), Environment: environment,
		FrontendPID: frontendPID, ReportFD: int(reportFile.Fd()), SessionID: session,
		WaitSchema: newCLIPayloadIdentity(devRuntimeWaitKind).SchemaRevision, Rebuild: rebuild,
	}
	for _, file := range files[:len(files)-1] {
		config.LockFDs = append(config.LockFDs, int(file.Fd()))
	}
	plan := struct {
		Bundle   workerBundle                  `json:"bundle"`
		Snapshot *workerruntime.ConfigSnapshot `json:"snapshot"`
		Token    string                        `json:"token"`
		Config   any                           `json:"config"`
	}{bundle, bundle.config, token, config}
	planPath := filepath.Join(directory, "host-input.json")
	if err := writePrivateWorkerJSON(planPath, plan); err != nil {
		return err
	}
	for _, file := range files {
		fd := file.Fd()
		if _, err := unix.FcntlInt(fd, unix.F_SETFD, 0); err != nil {
			return err
		}
		defer func() { _, _ = unix.FcntlInt(fd, unix.F_SETFD, unix.FD_CLOEXEC) }()
	}
	return execProcess(executable, []string{executable, "--dev", directory, planPath, listen}, minimalAppProcessEnv(envpolicy.Environ()))
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
