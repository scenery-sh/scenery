package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"scenery.sh/runtime/shared"
)

func TestInitializeServicesRunsInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restore := replaceGlobalRegistryForTest()
		defer restore()

		started := make(chan string, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		errCh := make(chan error, 1)

		blockingInit := func(name string) func(context.Context) error {
			return func(context.Context) error {
				started <- name
				<-release
				return nil
			}
		}
		if err := RegisterNativeService(NativeServiceRegistration{Address: "zeta", Initialize: blockingInit("zeta")}); err != nil {
			t.Fatal(err)
		}
		if err := RegisterNativeService(NativeServiceRegistration{Address: "alpha", Initialize: blockingInit("alpha")}); err != nil {
			t.Fatal(err)
		}

		go func() {
			errCh <- InitializeServices()
		}()

		synctest.Wait()
		seen := map[string]bool{}
		for len(started) > 0 {
			name := <-started
			seen[name] = true
		}
		if !seen["alpha"] || !seen["zeta"] {
			t.Fatalf("InitializeServices() started = %v, want both services", seen)
		}

		releaseOnce.Do(func() { close(release) })
		if err := <-errCh; err != nil {
			t.Fatalf("InitializeServices() error = %v", err)
		}
	})
}

func TestInitializeServicesPropagatesErrors(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	if err := RegisterNativeService(NativeServiceRegistration{Address: "service", Initialize: func(context.Context) error {
		return errors.New("boom")
	}}); err != nil {
		t.Fatal(err)
	}

	err := InitializeServices()
	if err == nil || err.Error() != "initialize service service: boom" {
		t.Fatalf("InitializeServices() error = %v, want initialize service service: boom", err)
	}
}

