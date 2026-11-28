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
	"sync"
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
	Reused   bool   `json:"reused,omitempty"`
}

type testHostSummary struct {
	URL       string `json:"url"`
	StartupMS int64  `json:"startup_ms"`
}

const defaultMaxTestTemplates = 4

type testDatabaseHarness struct {
	admin           *sql.DB
	adminURL        string
	templateName    string
	bindings        []compiler.SQLBinding
	requirements    compiler.SQLRequirements
	summary         *testDatabaseTemplateSummary
	releaseTemplate func() error
	stopServer      func() error

	// Optional hooks used by unit tests; nil defaults to postgresdb operations.
	cloneFn   func(ctx context.Context, targetName, templateName string) (time.Duration, error)
	dropFn    func(ctx context.Context, name string) error
	inspectFn func(ctx context.Context, dbName string) (*postgresdb.ResettableDatabaseState, error)
	resetFn   func(ctx context.Context, dbName string, state *postgresdb.ResettableDatabaseState) error

	mu              sync.Mutex
	closed          bool
	resetInspected  bool
	resetInspecting bool
	resetState      *postgresdb.ResettableDatabaseState
	idleDBs         []string
	pooledDBs       map[string]struct{}

	tplCond         *sync.Cond
	idleTemplates   []string
	ownedTemplates  []string
	totalTemplates  int
	templateWaiters int
	maxTemplates    int
}

func startTestDatabaseHarness(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result, baseEnv []string) (*testDatabaseHarness, error) {
	cfgs, err := resolveSQLSupply(compiled.SQLRequirements, baseEnv, true)
	if err != nil || len(cfgs) == 0 {
		return nil, err
	}
	var adminURL string
	canonicalRoot := root
	var stopAfter func() error

	if externalURL := lookupEnvValue(baseEnv, appDatabaseURLEnv); externalURL != "" {
		if err := validateAppPostgresURL(externalURL); err != nil {
			return nil, err
		}
		adminURL, err = postgresdb.DatabaseURLWithName(externalURL, "postgres")
		if err != nil {
			return nil, err
		}
	} else {
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
	templateName, hit, setupDur, bindings, releaseTemplate, err := ensureAppTemplateDatabase(ctx, canonicalRoot, cfg, compiled, admin, adminURL, baseEnv)
	if err != nil {
		_ = admin.Close()
		if stopAfter != nil {
			_ = stopAfter()
		}
		return nil, err
	}
	if templateName == "" {
		if releaseTemplate != nil {
			_ = releaseTemplate()
		}
		_ = admin.Close()
		if stopAfter != nil {
			_ = stopAfter()
		}
		return nil, nil
	}
	h := &testDatabaseHarness{
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
		releaseTemplate: releaseTemplate,
		stopServer:      stopAfter,
		maxTemplates:    defaultMaxTestTemplates,
	}
	h.initPoolLocked()
	return h, nil
}

func (h *testDatabaseHarness) initPoolLocked() {
	if h.tplCond == nil {
		h.tplCond = sync.NewCond(&h.mu)
	}
	if h.maxTemplates <= 0 {
		h.maxTemplates = defaultMaxTestTemplates
	}
	if h.totalTemplates == 0 && h.templateName != "" {
		h.idleTemplates = []string{h.templateName}
		h.totalTemplates = 1
	}
	if h.pooledDBs == nil {
		h.pooledDBs = make(map[string]struct{})
	}
}

func (h *testDatabaseHarness) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.closed = true
	toDrop := make([]string, 0, len(h.pooledDBs)+len(h.ownedTemplates))
	for name := range h.pooledDBs {
		toDrop = append(toDrop, name)
	}
	h.pooledDBs = nil
	h.idleDBs = nil
	toDrop = append(toDrop, h.ownedTemplates...)
	h.ownedTemplates = nil
	h.idleTemplates = nil
	if h.tplCond != nil {
		h.tplCond.Broadcast()
	}
	h.mu.Unlock()

	var err error
	if len(toDrop) > 0 {
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var errMu sync.Mutex
		var wg sync.WaitGroup
		for _, dbName := range toDrop {
			wg.Go(func() {
				if dropErr := h.dropDatabase(dropCtx, dbName); dropErr != nil {
					errMu.Lock()
					err = errors.Join(err, dropErr)
					errMu.Unlock()
				}
			})
		}
		wg.Wait()
	}
	if h.releaseTemplate != nil {
		err = errors.Join(err, h.releaseTemplate())
	}
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
	started := time.Now()

	if pooledName, state, ok := h.popIdleDB(); ok {
		if resetErr := h.resetDatabase(ctx, pooledName, state); resetErr == nil {
			env, envErr := h.moduleDatabaseEnv(pooledName)
			if envErr != nil {
				h.discardPooledDB(context.WithoutCancel(ctx), pooledName)
				return nil, nil, nil, envErr
			}
			return &testModuleDatabaseSummary{
				Name:     pooledName,
				Template: h.templateName,
				CloneMS:  time.Since(started).Milliseconds(),
				Reused:   true,
			}, env, h.makeCleanup(ctx, pooledName), nil
		}
		h.discardPooledDB(context.WithoutCancel(ctx), pooledName)
	}

	cloneName, err := h.randomDBName(moduleName)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := h.cloneFromPool(ctx, cloneName); err != nil {
		return nil, nil, nil, err
	}
	h.ensureResettableInspected(ctx, cloneName)

	env, err := h.moduleDatabaseEnv(cloneName)
	if err != nil {
		_ = h.dropDatabase(context.WithoutCancel(ctx), cloneName)
		return nil, nil, nil, err
	}
	return &testModuleDatabaseSummary{
		Name:     cloneName,
		Template: h.templateName,
		CloneMS:  time.Since(started).Milliseconds(),
	}, env, h.makeCleanup(ctx, cloneName), nil
}

