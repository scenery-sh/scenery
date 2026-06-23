package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

type fakeDoctorResourceProbe struct {
	runtime doctorRuntimeInfo
	memory  doctorMemoryInfo
	memErr  error
	disks   map[string]doctorDiskInfo
	diskErr error
}

func (p fakeDoctorResourceProbe) Runtime() doctorRuntimeInfo {
	if p.runtime.GOOS == "" {
		return doctorRuntimeInfo{GOOS: "linux", GOARCH: "amd64", NumCPU: 4}
	}
	return p.runtime
}

func (p fakeDoctorResourceProbe) Memory(context.Context) (doctorMemoryInfo, error) {
	if p.memErr != nil {
		return doctorMemoryInfo{}, p.memErr
	}
	if p.memory.TotalBytes == 0 {
		return doctorMemoryInfo{TotalBytes: 8 * 1024 * 1024 * 1024}, nil
	}
	return p.memory, nil
}

func (p fakeDoctorResourceProbe) Disk(_ context.Context, path string) (doctorDiskInfo, error) {
	if p.diskErr != nil {
		return doctorDiskInfo{}, p.diskErr
	}
	if disk, ok := p.disks[path]; ok {
		return disk, nil
	}
	abs, _ := filepath.Abs(path)
	return doctorDiskInfo{Path: abs, FreeBytes: 10 * 1024 * 1024 * 1024, TotalBytes: 20 * 1024 * 1024 * 1024}, nil
}

func fakeDoctorDeps(t *testing.T) doctorProbeDeps {
	t.Helper()
	agentHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(agentHome, "agent", "postgres"), 0o755); err != nil {
		t.Fatalf("mkdir fake agent home: %v", err)
	}
	tools := map[string]string{
		"go":     "/bin/go",
		"bun":    "/bin/bun",
		"docker": "/bin/docker",
		"atlas":  "/bin/atlas",
		"sqlc":   "/bin/sqlc",
		"git":    "/bin/git",
	}
	versions := map[string]string{
		"go version":                      "go version go1.26.3 linux/amd64",
		"bun --version":                   "1.2.3",
		"docker info --format {{json .}}": `{"ServerVersion":"29.0.0","OperatingSystem":"Docker Desktop","OSType":"linux","Architecture":"aarch64","NCPU":8,"MemTotal":8589934592,"DockerRootDir":"/var/lib/docker","Driver":"overlay2","CgroupVersion":"2","KernelVersion":"6.10.14-linuxkit","Name":"docker-desktop"}`,
		"docker context show":             "desktop-linux",
		"atlas version":                   "atlas version v0.38.0",
		"sqlc version":                    "v1.30.0",
		"git --version":                   "git version 2.52.0",
	}
	return doctorProbeDeps{
		LookPath: func(file string) (string, error) {
			if path, ok := tools[file]; ok {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		RunCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
			key := filepath.Base(name) + " " + strings.Join(args, " ")
			if out, ok := versions[key]; ok {
				return []byte(out), nil
			}
			return nil, errors.New("unexpected command " + key)
		},
		ResourceProbe: fakeDoctorResourceProbe{},
		Getwd:         func() (string, error) { return "/workspace", nil },
		CacheRoot:     func() (string, error) { return "/cache/scenery", nil },
		AgentHome:     func() (string, error) { return agentHome, nil },
		DiscoverApp: func(start string) (doctorAppInfo, appcfg.Config, bool, error) {
			return doctorAppInfo{}, appcfg.Config{}, false, errors.New("no app")
		},
	}
}

func TestParseDoctorArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseDoctorArgs([]string{"--app-root", "/tmp/app", "--json"})
	if err != nil {
		t.Fatalf("parseDoctorArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDoctorArgs([]string{"--app-root"}); err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("missing --app-root error = %v", err)
	}
	if _, err := parseDoctorArgs([]string{"--bad"}); err == nil || err.Error() != `unknown flag "--bad"` {
		t.Fatalf("unknown flag error = %v", err)
	}
}

