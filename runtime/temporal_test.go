package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestResolveTemporalConfigDefaults(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "")
	t.Setenv(DefaultTemporalNamespaceEnv, "")
	t.Setenv(DefaultTemporalTaskQueueEnv, "")
	t.Setenv(DefaultTemporalBuildIDEnv, "")
	t.Setenv(DefaultTemporalDeploymentEnv, "")
	t.Setenv(DefaultTemporalVersioningEnv, "")
	t.Setenv(DefaultTemporalAPIKeyEnv, "")
	t.Setenv(DefaultTemporalTLSServerNameEnv, "")
	t.Setenv(DefaultTemporalTLSCACertFileEnv, "")
	t.Setenv(DefaultTemporalTLSCertFileEnv, "")
	t.Setenv(DefaultTemporalTLSKeyFileEnv, "")
	t.Setenv(DefaultTemporalHostReportingEnv, "")
	t.Setenv(DefaultScenerySessionIDEnv, "")

	info := ResolveTemporalConfig("demo_app", TemporalConfig{})
	if info.Enabled {
		t.Fatal("expected temporal disabled by default")
	}
	if info.Mode != DefaultTemporalMode {
		t.Fatalf("mode = %q, want %q", info.Mode, DefaultTemporalMode)
	}
	if info.Address != DefaultTemporalAddress {
		t.Fatalf("address = %q, want %q", info.Address, DefaultTemporalAddress)
	}
	if info.Namespace != DefaultTemporalNamespace {
		t.Fatalf("namespace = %q, want %q", info.Namespace, DefaultTemporalNamespace)
	}
	if info.TaskQueuePrefix != "scenery.demo.app" {
		t.Fatalf("task queue prefix = %q", info.TaskQueuePrefix)
	}
	if info.PayloadCodec != DefaultTemporalPayloadCodec {
		t.Fatalf("payload codec = %q", info.PayloadCodec)
	}
	if info.DeploymentName != "scenery-demo-app" || info.DeploymentEnvSet {
		t.Fatalf("deployment = %q/%v", info.DeploymentName, info.DeploymentEnvSet)
	}
	if info.WorkerBuildID != DefaultTemporalBuildID || info.WorkerBuildIDSet {
		t.Fatalf("worker build id = %q/%v", info.WorkerBuildID, info.WorkerBuildIDSet)
	}
	if info.Versioning != TemporalVersioningPinned || info.VersioningEnvSet {
		t.Fatalf("versioning = %q/%v", info.Versioning, info.VersioningEnvSet)
	}
	if info.LocalDBFilename != DefaultTemporalLocalDBFile {
		t.Fatalf("local db filename = %q, want %q", info.LocalDBFilename, DefaultTemporalLocalDBFile)
	}
	if !info.HostReporting || info.HostReportingEnv != DefaultTemporalHostReportingEnv || info.HostReportingSet {
		t.Fatalf("host reporting = %v env=%q set=%v", info.HostReporting, info.HostReportingEnv, info.HostReportingSet)
	}
	if info.SessionID != "" || info.SessionIDEnv != DefaultScenerySessionIDEnv || info.SessionIDEnvSet {
		t.Fatalf("session = %q env=%q set=%v", info.SessionID, info.SessionIDEnv, info.SessionIDEnvSet)
	}
}

