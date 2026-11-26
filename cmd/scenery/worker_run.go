package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"path/filepath"
	"strings"

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

// setProcessEnvironment publishes entries into this process's environment,
// which every worker launch the host receives inherits.
func setProcessEnvironment(environment []string) error {
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := envpolicy.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

// workerHostOptions is what a supervisor decides about one development host.
type workerHostOptions struct {
	Root        string
	Environment string
	SessionID   string
	Victoria    map[string]string
	Assistants  workerAssistantProcess
	// Held are descriptors the host keeps open for its whole lifetime, such
	// as the worktree live lock and a storage task lease.
	Held []*os.File
}

// workerHostLaunch is the private input of one development host process. Its
// Files are inherited in order from descriptor 3.
type workerHostLaunch struct {
	Executable string
	Args       []string
	Directory  string
	Files      []*os.File
	reports    *net.TCPListener
	reportFile *os.File
}

// Close releases the launch's private resources after the host exited.
func (launch *workerHostLaunch) Close() {
	if launch == nil {
		return
	}
	if launch.reportFile != nil {
		_ = launch.reportFile.Close()
	}
	if launch.reports != nil {
		_ = launch.reports.Close()
	}
	_ = os.RemoveAll(launch.Directory)
}

// newWorkerReportListener opens the loopback listener the host serves
// worker failure reports on and publishes its address to worker launches.
func newWorkerReportListener(appID, sessionID string) (*net.TCPListener, error) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	if err := setProcessEnvironment([]string{
		"SCENERY_DEV_REPORT_URL=http://" + listener.Addr().String() + "/report",
		"SCENERY_DEV_REPORT_TOKEN=" + workerRandomToken(),
		"SCENERY_APP_ID=" + appID,
		"SCENERY_SESSION_ID=" + sessionID,
	}); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// prepareWorkerHost writes the private launch plan of a development host
// serving bundle. The host rebuilds and publishes later source edits itself.
func prepareWorkerHost(ctx context.Context, bundle workerBundle, dev *workerDevelopment, reports *net.TCPListener, options workerHostOptions) (*workerHostLaunch, error) {
	executable, err := rustbuild.Host(ctx)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("/tmp", "app-workers-")
	if err != nil {
		return nil, err
	}
	launch := &workerHostLaunch{Executable: executable, Directory: directory, reports: reports}
	fail := func(err error) (*workerHostLaunch, error) {
		launch.Close()
		return nil, err
	}
	launch.reportFile, err = reports.File()
	if err != nil {
		return fail(err)
	}
	launch.Files = append([]*os.File{launch.reportFile}, options.Held...)
	rebuild, err := dev.project(bundle)
	if err != nil {
		return fail(err)
	}
	cli, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	telemetryPath, _ := cliTelemetryPath()
	config := struct {
		workerAssistantProcess
		Victoria       map[string]string `json:"victoria,omitempty"`
		TelemetryPath  string            `json:"telemetry_path"`
		App            *cliTelemetryApp  `json:"app"`
		Root           string            `json:"root"`
		CLI            string            `json:"cli"`
		CLIEnvironment []string          `json:"cli_environment"`
		Environment    string            `json:"environment"`
		FrontendPID    int               `json:"frontend_pid"`
		ReportFD       int               `json:"report_fd"`
		LockFDs        []int             `json:"lock_fds"`
		SessionID      string            `json:"session_id"`
		WaitSchema     string            `json:"wait_schema"`
		Rebuild        workerRebuildPlan `json:"rebuild"`
	}{
		workerAssistantProcess: options.Assistants, Victoria: options.Victoria,
		TelemetryPath: telemetryPath, App: telemetryInvocationAppFrom(nil, options.Root),
		Root: options.Root, CLI: cli, CLIEnvironment: envpolicy.Environ(), Environment: options.Environment,
		ReportFD: 3, LockFDs: []int{}, SessionID: options.SessionID,
		WaitSchema: newCLIPayloadIdentity(devRuntimeWaitKind).SchemaRevision, Rebuild: rebuild,
	}
	for index := range options.Held {
		config.LockFDs = append(config.LockFDs, 4+index)
	}
	plan := struct {
		Bundle   workerBundle                  `json:"bundle"`
		Snapshot *workerruntime.ConfigSnapshot `json:"snapshot"`
		Token    string                        `json:"token"`
		Config   any                           `json:"config"`
	}{bundle, bundle.config, workerRandomToken(), config}
	planPath := filepath.Join(directory, "host-input.json")
	if err := writePrivateWorkerJSON(planPath, plan); err != nil {
		return fail(err)
	}
	launch.Args = []string{"--dev", directory, planPath}
	return launch, nil
}

func workerControlClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{Protocols: workerabi.Protocols(), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
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