func TestRunSceneryDoctorJSONReportsRequiredFailure(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	var out bytes.Buffer
	err := runSceneryDoctorWithDeps(context.Background(), &out, []string{"--json"}, deps)
	if _, ok := errors.AsType[*silentCLIError](err); !ok {
		t.Fatalf("expected silent error, got %v", err)
	}
	var payload doctorResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != doctorSchemaVersion || payload.OK || payload.Summary.Errors != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	goCheck := doctorCheckByID(payload.Checks, "tool.go")
	if goCheck.Status != doctorStatusError || goCheck.Severity != doctorSeverityRequired {
		t.Fatalf("go check = %+v", goCheck)
	}
}

func TestRunSceneryDoctorDiscoversAppSensitiveChecks(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.Getwd = func() (string, error) { return "/apps/demo", nil }
	deps.DiscoverApp = func(start string) (doctorAppInfo, appcfg.Config, bool, error) {
		if start != "/apps/demo" {
			t.Fatalf("discover start = %q", start)
		}
		cfg := appcfg.Config{
			Name: "demo",
			ID:   "demo-id",
			Proxy: appcfg.ProxyConfig{Frontends: map[string]appcfg.FrontendConfig{
				"web": {Host: "web.demo.localhost"},
			}},
			Temporal: appcfg.TemporalConfig{Enabled: true, TypeScript: appcfg.TemporalTypeScript{Enabled: true}},
			Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
				"postgres": {Kind: "postgres", Image: "postgres:18"},
			}},
			Generators: appcfg.GeneratorsConfig{SQLC: appcfg.SQLCGeneratorConfig{
				Schemas: []appcfg.SQLCGeneratorSchema{{AtlasSource: "db/schema.hcl", SQLCSchema: "db/schema.sql"}},
			}},
		}
		return doctorAppInfo{Root: "/apps/demo", ConfigPath: "/apps/demo/.scenery.json", Name: "demo", ID: "demo-id"}, cfg, true, nil
	}

	var out bytes.Buffer
	if err := runSceneryDoctorWithDeps(context.Background(), &out, []string{"--json"}, deps); err != nil {
		t.Fatalf("runSceneryDoctorWithDeps: %v\n%s", err, out.String())
	}
	var payload doctorResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.App == nil || payload.App.Name != "demo" || payload.App.ID != "demo-id" {
		t.Fatalf("app = %+v", payload.App)
	}
	for _, id := range []string{"tool.bun", "tool.atlas", "tool.sqlc"} {
		if got := doctorCheckByID(payload.Checks, id); got.ID == "" || got.Status != doctorStatusOK {
			t.Fatalf("%s check = %+v", id, got)
		}
	}
	dockerContext := doctorCheckByID(payload.Checks, "docker.context")
	if dockerContext.Status != doctorStatusOK || dockerContext.Observed["context"] != "desktop-linux" {
		t.Fatalf("docker.context check = %+v", dockerContext)
	}
	engine := doctorCheckByID(payload.Checks, "docker.engine")
	if engine.Status != doctorStatusOK || engine.Observed["server_version"] != "29.0.0" {
		t.Fatalf("docker.engine check = %+v", engine)
	}
}

func TestRunSceneryDoctorWarnsWhenDockerEngineUnavailable(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.RunCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := filepath.Base(name) + " " + strings.Join(args, " ")
		switch key {
		case "go version":
			return []byte("go version go1.26.3 linux/amd64"), nil
		case "bun --version":
			return []byte("1.2.3"), nil
		case "docker info --format {{json .}}":
			return []byte("Cannot connect to the Docker daemon"), errors.New("daemon unavailable")
		case "docker context show":
			return []byte("desktop-linux"), nil
		case "atlas version":
			return []byte("atlas version v0.38.0"), nil
		case "sqlc version":
			return []byte("v1.30.0"), nil
		case "git --version":
			return []byte("git version 2.52.0"), nil
		default:
			return nil, errors.New("unexpected command " + key)
		}
	}

	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	dockerContext := doctorCheckByID(resp.Checks, "docker.context")
	if dockerContext.Status != doctorStatusOK || dockerContext.Observed["context"] != "desktop-linux" {
		t.Fatalf("docker.context check = %+v", dockerContext)
	}
	engine := doctorCheckByID(resp.Checks, "docker.engine")
	if engine.Status != doctorStatusWarn || !strings.Contains(engine.Message, "engine is not reachable") || engine.Observed["error_output"] == "" {
		t.Fatalf("docker.engine check = %+v", engine)
	}
}