func TestResolveTemporalConfigUsesEnvFallbacks(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "temporal.example:7233")
	t.Setenv(DefaultTemporalNamespaceEnv, "prod")
	t.Setenv(DefaultTemporalTaskQueueEnv, "scenery.orders.session")
	t.Setenv(DefaultTemporalBuildIDEnv, "git-sha")
	t.Setenv(DefaultTemporalDeploymentEnv, "orders-api")
	t.Setenv(DefaultTemporalVersioningEnv, "auto-upgrade")
	t.Setenv(DefaultTemporalAPIKeyEnv, "secret")
	t.Setenv(DefaultTemporalTLSServerNameEnv, "orders.tmprl.cloud")
	t.Setenv(DefaultTemporalHostReportingEnv, "0")
	t.Setenv(DefaultScenerySessionIDEnv, "session-a")

	info := ResolveTemporalConfig("demo", TemporalConfig{Enabled: true})
	if !info.Enabled {
		t.Fatal("expected temporal enabled")
	}
	if info.Address != "temporal.example:7233" || !info.AddressEnvSet {
		t.Fatalf("address/env = %q/%v", info.Address, info.AddressEnvSet)
	}
	if info.Namespace != "prod" || !info.NamespaceEnvSet {
		t.Fatalf("namespace/env = %q/%v", info.Namespace, info.NamespaceEnvSet)
	}
	if info.TaskQueuePrefix != "scenery.orders.session" || !info.TaskQueueEnvSet {
		t.Fatalf("task queue env = %q/%v", info.TaskQueuePrefix, info.TaskQueueEnvSet)
	}
	if info.DeploymentName != "orders-api" || !info.DeploymentEnvSet {
		t.Fatalf("deployment/env = %q/%v", info.DeploymentName, info.DeploymentEnvSet)
	}
	if info.WorkerBuildID != "git-sha" || !info.WorkerBuildIDSet {
		t.Fatalf("worker build/env = %q/%v", info.WorkerBuildID, info.WorkerBuildIDSet)
	}
	if info.Versioning != TemporalVersioningAutoUpgrade || !info.VersioningEnvSet {
		t.Fatalf("versioning/env = %q/%v", info.Versioning, info.VersioningEnvSet)
	}
	if !info.APIKeyEnvSet || !info.TLSEnabled || info.TLSServerName != "orders.tmprl.cloud" || !info.TLSServerNameSet {
		t.Fatalf("security envs = %+v", info)
	}
	if info.HostReporting || !info.HostReportingSet {
		t.Fatalf("host reporting env = %v/%v", info.HostReporting, info.HostReportingSet)
	}
	if info.SessionID != "session-a" || !info.SessionIDEnvSet {
		t.Fatalf("session env = %q/%v", info.SessionID, info.SessionIDEnvSet)
	}
}

func TestResolveTemporalConfigPrefersExplicitValues(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "ignored.example:7233")
	t.Setenv(DefaultTemporalNamespaceEnv, "ignored")
	t.Setenv(DefaultTemporalTaskQueueEnv, "")
	t.Setenv("CUSTOM_TEMPORAL_ADDRESS", "custom.example:7233")

	info := ResolveTemporalConfig("demo", TemporalConfig{
		Enabled:         true,
		Mode:            "production",
		Namespace:       "explicit",
		AddressEnv:      "CUSTOM_TEMPORAL_ADDRESS",
		TaskQueuePrefix: "custom.queue",
		PayloadCodec:    DefaultTemporalPayloadCodec,
		APIKeyEnv:       "CUSTOM_TEMPORAL_API_KEY",
		TLS: TemporalTLSConfig{
			Enabled:       true,
			ServerNameEnv: "CUSTOM_TEMPORAL_TLS_SERVER_NAME",
		},
		Local: TemporalLocalConfig{
			AutoStart:  true,
			DBFilename: ".state/temporal.db",
		},
	})
	if info.Mode != "production" {
		t.Fatalf("mode = %q", info.Mode)
	}
	if info.Address != "custom.example:7233" || !info.AddressEnvSet {
		t.Fatalf("address/env = %q/%v", info.Address, info.AddressEnvSet)
	}
	if info.Namespace != "explicit" || info.NamespaceEnvSet {
		t.Fatalf("namespace/env = %q/%v", info.Namespace, info.NamespaceEnvSet)
	}
	if info.TaskQueuePrefix != "custom.queue" || !info.LocalAutoStart {
		t.Fatalf("info = %+v", info)
	}
	if info.APIKeyEnv != "CUSTOM_TEMPORAL_API_KEY" || !info.TLSEnabled || info.TLSServerNameEnv != "CUSTOM_TEMPORAL_TLS_SERVER_NAME" {
		t.Fatalf("security config = %+v", info)
	}
	if info.LocalDBFilename != ".state/temporal.db" {
		t.Fatalf("local db filename = %q", info.LocalDBFilename)
	}
}