func TestInitializeNativeServicesRespectsDependencies(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	var mu sync.Mutex
	var calls []string
	register := func(address string, dependencies ...string) {
		t.Helper()
		if err := RegisterNativeService(NativeServiceRegistration{Address: address, Dependencies: dependencies, Initialize: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, address)
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	register("database")
	register("house", "database")
	if err := InitializeServices(); err != nil {
		t.Fatal(err)
	}
	if got, want := calls, []string{"database", "house"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("initialization calls = %v, want %v", got, want)
	}
}

func TestInitializeNativeServicesRejectsMissingDependencyAndCycle(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	if err := RegisterNativeService(NativeServiceRegistration{Address: "house", Dependencies: []string{"database"}, Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeServices(); err == nil {
		t.Fatal("missing dependency initialized")
	}
	restore()

	restore = replaceGlobalRegistryForTest()
	defer restore()
	for _, registration := range []NativeServiceRegistration{
		{Address: "house", Dependencies: []string{"audit"}, Initialize: func(context.Context) error { return nil }},
		{Address: "audit", Dependencies: []string{"house"}, Initialize: func(context.Context) error { return nil }},
	} {
		if err := RegisterNativeService(registration); err != nil {
			t.Fatal(err)
		}
	}
	if err := InitializeServices(); err == nil || err.Error() != "initialize services: dependency cycle" {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestNativeServiceShutdownErrorsAreAggregated(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	if err := RegisterNativeService(NativeServiceRegistration{
		Address: "house", Initialize: func(context.Context) error { return nil },
		Shutdown: func(context.Context) error { return errors.New("stop failed") },
	}); err != nil {
		t.Fatal(err)
	}
	if err := InitializeServices(); err != nil {
		t.Fatal(err)
	}
	if err := ShutdownServices(context.Background()); err == nil || err.Error() != "shutdown service house: stop failed" {
		t.Fatalf("shutdown error = %v", err)
	}
}

func TestShutdownServicesRunsInReverseInitializerOrder(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	for _, address := range []string{"alpha", "zeta"} {
		if err := RegisterNativeService(NativeServiceRegistration{Address: address, Initialize: func(context.Context) error { return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	if err := InitializeServices(); err != nil {
		t.Fatalf("InitializeServices() error = %v", err)
	}

	var mu sync.Mutex
	var calls []string
	MarkServiceInitialized("alpha", func(context.Context) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, "alpha")
	})
	MarkServiceInitialized("zeta", func(context.Context) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, "zeta")
	})

	if err := ShutdownServices(context.Background()); err != nil {
		t.Fatalf("ShutdownServices() error = %v", err)
	}

	got := append([]string(nil), calls...)
	want := []string{"zeta", "alpha"}
	if len(got) != len(want) {
		t.Fatalf("ShutdownServices() calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ShutdownServices() calls = %v, want %v", got, want)
		}
	}
}

func TestDefaultEnvironmentUsesTestMode(t *testing.T) {
	t.Setenv("SCENERY_RUNTIME_ENV", "test")
	env := defaultEnvironment()
	if env.Name != "test" {
		t.Fatalf("defaultEnvironment().Name = %q, want %q", env.Name, "test")
	}
	if env.Type != shared.EnvTest {
		t.Fatalf("defaultEnvironment().Type = %q, want %q", env.Type, shared.EnvTest)
	}
	if env.Cloud != shared.CloudLocal {
		t.Fatalf("defaultEnvironment().Cloud = %q, want %q", env.Cloud, shared.CloudLocal)
	}
}

func TestSetAppConfigUsesTestEnvironment(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	t.Setenv("SCENERY_RUNTIME_ENV", "test")
	SetAppConfig(AppConfig{Name: "testapp", ListenAddr: "127.0.0.1:4000"})
	meta := Meta()
	if meta.Environment.Type != shared.EnvTest {
		t.Fatalf("Meta().Environment.Type = %q, want %q", meta.Environment.Type, shared.EnvTest)
	}
	if meta.Environment.Name != "test" {
		t.Fatalf("Meta().Environment.Name = %q, want %q", meta.Environment.Name, "test")
	}
}

func TestSetAppConfigUsesSessionIdentityEnv(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	t.Setenv("SCENERY_BASE_APP_ID", "demo")
	t.Setenv("SCENERY_RUNTIME_APP_ID", "demo--feature-a")
	t.Setenv("SCENERY_SESSION_ID", "feature-a-123abc")
	SetAppConfig(AppConfig{Name: "demo", ListenAddr: "127.0.0.1:4000"})
	meta := Meta()
	if meta.AppID != "demo" {
		t.Fatalf("Meta().AppID = %q, want source app id", meta.AppID)
	}
	if meta.BaseAppID != "demo" {
		t.Fatalf("Meta().BaseAppID = %q, want demo", meta.BaseAppID)
	}
	if meta.RuntimeAppID != "demo--feature-a" {
		t.Fatalf("Meta().RuntimeAppID = %q, want demo--feature-a", meta.RuntimeAppID)
	}
	if meta.SessionID != "feature-a-123abc" {
		t.Fatalf("Meta().SessionID = %q, want feature-a-123abc", meta.SessionID)
	}
}

func replaceGlobalRegistryForTest() func() {
	prev := global
	global = &registry{
		endpoints:                 make(map[string]*Endpoint),
		cronJobs:                  make(map[string]*CronJob),
		durableTasks:              make(map[string]*DurableTask),
		contractDurableExecutions: make(map[string]ContractDurableRegistration),
		contractBindings:          make(map[string]ContractInternalBindingRegistration),
		contractCLIBindings:       make(map[string]ContractCLIBindingRegistration),
		contractPages:             make(map[string]ContractPageRegistration),
		contractEventBuses:        make(map[string]ContractEventBus),
		contractEventConsumers:    make(map[string]ContractEventConsumerRegistration),
		contractEventEmissions:    make(map[string]ContractEventEmissionRegistration),
		serviceInitializers:       make(map[string]serviceInitializer),
		serviceInitOrder:          make(map[string]int),
		serviceShutdowns:          make(map[string]serviceShutdown),
		meta: shared.AppMetadata{
			Environment: defaultEnvironment(),
		},
	}
	return func() {
		global = prev
	}
}