func TestRunSceneryDoctorWarnsWhenDockerContextUnavailable(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.RunCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := filepath.Base(name) + " " + strings.Join(args, " ")
		switch key {
		case "go version":
			return []byte("go version go1.26.3 linux/amd64"), nil
		case "bun --version":
			return []byte("1.2.3"), nil
		case "docker info --format {{json .}}":
			return []byte(`{"ServerVersion":"29.0.0"}`), nil
		case "docker context show":
			return []byte("context store is corrupt"), errors.New("context unavailable")
		case "git --version":
			return []byte("git version 2.52.0"), nil
		default:
			return nil, errors.New("unexpected command " + key)
		}
	}

	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	dockerContext := doctorCheckByID(resp.Checks, "docker.context")
	if dockerContext.Status != doctorStatusWarn || !strings.Contains(dockerContext.Message, "context could not be determined") || dockerContext.Observed["error_output"] == "" {
		t.Fatalf("docker.context check = %+v", dockerContext)
	}
	engine := doctorCheckByID(resp.Checks, "docker.engine")
	if engine.Status != doctorStatusOK || engine.Observed["server_version"] != "29.0.0" {
		t.Fatalf("docker.engine check = %+v", engine)
	}
}

func TestRunSceneryDoctorExplicitAppRootFailureIsError(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.DiscoverApp = func(string) (doctorAppInfo, appcfg.Config, bool, error) {
		return doctorAppInfo{}, appcfg.Config{}, false, errors.New("no .scenery.json found")
	}
	var out bytes.Buffer
	err := runSceneryDoctorWithDeps(context.Background(), &out, []string{"--app-root", "/missing", "--json"}, deps)
	if _, ok := errors.AsType[*silentCLIError](err); !ok {
		t.Fatalf("expected silent error, got %v", err)
	}
	var payload doctorResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	appCheck := doctorCheckByID(payload.Checks, "app.root")
	if appCheck.Status != doctorStatusError || appCheck.Severity != doctorSeverityRequired {
		t.Fatalf("app.root check = %+v", appCheck)
	}
}

func TestDoctorResourceThresholds(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	deps.ResourceProbe = fakeDoctorResourceProbe{
		memory: doctorMemoryInfo{TotalBytes: 1536 * 1024 * 1024},
		disks: map[string]doctorDiskInfo{
			"/workspace":     {Path: "/workspace", FreeBytes: 3 * 1024 * 1024 * 1024, TotalBytes: 20 * 1024 * 1024 * 1024},
			"/cache/scenery": {Path: "/cache/scenery", FreeBytes: 700 * 1024 * 1024, TotalBytes: 20 * 1024 * 1024 * 1024},
		},
	}
	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	if got := doctorCheckByID(resp.Checks, "resource.memory"); got.Status != doctorStatusError {
		t.Fatalf("memory check = %+v", got)
	}
	if got := doctorCheckByID(resp.Checks, "resource.disk.cwd"); got.Status != doctorStatusWarn {
		t.Fatalf("cwd disk check = %+v", got)
	}
	if got := doctorCheckByID(resp.Checks, "resource.disk.cache_root"); got.Status != doctorStatusError {
		t.Fatalf("cache disk check = %+v", got)
	}
}

func TestRunSceneryDoctorWarnsWhenManagedZeroFSBinaryMissing(t *testing.T) {
	t.Setenv(devZeroFSBinEnv, "")
	deps := fakeDoctorDeps(t)
	deps.Getwd = func() (string, error) { return "/apps/storage", nil }
	deps.DiscoverApp = func(start string) (doctorAppInfo, appcfg.Config, bool, error) {
		return doctorAppInfo{Root: start, ConfigPath: filepath.Join(start, ".scenery.json"), Name: "storage"}, appcfg.Config{
			Name: "storage",
			Storage: appcfg.StorageConfig{Stores: map[string]appcfg.StorageStoreConfig{
				"app": {Kind: "zerofs"},
			}},
			Dev: appcfg.DevConfig{Services: map[string]appcfg.DevServiceConfig{
				"storage": {Kind: "zerofs"},
			}},
		}, true, nil
	}

	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	check := doctorCheckByID(resp.Checks, "storage.zerofs_binary")
	if check.Status != doctorStatusWarn || !strings.Contains(check.Message, devZeroFSBinEnv) {
		t.Fatalf("zerofs doctor check = %+v", check)
	}
}