func (h *testDatabaseHarness) randomDBName(label string) (string, error) {
	var randBytes [4]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return "", err
	}
	slug := sanitizeWorktreeName(label)
	slug = strings.ReplaceAll(slug, "-", "_")
	if len(slug) > 32 {
		slug = strings.TrimRight(slug[:32], "_")
	}
	if slug == "" {
		slug = "mod"
	}
	return fmt.Sprintf("scenery_test_%s_%s", slug, hex.EncodeToString(randBytes[:])), nil
}

func (h *testDatabaseHarness) moduleDatabaseEnv(dbName string) ([]string, error) {
	cloneURL, err := postgresdb.DatabaseURLWithName(h.adminURL, dbName)
	if err != nil {
		return nil, err
	}
	services := make([]postgresdb.Service, 0, len(h.bindings))
	for _, svc := range h.bindings {
		serviceURL, svcErr := postgresdb.ServiceURL(cloneURL, svc.Schema)
		if svcErr != nil {
			return nil, svcErr
		}
		services = append(services, postgresdb.Service{Name: svc.Name, Schema: svc.Schema, URL: serviceURL})
	}
	database := postgresdb.Database{
		Database: dbName,
		URL:      cloneURL,
		Source:   postgresdb.SourceManaged,
		Schemas:  services,
	}
	managed := postgresdb.Env(database)
	managed = append(managed, managedDatabaseSetupEnv(h.requirements, managed)...)
	return managed, nil
}

func (h *testDatabaseHarness) popIdleDB() (string, *postgresdb.ResettableDatabaseState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.idleDBs) == 0 || !h.resetInspected || h.resetState == nil || !h.resetState.Resettable {
		return "", nil, false
	}
	last := len(h.idleDBs) - 1
	name := h.idleDBs[last]
	h.idleDBs = h.idleDBs[:last]
	return name, h.resetState, true
}

func (h *testDatabaseHarness) discardPooledDB(ctx context.Context, dbName string) {
	h.mu.Lock()
	delete(h.pooledDBs, dbName)
	h.mu.Unlock()
	_ = h.dropDatabase(ctx, dbName)
}

func (h *testDatabaseHarness) makeCleanup(ctx context.Context, dbName string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			h.returnOrDropDB(context.WithoutCancel(ctx), dbName)
		})
	}
}

func (h *testDatabaseHarness) returnOrDropDB(ctx context.Context, dbName string) {
	h.mu.Lock()
	canReuse := !h.closed && h.resetInspected && h.resetState != nil && h.resetState.Resettable
	if canReuse {
		if h.pooledDBs == nil {
			h.pooledDBs = make(map[string]struct{})
		}
		h.pooledDBs[dbName] = struct{}{}
		h.idleDBs = append(h.idleDBs, dbName)
		h.mu.Unlock()
		return
	}
	delete(h.pooledDBs, dbName)
	h.mu.Unlock()
	_ = h.dropDatabase(ctx, dbName)
}

func (h *testDatabaseHarness) ensureResettableInspected(ctx context.Context, dbName string) {
	h.mu.Lock()
	if h.resetInspected || h.resetInspecting {
		h.mu.Unlock()
		return
	}
	h.resetInspecting = true
	h.mu.Unlock()

	state, err := h.inspectDatabase(ctx, dbName)

	h.mu.Lock()
	h.resetInspecting = false
	if err != nil && ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	if err != nil || state == nil {
		h.resetState = &postgresdb.ResettableDatabaseState{Resettable: false}
	} else {
		h.resetState = state
	}
	h.resetInspected = true
	h.mu.Unlock()
}