func TestStartTemporalRuntimeDisabledNoops(t *testing.T) {
	stop, err := StartTemporalRuntime(context.Background(), AppConfig{Name: "demo"})
	if err != nil {
		t.Fatalf("StartTemporalRuntime returned error: %v", err)
	}
	if stop == nil {
		t.Fatal("expected stop function")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop returned error: %v", err)
	}
}

func TestTemporalWorkerIdentityIncludesDeploymentRoleQueueAndBuild(t *testing.T) {
	info := TemporalRuntimeInfo{
		DeploymentName: "orders-api",
		WorkerBuildID:  "sha.123",
	}
	got := TemporalWorkerIdentity(info, "worker", "orders.go")
	for _, want := range []string{"scenery:", "orders-api", "worker", "orders.go", "build-sha.123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TemporalWorkerIdentity = %q, want it to contain %q", got, want)
		}
	}
}

func TestSessionScopedTemporalTaskQueue(t *testing.T) {
	info := TemporalRuntimeInfo{
		TaskQueuePrefix: "scenery.orders.session-a",
		SessionID:       "session-a",
	}
	for _, tt := range []struct {
		name  string
		queue string
		want  string
	}{
		{name: "explicit", queue: "orders.go", want: "scenery.orders.session-a.orders.go"},
		{name: "already scoped", queue: "scenery.orders.session-a.orders.go", want: "scenery.orders.session-a.orders.go"},
		{name: "empty", queue: "", want: ""},
		{name: "sanitize", queue: "House/Process Queue", want: "scenery.orders.session-a.House.Process.Queue"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionScopedTemporalTaskQueue(info, tt.queue); got != tt.want {
				t.Fatalf("SessionScopedTemporalTaskQueue = %q, want %q", got, tt.want)
			}
		})
	}
	if got := SessionScopedTemporalTaskQueue(TemporalRuntimeInfo{TaskQueuePrefix: "scenery.orders"}, "orders.go"); got != "orders.go" {
		t.Fatalf("without session = %q", got)
	}
}

func TestSessionScopedTemporalTaskQueueFromEnv(t *testing.T) {
	t.Setenv(DefaultTemporalTaskQueueEnv, "scenery.orders.session-a")
	t.Setenv(DefaultScenerySessionIDEnv, "session-a")
	if got := SessionScopedTemporalTaskQueueFromEnv("orders.go"); got != "scenery.orders.session-a.orders.go" {
		t.Fatalf("SessionScopedTemporalTaskQueueFromEnv = %q", got)
	}
}

func TestResolveTemporalConfigIsolatesTestRuntimeTaskQueues(t *testing.T) {
	t.Setenv(DefaultSceneryRuntimeEnv, "test")
	t.Setenv(DefaultTemporalTestQueueSuffixEnv, "run-123")
	t.Setenv(DefaultTemporalTaskQueueEnv, "scenery.orders.feature-a")
	t.Setenv(DefaultScenerySessionIDEnv, "feature-a")
	t.Setenv(DefaultTemporalDeploymentEnv, "")

	info := ResolveTemporalConfig("orders", TemporalConfig{Enabled: true})
	if info.TaskQueuePrefix != "scenery.orders.feature-a.test.run.123" {
		t.Fatalf("task queue prefix = %q", info.TaskQueuePrefix)
	}
	if info.SessionID != "feature-a" {
		t.Fatalf("session id = %q", info.SessionID)
	}
	if info.DeploymentName != "scenery-orders-feature-a-test-run-123" {
		t.Fatalf("deployment name = %q", info.DeploymentName)
	}
	if got := SessionScopedTemporalTaskQueue(info, "orders.go"); got != "scenery.orders.feature-a.test.run.123.orders.go" {
		t.Fatalf("scoped queue = %q", got)
	}
}