func TestDoctorReportsSceneryAndPostgresStorageSizes(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	agentHome := t.TempDir()
	deps.AgentHome = func() (string, error) { return agentHome, nil }
	writeTestAppFile(t, agentHome, "agent/postgres/branches.json", strings.Repeat("p", 2048))
	writeTestAppFile(t, agentHome, "agent/postgres/cell.json", strings.Repeat("m", 1024))
	writeTestAppFile(t, agentHome, "agent/agent.log", strings.Repeat("l", 512))

	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	home := doctorCheckByID(resp.Checks, "storage.scenery_home")
	if home.Status != doctorStatusOK || home.Observed["size_bytes"] == nil || !strings.Contains(home.Message, agentHome) {
		t.Fatalf("storage.scenery_home check = %+v", home)
	}
	postgres := doctorCheckByID(resp.Checks, "storage.postgres_database")
	if postgres.Status != doctorStatusOK || postgres.Observed["size_bytes"] != uint64(3072) || !strings.Contains(postgres.Message, filepath.Join(agentHome, "agent", "postgres")) {
		t.Fatalf("storage.postgres_database check = %+v", postgres)
	}
}

func TestDoctorSkipsMissingPostgresStorageSize(t *testing.T) {
	t.Parallel()

	deps := fakeDoctorDeps(t)
	agentHome := t.TempDir()
	deps.AgentHome = func() (string, error) { return agentHome, nil }

	resp := buildDoctorResponse(context.Background(), doctorOptions{}, deps)
	postgres := doctorCheckByID(resp.Checks, "storage.postgres_database")
	if postgres.Status != doctorStatusSkipped || !strings.Contains(postgres.Message, "not present") {
		t.Fatalf("storage.postgres_database check = %+v", postgres)
	}
}

func TestGoToolchainVersionParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		output string
		want   doctorGoVersion
		ok     bool
	}{
		{output: "go version go1.26.3 darwin/arm64", want: doctorGoVersion{Major: 1, Minor: 26, Patch: 3}, ok: true},
		{output: "go version go1.27 linux/amd64", want: doctorGoVersion{Major: 1, Minor: 27}, ok: true},
		{output: "go version devel go1.28-abc linux/amd64", want: doctorGoVersion{Major: 1, Minor: 28}, ok: true},
		{output: "not go", ok: false},
	}
	for _, tt := range tests {
		got, ok := parseGoToolchainVersion(tt.output)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("parseGoToolchainVersion(%q) = %+v,%v want %+v,%v", tt.output, got, ok, tt.want, tt.ok)
		}
	}
	if (doctorGoVersion{Major: 1, Minor: 25, Patch: 9}).compare(doctorGoVersion{Major: 1, Minor: 26}) >= 0 {
		t.Fatal("old Go version compared as supported")
	}
}

func TestDoctorTextRendering(t *testing.T) {
	t.Parallel()

	resp := doctorResponse{
		SchemaVersion: doctorSchemaVersion,
		OK:            true,
		Summary:       doctorSummary{OK: 1, Warnings: 1},
		Checks: []doctorCheck{
			{ID: "os.runtime", Status: doctorStatusOK, Message: "linux/amd64"},
			{ID: "tool.bun", Status: doctorStatusWarn, Message: "bun not found"},
		},
	}
	var out bytes.Buffer
	if err := writeDoctorText(&out, resp); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"scenery doctor", "ok      os.runtime", "warn    tool.bun", "summary: 1 ok, 1 warnings, 0 errors, 0 skipped"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
}

func TestDoctorSchemaValidatesSyntheticPayload(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.doctor.result.v1.schema.json")
	payload := buildHarnessDoctorSchemaPayload(buildVersionResponse())
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
		t.Fatalf("doctor schema diagnostics = %+v", diagnostics)
	}
}

func doctorCheckByID(checks []doctorCheck, id string) doctorCheck {
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	return doctorCheck{}
}
