package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
	"scenery.sh/internal/rustbuild"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
	workerruntime "scenery.sh/runtime"
)

type testDatabaseTemplateSummary struct {
	Name        string `json:"name"`
	TemplateHit bool   `json:"template_hit"`
	SetupMS     int64  `json:"setup_ms"`
}

type testModuleDatabaseSummary struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	CloneMS  int64  `json:"clone_ms"`
}

type testHostSummary struct {
	URL       string `json:"url"`
	StartupMS int64  `json:"startup_ms"`
}

type testDatabaseHarness struct {
	admin        *sql.DB
	adminURL     string
	templateName string
	bindings     []compiler.SQLBinding
	requirements compiler.SQLRequirements
	summary      *testDatabaseTemplateSummary
	stopServer   func() error
}

func startTestDatabaseHarness(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result, baseEnv []string) (*testDatabaseHarness, error) {
	if len(compiled.SQLRequirements) == 0 {
		return nil, nil
	}
	var adminURL string
	canonicalRoot := root
	var stopAfter func() error

	if externalURL := lookupEnvValue(baseEnv, appDatabaseURLEnv); externalURL != "" {
		if err := validateAppPostgresURL(externalURL); err != nil {
			return nil, err
		}
		var err error
		adminURL, err = postgresdb.DatabaseURLWithName(externalURL, "postgres")
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		canonicalRoot, adminURL, stopAfter, err = acquireManagedPostgresHarness(ctx, root, cfg.AppID())
		if err != nil {
			return nil, err
		}
	}

	admin, err := openPostgresAdmin(ctx, adminURL)
	if err != nil {
		if stopAfter != nil {
			_ = stopAfter()
		}
		return nil, fmt.Errorf("connect postgres admin for test branching: %w", err)
	}
	templateName, hit, setupDur, bindings, err := ensureAppTemplateDatabase(ctx, canonicalRoot, cfg, compiled, admin, adminURL, baseEnv)
	if err != nil {
		_ = admin.Close()
		if stopAfter != nil {
			_ = stopAfter()
		}
		return nil, err
	}
	if templateName == "" {
		_ = admin.Close()
		if stopAfter != nil {
			_ = stopAfter()
		}
		return nil, nil
	}
	return &testDatabaseHarness{
		admin:        admin,
		adminURL:     adminURL,
		templateName: templateName,
		bindings:     bindings,
		requirements: compiled.SQLRequirements,
		summary: &testDatabaseTemplateSummary{
			Name:        templateName,
			TemplateHit: hit,
			SetupMS:     setupDur.Milliseconds(),
		},
		stopServer: stopAfter,
	}, nil
}

func (h *testDatabaseHarness) Close() error {
	if h == nil {
		return nil
	}
	var err error
	if h.admin != nil {
		err = errors.Join(err, h.admin.Close())
	}
	if h.stopServer != nil {
		err = errors.Join(err, h.stopServer())
	}
	return err
}

func (h *testDatabaseHarness) branchForModule(ctx context.Context, moduleName string) (*testModuleDatabaseSummary, []string, func(), error) {
	if h == nil {
		return nil, nil, func() {}, nil
	}
	var randBytes [4]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return nil, nil, nil, err
	}
	slug := sanitizeWorktreeName(moduleName)
	slug = strings.ReplaceAll(slug, "-", "_")
	if slug == "" {
		slug = "mod"
	}
	if len(slug) > 32 {
		slug = strings.TrimRight(slug[:32], "_")
	}
	cloneName := fmt.Sprintf("scenery_test_%s_%s", slug, hex.EncodeToString(randBytes[:]))
	_, cloneDur, err := postgresdb.CloneDatabaseFromTemplate(ctx, h.admin, cloneName, h.templateName, true)
	if err != nil {
		return nil, nil, nil, err
	}
	cleanup := func() {
		_ = postgresdb.DropDatabase(context.WithoutCancel(ctx), h.admin, cloneName)
	}
	cloneURL, err := postgresdb.DatabaseURLWithName(h.adminURL, cloneName)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	services := make([]postgresdb.Service, 0, len(h.bindings))
	for _, svc := range h.bindings {
		serviceURL, svcErr := postgresdb.ServiceURL(cloneURL, svc.Schema)
		if svcErr != nil {
			cleanup()
			return nil, nil, nil, svcErr
		}
		services = append(services, postgresdb.Service{Name: svc.Name, Schema: svc.Schema, URL: serviceURL})
	}
	database := postgresdb.Database{
		Database: cloneName,
		URL:      cloneURL,
		Source:   postgresdb.SourceManaged,
		Schemas:  services,
	}
	managed := postgresdb.Env(database)
	managed = append(managed, managedDatabaseSetupEnv(h.requirements, managed)...)
	return &testModuleDatabaseSummary{
		Name:     cloneName,
		Template: h.templateName,
		CloneMS:  cloneDur.Milliseconds(),
	}, managed, cleanup, nil
}

