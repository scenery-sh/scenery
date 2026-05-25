package runtime

import (
	"context"
	"strings"
	"testing"

	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

func TestResolveTemporalConfigDefaults(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "")
	t.Setenv(DefaultTemporalNamespaceEnv, "")
	t.Setenv(DefaultTemporalBuildIDEnv, "")
	t.Setenv(DefaultTemporalDeploymentEnv, "")
	t.Setenv(DefaultTemporalVersioningEnv, "")

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
}

func TestResolveTemporalConfigUsesEnvFallbacks(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "temporal.example:7233")
	t.Setenv(DefaultTemporalNamespaceEnv, "prod")
	t.Setenv(DefaultTemporalBuildIDEnv, "git-sha")
	t.Setenv(DefaultTemporalDeploymentEnv, "orders-api")
	t.Setenv(DefaultTemporalVersioningEnv, "auto-upgrade")

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
	if info.DeploymentName != "orders-api" || !info.DeploymentEnvSet {
		t.Fatalf("deployment/env = %q/%v", info.DeploymentName, info.DeploymentEnvSet)
	}
	if info.WorkerBuildID != "git-sha" || !info.WorkerBuildIDSet {
		t.Fatalf("worker build/env = %q/%v", info.WorkerBuildID, info.WorkerBuildIDSet)
	}
	if info.Versioning != TemporalVersioningAutoUpgrade || !info.VersioningEnvSet {
		t.Fatalf("versioning/env = %q/%v", info.Versioning, info.VersioningEnvSet)
	}
}

func TestResolveTemporalConfigPrefersExplicitValues(t *testing.T) {
	t.Setenv(DefaultTemporalAddressEnv, "ignored.example:7233")
	t.Setenv(DefaultTemporalNamespaceEnv, "ignored")
	t.Setenv("CUSTOM_TEMPORAL_ADDRESS", "custom.example:7233")

	info := ResolveTemporalConfig("demo", TemporalConfig{
		Enabled:         true,
		Mode:            "production",
		Namespace:       "explicit",
		AddressEnv:      "CUSTOM_TEMPORAL_ADDRESS",
		TaskQueuePrefix: "custom.queue",
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
	if info.LocalDBFilename != ".state/temporal.sqlite" {
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
	for _, want := range []string{"onlava:", "orders-api", "worker", "orders.go", "build-sha.123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TemporalWorkerIdentity = %q, want it to contain %q", got, want)
		}
	}
}

func TestTemporalWorkerOptionsEnableDeploymentVersioning(t *testing.T) {
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

func TestTemporalWorkflowVersioningOverride(t *testing.T) {
	pinned := TemporalWorkflowVersioningOverride(TemporalRuntimeInfo{
		DeploymentName: "orders.api",
		WorkerBuildID:  "sha.123",
		Versioning:     TemporalVersioningPinned,
	})
	pinnedOverride, ok := pinned.(*temporalclient.PinnedVersioningOverride)
	if !ok {
		t.Fatalf("pinned override = %T", pinned)
	}
	if pinnedOverride.Version.DeploymentName != "orders-api" || pinnedOverride.Version.BuildID != "sha.123" {
		t.Fatalf("pinned override version = %+v", pinnedOverride.Version)
	}

	auto := TemporalWorkflowVersioningOverride(TemporalRuntimeInfo{Versioning: TemporalVersioningAutoUpgrade})
	if _, ok := auto.(*temporalclient.AutoUpgradeVersioningOverride); !ok {
		t.Fatalf("auto override = %T", auto)
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
