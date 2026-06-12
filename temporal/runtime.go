package temporal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	temporalinterceptor "go.temporal.io/sdk/interceptor"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"scenery.sh/internal/envpolicy"
	sceneryruntime "scenery.sh/runtime"
)

const workerDeploymentPromotionTimeout = 30 * time.Second

type temporalRuntimeState struct {
	client temporalclient.Client
	info   sceneryruntime.TemporalRuntimeInfo
}

var activeTemporal struct {
	mu    sync.RWMutex
	state *temporalRuntimeState
}

var temporalTracingEnabled = sceneryruntime.TemporalTracingEnabled

func StartRuntime(ctx context.Context, cfg sceneryruntime.AppConfig) (func(context.Context) error, error) {
	info := sceneryruntime.ResolveTemporalConfig(cfg.Name, cfg.Temporal)
	if !info.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	if err := sceneryruntime.ValidateTemporalVersioning(info); err != nil {
		return nil, err
	}
	client, err := Dial(ctx, info)
	if err != nil {
		return nil, err
	}
	state := &temporalRuntimeState{
		client: client,
		info:   info,
	}
	activeTemporal.mu.Lock()
	activeTemporal.state = state
	activeTemporal.mu.Unlock()
	return func(context.Context) error {
		activeTemporal.mu.Lock()
		if activeTemporal.state == state {
			activeTemporal.state = nil
		}
		activeTemporal.mu.Unlock()
		client.Close()
		return nil
	}, nil
}

func ActiveClient() (temporalclient.Client, sceneryruntime.TemporalRuntimeInfo, bool) {
	activeTemporal.mu.RLock()
	defer activeTemporal.mu.RUnlock()
	if activeTemporal.state == nil {
		return nil, sceneryruntime.TemporalRuntimeInfo{}, false
	}
	return activeTemporal.state.client, activeTemporal.state.info, true
}

func CheckConnection(ctx context.Context, appName string, cfg sceneryruntime.TemporalConfig) (sceneryruntime.TemporalRuntimeInfo, sceneryruntime.TemporalConnectionStatus) {
	info := sceneryruntime.ResolveTemporalConfig(appName, cfg)
	if !info.Enabled {
		return info, sceneryruntime.TemporalConnectionStatus{}
	}
	client, err := Dial(ctx, info)
	if err != nil {
		return info, sceneryruntime.TemporalConnectionStatus{
			Checked: true,
			Error:   err.Error(),
		}
	}
	client.Close()
	return info, sceneryruntime.TemporalConnectionStatus{
		Checked:   true,
		Reachable: true,
	}
}

func Dial(ctx context.Context, info sceneryruntime.TemporalRuntimeInfo) (temporalclient.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options, err := temporalClientOptions(info)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, sceneryruntime.DefaultTemporalConnectWait)
	defer cancel()
	client, err := temporalclient.DialContext(dialCtx, options)
	if err != nil {
		return nil, fmt.Errorf("temporal: connect to %s namespace %s: %w", info.Address, info.Namespace, err)
	}
	return client, nil
}

func temporalClientOptions(info sceneryruntime.TemporalRuntimeInfo) (temporalclient.Options, error) {
	if err := sceneryruntime.ValidateTemporalPayloadCodec(info.PayloadCodec); err != nil {
		return temporalclient.Options{}, err
	}
	options := temporalclient.Options{
		HostPort:  info.Address,
		Namespace: info.Namespace,
		Identity:  temporalIdentity(info),
	}
	if apiKey, ok := envValue(info.APIKeyEnv); ok {
		options.Credentials = temporalclient.NewAPIKeyStaticCredentials(apiKey)
	}
	tlsConfig, enabled, err := temporalTLSConfig(info)
	if err != nil {
		return temporalclient.Options{}, err
	}
	if enabled {
		options.ConnectionOptions.TLS = tlsConfig
	}
	if temporalTracingEnabled() {
		options.Interceptors = append(options.Interceptors, temporalinterceptor.NewTracingInterceptor(newSceneryTemporalTracer(info)))
	}
	return options, nil
}

