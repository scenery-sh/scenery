package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"onlava.com/runtime/shared"
)

func TestInitializeServicesRunsInParallel(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	started := make(chan string, 2)
	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	errCh := make(chan error, 1)

	blockingInit := func(name string) func() error {
		return func() error {
			started <- name
			<-release
			return nil
		}
	}
	RegisterServiceInitializer("zeta", blockingInit("zeta"))
	RegisterServiceInitializer("alpha", blockingInit("alpha"))

	go func() {
		defer done.Done()
		errCh <- InitializeServices()
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for service initializers to start; saw %v", seen)
		}
	}
	close(release)
	done.Wait()

	if err := <-errCh; err != nil {
		t.Fatalf("InitializeServices() error = %v", err)
	}
	if !seen["alpha"] || !seen["zeta"] {
		t.Fatalf("InitializeServices() started = %v, want both services", seen)
	}
}

func TestInitializeServicesPropagatesErrors(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	RegisterServiceInitializer("service", func() error {
		return errors.New("boom")
	})

	err := InitializeServices()
	if err == nil || err.Error() != "initialize service service: boom" {
		t.Fatalf("InitializeServices() error = %v, want initialize service service: boom", err)
	}
}

func TestShutdownServicesRunsInReverseInitializerOrder(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	RegisterServiceInitializer("alpha", func() error { return nil })
	RegisterServiceInitializer("zeta", func() error { return nil })
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
	t.Setenv("ONLAVA_RUNTIME_ENV", "test")
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

	t.Setenv("ONLAVA_RUNTIME_ENV", "test")
	SetAppConfig(AppConfig{Name: "testapp", ListenAddr: "127.0.0.1:4000"})
	meta := Meta()
	if meta.Environment.Type != shared.EnvTest {
		t.Fatalf("Meta().Environment.Type = %q, want %q", meta.Environment.Type, shared.EnvTest)
	}
	if meta.Environment.Name != "test" {
		t.Fatalf("Meta().Environment.Name = %q, want %q", meta.Environment.Name, "test")
	}
}

func replaceGlobalRegistryForTest() func() {
	prev := global
	global = &registry{
		endpoints:           make(map[string]*Endpoint),
		middlewares:         make(map[string]*Middleware),
		cronJobs:            make(map[string]*CronJob),
		serviceInitializers: make(map[string]func() error),
		serviceInitOrder:    make(map[string]int),
		serviceShutdowns:    make(map[string]serviceShutdown),
		meta: shared.AppMetadata{
			Environment: defaultEnvironment(),
		},
	}
	return func() {
		global = prev
	}
}