type testHostBuilder struct {
	root       string
	executable string
	bundle     workerBundle
	cfg        appcfg.Config
}

func bundleNeedsConfigResolution(cfg appcfg.Config, bundle workerBundle) bool {
	if len(cfg.Envs) > 0 || len(bundle.HostConfigKeys) > 0 {
		return true
	}
	for _, program := range bundle.Programs {
		if len(program.ConfigKeys) > 0 {
			return true
		}
	}
	return false
}

func prepareTestHostBuilder(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result) (*testHostBuilder, error) {
	bundle, err := buildWorkerBundle(ctx, compiled, "development", "", false)
	if err != nil {
		return nil, err
	}
	if cfg.Name != "" && bundleNeedsConfigResolution(cfg, bundle) {
		if err := bundle.resolveDevelopmentConfig(ctx, compiled, cfg, ""); err != nil {
			return nil, err
		}
	}
	bundle.setWorkerConfig()
	executable, err := rustbuild.Host(ctx)
	if err != nil {
		return nil, err
	}
	return &testHostBuilder{
		root:       root,
		executable: executable,
		bundle:     bundle,
		cfg:        cfg,
	}, nil
}

func (b *testHostBuilder) startInstance(ctx context.Context, baseEnv []string) (*testHostSummary, []string, func(), error) {
	started := time.Now()
	runtimeDir, err := os.MkdirTemp("/tmp", "app-test-host-")
	if err != nil {
		return nil, nil, nil, err
	}
	storageDir, err := os.MkdirTemp("", "app-test-storage-")
	if err != nil {
		_ = os.RemoveAll(runtimeDir)
		return nil, nil, nil, err
	}
	hostEnv, err := withTestStorageEnv(ctx, baseEnv, b.root, b.cfg, b.bundle, storageDir)
	if err != nil {
		_ = os.RemoveAll(runtimeDir)
		_ = os.RemoveAll(storageDir)
		return nil, nil, nil, err
	}

	programs := make([]workerLaunchProgram, 0, len(b.bundle.Programs))
	for _, program := range b.bundle.Programs {
		launch, projErr := projectTestWorkerLaunch(program, hostEnv)
		if projErr != nil {
			_ = os.RemoveAll(runtimeDir)
			_ = os.RemoveAll(storageDir)
			return nil, nil, nil, projErr
		}
		programs = append(programs, launch)
	}

	planPayload := struct {
		Bundle   workerBundle                  `json:"bundle"`
		Snapshot *workerruntime.ConfigSnapshot `json:"snapshot"`
		Token    string                        `json:"token"`
		Programs []workerLaunchProgram         `json:"programs"`
	}{
		Bundle:   b.bundle,
		Snapshot: b.bundle.config,
		Token:    workerRandomToken(),
		Programs: programs,
	}
	planPath := filepath.Join(runtimeDir, "test-input.json")
	if err := writePrivateWorkerJSON(planPath, planPayload); err != nil {
		_ = os.RemoveAll(runtimeDir)
		_ = os.RemoveAll(storageDir)
		return nil, nil, nil, err
	}

	cmd := exec.CommandContext(ctx, b.executable, "--test", runtimeDir, planPath, "127.0.0.1:0")
	cmd.Env = hostEnv
	var stderr tailOutput
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(runtimeDir)
		_ = os.RemoveAll(storageDir)
		return nil, nil, nil, fmt.Errorf("start test host: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		_ = os.RemoveAll(runtimeDir)
		_ = os.RemoveAll(storageDir)
	}

	readyPath := filepath.Join(runtimeDir, "ready")
	deadline := time.Now().Add(15 * time.Second)
	var address string
	for time.Now().Before(deadline) {
		if raw, readErr := os.ReadFile(readyPath); readErr == nil {
			if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
				address = trimmed
				break
			}
		}
		select {
		case waitErr := <-done:
			_ = os.RemoveAll(runtimeDir)
			_ = os.RemoveAll(storageDir)
			return nil, nil, nil, fmt.Errorf("test host exited before ready: %v\n%s", waitErr, bytes.TrimSpace(stderr))
		case <-ctx.Done():
			stop()
			return nil, nil, nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if address == "" {
		stop()
		return nil, nil, nil, fmt.Errorf("timed out waiting for test host readiness\n%s", bytes.TrimSpace(stderr))
	}

	apiURL := "http://" + address
	outEnv := setOrAppendEnv(hostEnv, "SCENERY_API_BASE_URL", apiURL)
	return &testHostSummary{
		URL:       apiURL,
		StartupMS: time.Since(started).Milliseconds(),
	}, outEnv, stop, nil
}

func withTestStorageEnv(ctx context.Context, baseEnv []string, appRoot string, cfg appcfg.Config, bundle workerBundle, storageDir string) ([]string, error) {
	storePolicies := make(map[string]appcfg.StorageStoreConfig, len(cfg.Storage.Stores))
	for name, store := range cfg.Storage.Stores {
		storePolicies[name] = store
	}
	for _, worker := range bundle.Host.Workers {
		for _, storeName := range worker.Stores {
			if _, exists := storePolicies[storeName]; !exists && storeName != "" {
				storePolicies[storeName] = appcfg.StorageStoreConfig{Kind: "local"}
			}
		}
	}
	if len(storePolicies) == 0 {
		return slicesCloneEnv(baseEnv), nil
	}
	if err := os.Chmod(storageDir, 0o700); err != nil {
		return nil, err
	}
	canonicalStorageDir, err := filepath.EvalSymlinks(storageDir)
	if err != nil {
		return nil, err
	}
	canonicalAppRoot := canonicalStorageDir
	if strings.TrimSpace(appRoot) != "" {
		if resolved, evalErr := filepath.EvalSymlinks(appRoot); evalErr == nil {
			canonicalAppRoot = resolved
		}
	}
	namespaceRoot := filepath.Join(canonicalStorageDir, "namespace")
	binding := storagefs.Binding{
		AppID:   firstNonEmpty(cfg.AppID(), bundle.Host.Name, "test-app"),
		AppRoot: canonicalAppRoot,
		UserID:  os.Getuid(),
		Managed: false,
	}
	namespace, err := storagefs.AllocateExternal(ctx, namespaceRoot, binding)
	if err != nil {
		return nil, err
	}
	stores := make(map[string]storageconfig.RuntimeStoreConfig, len(storePolicies))
	for name, store := range storePolicies {
		stores[name] = storageconfig.RuntimeStoreConfig{
			Kind:           "local",
			Root:           namespace.Path,
			Access:         strings.TrimSpace(store.Access),
			TenantScoped:   store.TenantScoped,
			MaxObjectBytes: store.MaxObjectBytes,
		}
	}
	runtimeCfg := storageconfig.RuntimeConfig{
		ArtifactIdentity: storageconfig.NewRuntimeIdentity(),
		Namespace: &storageconfig.Namespace{
			Root:        namespace.Path,
			Binding:     binding,
			Incarnation: namespace.Incarnation,
		},
		Default: strings.TrimSpace(cfg.Storage.Default),
		Stores:  stores,
	}
	if runtimeCfg.Default == "" && len(stores) == 1 {
		for name := range stores {
			runtimeCfg.Default = name
		}
	}
	encoded, err := json.Marshal(runtimeCfg)
	if err != nil {
		return nil, err
	}
	return setOrAppendEnv(baseEnv, storageconfig.RuntimeConfigEnv, string(encoded)), nil
}

func projectTestWorkerLaunch(program workerProgram, env []string) (workerLaunchProgram, error) {
	environment := workerProcessEnv(env, program.Runtime)
	launch := workerLaunchProgram{
		Service:     program.Service,
		Process:     program.Process,
		Binary:      program.Binary,
		Digest:      program.Digest,
		Runtime:     program.Runtime,
		Identity:    program.Identity,
		Environment: environment,
	}
	if compiler.IsJSRuntime(program.Runtime) || compiler.IsPythonRuntime(program.Runtime) {
		startup := struct {
			Config    *workerruntime.ConfigSnapshot `json:"config,omitempty"`
			Databases map[string]string             `json:"databases"`
		}{Config: program.config, Databases: make(map[string]string)}
		base := lookupEnvValue(env, appDatabaseURLEnv)
		for name, schema := range program.Databases {
			override := lookupEnvValue(env, postgresname.ServiceDatabaseURLEnv(schema))
			endpoint, err := postgresdb.ResolveServiceEndpoint(schema, override, base)
			if err != nil || endpoint.URL == "" {
				return workerLaunchProgram{}, fmt.Errorf("%s worker %s database %s has no usable connection URL", program.Runtime, program.Service, name)
			}
			startup.Databases[name] = endpoint.URL
		}
		data, err := json.Marshal(startup)
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

func setOrAppendEnv(env []string, key, value string) []string {
	out := envWithoutKeys(slicesCloneEnv(env), key)
	return append(out, key+"="+value)
}

func mergeEnvEntries(base, extra []string) []string {
	out := slicesCloneEnv(base)
	for _, entry := range extra {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			out = setOrAppendEnv(out, k, v)
		}
	}
	return out
}

func slicesCloneEnv(env []string) []string {
	out := make([]string, len(env))
	copy(out, env)
	return out
}
