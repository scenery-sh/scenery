package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	temporalinterceptor "go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
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
	t.Setenv(DefaultOnlavaSessionIDEnv, "")

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
	if info.TaskQueuePrefix != "onlava.demo.app" {
		t.Fatalf("task queue prefix = %q", info.TaskQueuePrefix)
	}
	if info.PayloadCodec != DefaultTemporalPayloadCodec {
		t.Fatalf("payload codec = %q", info.PayloadCodec)
	}
	if info.DeploymentName != "onlava-demo-app" || info.DeploymentEnvSet {
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
	if info.SessionID != "" || info.SessionIDEnv != DefaultOnlavaSessionIDEnv || info.SessionIDEnvSet {
		t.Fatalf("session = %q env=%q set=%v", info.SessionID, info.SessionIDEnv, info.SessionIDEnvSet)
	}
}

func TestResolveTemporalConfigUsesEnvFallbacks(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "temporal.example:7233")
	t.Setenv(DefaultTemporalNamespaceEnv, "prod")
	t.Setenv(DefaultTemporalTaskQueueEnv, "onlava.orders.session")
	t.Setenv(DefaultTemporalBuildIDEnv, "git-sha")
	t.Setenv(DefaultTemporalDeploymentEnv, "orders-api")
	t.Setenv(DefaultTemporalVersioningEnv, "auto-upgrade")
	t.Setenv(DefaultTemporalAPIKeyEnv, "secret")
	t.Setenv(DefaultTemporalTLSServerNameEnv, "orders.tmprl.cloud")
	t.Setenv(DefaultTemporalHostReportingEnv, "0")
	t.Setenv(DefaultOnlavaSessionIDEnv, "session-a")

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
	if info.TaskQueuePrefix != "onlava.orders.session" || !info.TaskQueueEnvSet {
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
			DBFilename: ".state/temporal.sqlite",
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
	if info.LocalDBFilename != ".state/temporal.sqlite" {
		t.Fatalf("local db filename = %q", info.LocalDBFilename)
	}
}

func TestTemporalClientOptionsValidatePayloadCodec(t *testing.T) {
	_, err := temporalClientOptions(TemporalRuntimeInfo{
		Address:      DefaultTemporalAddress,
		Namespace:    DefaultTemporalNamespace,
		PayloadCodec: "custom",
	})
	if err == nil || !strings.Contains(err.Error(), "payload_codec") {
		t.Fatalf("temporalClientOptions error = %v", err)
	}
}