func (h *testDatabaseHarness) acquireTemplate(ctx context.Context) (string, bool, error) {
	h.mu.Lock()
	h.initPoolLocked()
	h.mu.Unlock()

	stopWake := context.AfterFunc(ctx, func() {
		h.mu.Lock()
		h.tplCond.Broadcast()
		h.mu.Unlock()
	})
	defer stopWake()

	h.mu.Lock()
	defer h.mu.Unlock()
	for len(h.idleTemplates) == 0 && !h.closed && ctx.Err() == nil {
		h.templateWaiters++
		h.tplCond.Wait()
		h.templateWaiters--
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if h.closed {
		return "", false, errors.New("test database harness closed")
	}
	last := len(h.idleTemplates) - 1
	src := h.idleTemplates[last]
	h.idleTemplates = h.idleTemplates[:last]
	shouldExpand := h.templateWaiters > 0 && h.totalTemplates < h.maxTemplates
	if shouldExpand {
		h.totalTemplates++
	}
	return src, shouldExpand, nil
}

func (h *testDatabaseHarness) releaseTemplateSlot(src string) {
	h.mu.Lock()
	if !h.closed {
		h.idleTemplates = append(h.idleTemplates, src)
		h.tplCond.Signal()
	}
	h.mu.Unlock()
}

func (h *testDatabaseHarness) registerExpandedTemplate(extraTpl string) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = h.dropDatabase(context.Background(), extraTpl)
		return
	}
	h.ownedTemplates = append(h.ownedTemplates, extraTpl)
	h.idleTemplates = append(h.idleTemplates, extraTpl)
	h.tplCond.Signal()
	h.mu.Unlock()
}

func (h *testDatabaseHarness) cancelTemplateExpansion() {
	h.mu.Lock()
	h.totalTemplates--
	h.mu.Unlock()
}

func (h *testDatabaseHarness) cloneFromPool(ctx context.Context, targetName string) (time.Duration, error) {
	started := time.Now()
	src, shouldExpand, err := h.acquireTemplate(ctx)
	if err != nil {
		return 0, err
	}
	if shouldExpand {
		extraTpl, nameErr := h.randomDBName("tpl")
		if nameErr == nil {
			if _, expErr := h.cloneDatabase(ctx, extraTpl, src); expErr == nil {
				h.registerExpandedTemplate(extraTpl)
			} else {
				h.cancelTemplateExpansion()
			}
		} else {
			h.cancelTemplateExpansion()
		}
	}
	_, err = h.cloneDatabase(ctx, targetName, src)
	h.releaseTemplateSlot(src)
	if err != nil {
		return 0, err
	}
	return time.Since(started), nil
}

func (h *testDatabaseHarness) cloneDatabase(ctx context.Context, targetName, templateName string) (time.Duration, error) {
	if h.cloneFn != nil {
		return h.cloneFn(ctx, targetName, templateName)
	}
	_, dur, err := postgresdb.CloneDatabaseFromTemplate(ctx, h.admin, targetName, templateName, true)
	return dur, err
}

func (h *testDatabaseHarness) dropDatabase(ctx context.Context, name string) error {
	if h.dropFn != nil {
		return h.dropFn(ctx, name)
	}
	return postgresdb.DropDatabase(ctx, h.admin, name)
}

func (h *testDatabaseHarness) inspectDatabase(ctx context.Context, dbName string) (*postgresdb.ResettableDatabaseState, error) {
	if h.inspectFn != nil {
		return h.inspectFn(ctx, dbName)
	}
	dbURL, err := postgresdb.DatabaseURLWithName(h.adminURL, dbName)
	if err != nil {
		return nil, err
	}
	db, err := openPostgresDatabase(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return postgresdb.InspectResettableDatabase(ctx, db)
}

func (h *testDatabaseHarness) resetDatabase(ctx context.Context, dbName string, state *postgresdb.ResettableDatabaseState) error {
	if h.resetFn != nil {
		return h.resetFn(ctx, dbName, state)
	}
	if err := postgresdb.TerminateDatabaseBackends(ctx, h.admin, dbName); err != nil {
		return err
	}
	dbURL, err := postgresdb.DatabaseURLWithName(h.adminURL, dbName)
	if err != nil {
		return err
	}
	db, err := openPostgresDatabase(ctx, dbURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return postgresdb.FastResetDatabase(ctx, db, state)
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