func TestResolveTemporalConfigAddsTestSessionWhenMissing(t *testing.T) {
	t.Setenv(DefaultSceneryRuntimeEnv, "test")
	t.Setenv(DefaultTemporalTestQueueSuffixEnv, "run-456")
	t.Setenv(DefaultTemporalTaskQueueEnv, "")
	t.Setenv(DefaultScenerySessionIDEnv, "")

	info := ResolveTemporalConfig("orders", TemporalConfig{Enabled: true})
	if info.TaskQueuePrefix != "scenery.orders.test.run.456" {
		t.Fatalf("task queue prefix = %q", info.TaskQueuePrefix)
	}
	if info.SessionID != "test.run.456" || info.SessionIDEnvSet {
		t.Fatalf("session = %q envSet=%v", info.SessionID, info.SessionIDEnvSet)
	}
}

func TestTestRuntimeDoesNotAdoptDevRuntimeTaskQueue(t *testing.T) {
	t.Setenv(DefaultTemporalTestQueueSuffixEnv, "run-789")
	t.Setenv(DefaultTemporalTaskQueueEnv, "scenery.orders.feature-a")
	t.Setenv(DefaultScenerySessionIDEnv, "feature-a")
	t.Setenv(DefaultTemporalDeploymentEnv, "")

	t.Setenv(DefaultSceneryRuntimeEnv, "local")
	devInfo := ResolveTemporalConfig("orders", TemporalConfig{Enabled: true})
	devQueue := SessionScopedTemporalTaskQueue(devInfo, "orders.go")

	t.Setenv(DefaultSceneryRuntimeEnv, "test")
	testInfo := ResolveTemporalConfig("orders", TemporalConfig{Enabled: true})
	testQueue := SessionScopedTemporalTaskQueue(testInfo, "orders.go")

	if devInfo.TaskQueuePrefix == testInfo.TaskQueuePrefix {
		t.Fatalf("test runtime adopted dev task queue prefix %q", testInfo.TaskQueuePrefix)
	}
	if devQueue == testQueue {
		t.Fatalf("test runtime adopted dev task queue %q", testQueue)
	}
	if testQueue != "scenery.orders.feature-a.test.run.789.orders.go" {
		t.Fatalf("test queue = %q", testQueue)
	}
}

func TestSessionScopedTemporalTaskQueueFromEnvUsesTestIsolation(t *testing.T) {
	t.Setenv(DefaultSceneryRuntimeEnv, "test")
	t.Setenv(DefaultTemporalTestQueueSuffixEnv, "run-789")
	t.Setenv(DefaultTemporalTaskQueueEnv, "scenery.orders.feature-a")
	t.Setenv(DefaultScenerySessionIDEnv, "feature-a")

	got := SessionScopedTemporalTaskQueueFromEnv("orders.go")
	if got != "scenery.orders.feature-a.test.run.789.orders.go" {
		t.Fatalf("SessionScopedTemporalTaskQueueFromEnv = %q", got)
	}
}

func TestShouldAutoPromoteTemporalWorkerDeployment(t *testing.T) {
	for _, mode := range []string{"", "local", " LOCAL "} {
		if !ShouldAutoPromoteTemporalWorkerDeployment(TemporalRuntimeInfo{Mode: mode}) {
			t.Fatalf("mode %q should auto-promote", mode)
		}
	}
	for _, mode := range []string{"production", "cloud"} {
		if ShouldAutoPromoteTemporalWorkerDeployment(TemporalRuntimeInfo{Mode: mode}) {
			t.Fatalf("mode %q should not auto-promote", mode)
		}
	}
}

func TestValidateTemporalVersioningRejectsUnknown(t *testing.T) {
	err := validateTemporalVersioning(TemporalRuntimeInfo{
		VersioningEnv: DefaultTemporalVersioningEnv,
		Versioning:    "surprise",
	})
	if err == nil || !strings.Contains(err.Error(), DefaultTemporalVersioningEnv) {
		t.Fatalf("expected versioning error, got %v", err)
	}
}