func TestTemporalClientOptionsAddsDevTelemetryInterceptor(t *testing.T) {
	restore := setActiveReporterForTest(&devReporter{appID: "demo"})
	defer restore()

	options, err := temporalClientOptions(TemporalRuntimeInfo{
		Address:      DefaultTemporalAddress,
		Namespace:    DefaultTemporalNamespace,
		PayloadCodec: DefaultTemporalPayloadCodec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Interceptors) != 1 {
		t.Fatalf("interceptors = %d, want 1", len(options.Interceptors))
	}
}

func TestOnlavaTemporalTracerPropagatesParent(t *testing.T) {
	tracer := newOnlavaTemporalTracer(TemporalRuntimeInfo{})
	parent, err := tracer.UnmarshalSpan(map[string]string{
		"trace_id": "11111111111111111111111111111111",
		"span_id":  "2222222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	span, err := tracer.StartSpan(&temporalinterceptor.TracerStartSpanOptions{
		Parent:    parent,
		Operation: "RunActivity",
		Name:      "agents.PlanCIFailureFix/v1",
		Time:      time.Unix(10, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := tracer.MarshalSpan(span)
	if err != nil {
		t.Fatal(err)
	}
	if data["trace_id"] != "11111111111111111111111111111111" || !isTemporalSpanID(data["span_id"]) {
		t.Fatalf("marshaled span = %#v", data)
	}
	got := span.(*onlavaTemporalSpan)
	if got.parentSpanID != "2222222222222222" || temporalTraceType(got.operation) != "TEMPORAL_ACTIVITY" {
		t.Fatalf("span = %#v", got)
	}
}

func setActiveReporterForTest(reporter *devReporter) func() {
	reporterMu.Lock()
	prev := globalReporter
	globalReporter = reporter
	reporterMu.Unlock()
	return func() {
		reporterMu.Lock()
		globalReporter = prev
		reporterMu.Unlock()
	}
}

func TestTemporalTLSConfigRequiresCertAndKeyPair(t *testing.T) {
	t.Setenv("TEMPORAL_TEST_CERT", "/tmp/missing-cert.pem")
	t.Setenv("TEMPORAL_TEST_KEY", "")
	_, enabled, err := temporalTLSConfig(TemporalRuntimeInfo{
		TLSEnabled:     true,
		TLSCertFileEnv: "TEMPORAL_TEST_CERT",
		TLSKeyFileEnv:  "TEMPORAL_TEST_KEY",
	})
	if err == nil || !strings.Contains(err.Error(), "must both be set") {
		t.Fatalf("temporalTLSConfig enabled=%v error=%v", enabled, err)
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
	for _, want := range []string{"onlava:", "orders-api", "worker", "orders.go", "build-sha.123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TemporalWorkerIdentity = %q, want it to contain %q", got, want)
		}
	}
}

func TestTemporalWorkerOptionsEnableDeploymentVersioning(t *testing.T) {
	restore := setActiveReporterForTest(nil)
	defer restore()

	info := TemporalRuntimeInfo{
		DeploymentName: "orders-api",
		WorkerBuildID:  "sha.123",
		Versioning:     TemporalVersioningAutoUpgrade,
	}
	opts := TemporalWorkerOptions(info, "worker", "orders.go")
	if !opts.DeploymentOptions.UseVersioning {
		t.Fatal("expected worker deployment versioning")
	}
	if opts.DeploymentOptions.Version.DeploymentName != "orders-api" {
		t.Fatalf("deployment name = %q", opts.DeploymentOptions.Version.DeploymentName)
	}
	if opts.DeploymentOptions.Version.BuildID != "sha.123" {
		t.Fatalf("build id = %q", opts.DeploymentOptions.Version.BuildID)
	}
	if opts.DeploymentOptions.DefaultVersioningBehavior != workflow.VersioningBehaviorAutoUpgrade {
		t.Fatalf("versioning behavior = %v", opts.DeploymentOptions.DefaultVersioningBehavior)
	}
}

func TestTemporalWorkerOptionsAddsDevTelemetryInterceptor(t *testing.T) {
	restore := setActiveReporterForTest(&devReporter{appID: "demo"})
	defer restore()

	opts := TemporalWorkerOptions(TemporalRuntimeInfo{}, "worker", "orders.go")
	if len(opts.Interceptors) != 1 {
		t.Fatalf("interceptors = %d, want 1", len(opts.Interceptors))
	}
}

func TestTemporalWorkerOptionsEnableHostResourceReporting(t *testing.T) {
	restore := setActiveReporterForTest(nil)
	defer restore()

	opts := TemporalWorkerOptions(TemporalRuntimeInfo{
		DeploymentName: "orders-api",
	}, "worker", "orders.go")
	if opts.SysInfoProvider == nil {
		t.Fatal("expected SysInfoProvider when host resource reporting uses default")
	}

	opts = TemporalWorkerOptions(TemporalRuntimeInfo{
		DeploymentName:   "orders-api",
		HostReporting:    false,
		HostReportingSet: true,
	}, "worker", "orders.go")
	if opts.SysInfoProvider != nil {
		t.Fatal("did not expect SysInfoProvider when host resource reporting is disabled")
	}
}

func TestSessionScopedTemporalTaskQueue(t *testing.T) {
	info := TemporalRuntimeInfo{
		TaskQueuePrefix: "onlava.orders.session-a",
		SessionID:       "session-a",
	}
	for _, tt := range []struct {
		name  string
		queue string
		want  string
	}{
		{name: "explicit", queue: "orders.go", want: "onlava.orders.session-a.orders.go"},
		{name: "already scoped", queue: "onlava.orders.session-a.orders.go", want: "onlava.orders.session-a.orders.go"},
		{name: "empty", queue: "", want: ""},
		{name: "sanitize", queue: "House/Process Queue", want: "onlava.orders.session-a.House.Process.Queue"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionScopedTemporalTaskQueue(info, tt.queue); got != tt.want {
				t.Fatalf("SessionScopedTemporalTaskQueue = %q, want %q", got, tt.want)
			}
		})
	}
	if got := SessionScopedTemporalTaskQueue(TemporalRuntimeInfo{TaskQueuePrefix: "onlava.orders"}, "orders.go"); got != "orders.go" {
		t.Fatalf("without session = %q", got)
	}
}

func TestSessionScopedTemporalTaskQueueFromEnv(t *testing.T) {
	t.Setenv(DefaultTemporalTaskQueueEnv, "onlava.orders.session-a")
	t.Setenv(DefaultOnlavaSessionIDEnv, "session-a")
	if got := SessionScopedTemporalTaskQueueFromEnv("orders.go"); got != "onlava.orders.session-a.orders.go" {
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