func temporalTLSConfig(info sceneryruntime.TemporalRuntimeInfo) (*tls.Config, bool, error) {
	caPath, caSet := envValue(info.TLSCACertFileEnv)
	certPath, certSet := envValue(info.TLSCertFileEnv)
	keyPath, keySet := envValue(info.TLSKeyFileEnv)
	enabled := info.TLSEnabled || info.TLSServerNameSet || caSet || certSet || keySet
	if !enabled {
		return nil, false, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(info.TLSServerName),
	}
	if caSet {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, false, fmt.Errorf("temporal: read TLS CA certificate from %s: %w", info.TLSCACertFileEnv, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, false, fmt.Errorf("temporal: TLS CA certificate from %s does not contain PEM certificates", info.TLSCACertFileEnv)
		}
		cfg.RootCAs = pool
	}
	if certSet != keySet {
		return nil, false, fmt.Errorf("temporal: TLS client certificate and key must both be set with %s and %s", info.TLSCertFileEnv, info.TLSKeyFileEnv)
	}
	if certSet {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, false, fmt.Errorf("temporal: load TLS client certificate from %s/%s: %w", info.TLSCertFileEnv, info.TLSKeyFileEnv, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, true, nil
}

func temporalIdentity(info sceneryruntime.TemporalRuntimeInfo) string {
	pid := os.Getpid()
	if info.TaskQueuePrefix == "" {
		return fmt.Sprintf("scenery:%d", pid)
	}
	return fmt.Sprintf("%s:%d", info.TaskQueuePrefix, pid)
}

func TemporalWorkerOptions(info sceneryruntime.TemporalRuntimeInfo, role, taskQueue string) temporalworker.Options {
	buildID := sceneryruntime.TemporalWorkerBuildID(info)
	opts := temporalworker.Options{
		DisableRegistrationAliasing: true,
		Identity:                    sceneryruntime.TemporalWorkerIdentity(info, role, taskQueue),
		BuildID:                     buildID,
		DeploymentOptions: temporalworker.DeploymentOptions{
			UseVersioning: true,
			Version: temporalworker.WorkerDeploymentVersion{
				DeploymentName: sceneryruntime.TemporalDeploymentName(info),
				BuildID:        buildID,
			},
			DefaultVersioningBehavior: TemporalWorkflowVersioningBehavior(info),
		},
	}
	if sceneryruntime.TemporalHostResourceReportingEnabled(info) {
		opts.SysInfoProvider = sysinfo.SysInfoProvider()
	}
	if temporalTracingEnabled() {
		opts.Interceptors = append(opts.Interceptors, temporalinterceptor.NewTracingInterceptor(newSceneryTemporalTracer(info)))
	}
	return opts
}

func TemporalWorkflowVersioningBehavior(info sceneryruntime.TemporalRuntimeInfo) workflow.VersioningBehavior {
	switch sceneryruntime.NormalizeTemporalVersioning(info.Versioning) {
	case sceneryruntime.TemporalVersioningAutoUpgrade:
		return workflow.VersioningBehaviorAutoUpgrade
	default:
		return workflow.VersioningBehaviorPinned
	}
}

func EnsureWorkerDeploymentCurrentVersion(ctx context.Context, client temporalclient.Client, info sceneryruntime.TemporalRuntimeInfo) error {
	if client == nil {
		return fmt.Errorf("temporal: missing client for worker deployment versioning")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	updateCtx, cancel := context.WithTimeout(ctx, workerDeploymentPromotionTimeout)
	defer cancel()
	deploymentName := sceneryruntime.TemporalDeploymentName(info)
	buildID := sceneryruntime.TemporalWorkerBuildID(info)
	_, err := client.WorkerDeploymentClient().GetHandle(deploymentName).SetCurrentVersion(updateCtx, temporalclient.WorkerDeploymentSetCurrentVersionOptions{
		BuildID:                 buildID,
		Identity:                temporalIdentity(info),
		IgnoreMissingTaskQueues: true,
		AllowNoPollers:          true,
	})
	if err != nil {
		return fmt.Errorf("temporal: set worker deployment %s current version %s: %w", deploymentName, buildID, err)
	}
	return nil
}

func envValue(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	value, ok := envpolicy.Lookup(name)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}
