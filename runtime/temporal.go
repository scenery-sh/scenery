package runtime

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
	"go.temporal.io/sdk/converter"
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	DefaultTemporalAddress              = "127.0.0.1:7233"
	DefaultTemporalAddressEnv           = "TEMPORAL_ADDRESS"
	DefaultTemporalNamespace            = "default"
	DefaultTemporalNamespaceEnv         = "TEMPORAL_NAMESPACE"
	DefaultTemporalBuildID              = "dev"
	DefaultTemporalBuildIDEnv           = "ONLAVA_BUILD_ID"
	DefaultTemporalDeploymentEnv        = "ONLAVA_TEMPORAL_DEPLOYMENT_NAME"
	DefaultTemporalVersioningEnv        = "ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR"
	DefaultTemporalVersioning           = "pinned"
	DefaultTemporalPayloadCodec         = "onlava-json-v1"
	DefaultTemporalTLSServerNameEnv     = "TEMPORAL_TLS_SERVER_NAME"
	DefaultTemporalTLSCACertFileEnv     = "TEMPORAL_TLS_CA_CERT_FILE"
	DefaultTemporalTLSClientCertFileEnv = "TEMPORAL_TLS_CERT_FILE"
	DefaultTemporalTLSClientKeyFileEnv  = "TEMPORAL_TLS_KEY_FILE"
	DefaultTemporalMode                 = "local"
	DefaultTemporalConnectWait          = 5 * time.Second
	DefaultTemporalLocalDBFile          = ".onlava/temporal/dev.sqlite"
	defaultTemporalTaskQueuePart        = "onlava"
)

const (
	TemporalVersioningPinned      = "pinned"
	TemporalVersioningAutoUpgrade = "auto_upgrade"
)

type TemporalConfig struct {
	Enabled         bool
	Mode            string
	Namespace       string
	AddressEnv      string
	TaskQueuePrefix string
	PayloadCodec    string
	APIKeyEnv       string
	TLS             TemporalTLSConfig
	Local           TemporalLocalConfig
}

type TemporalTLSConfig struct {
	Enabled           bool
	ServerNameEnv     string
	CACertFileEnv     string
	ClientCertFileEnv string
	ClientKeyFileEnv  string
}

type TemporalLocalConfig struct {
	AutoStart  bool
	DBFilename string
}

type TemporalRuntimeInfo struct {
	Enabled              bool
	Mode                 string
	Address              string
	AddressEnv           string
	AddressEnvSet        bool
	Namespace            string
	NamespaceEnvSet      bool
	TaskQueuePrefix      string
	PayloadCodec         string
	APIKeyEnv            string
	APIKeyEnvSet         bool
	TLSEnabled           bool
	TLSServerNameEnv     string
	TLSServerName        string
	TLSCACertFileEnv     string
	TLSClientCertFileEnv string
	TLSClientKeyFileEnv  string
	DeploymentName       string
	DeploymentEnv        string
	DeploymentEnvSet     bool
	WorkerBuildID        string
	WorkerBuildIDEnv     string
	WorkerBuildIDSet     bool
	Versioning           string
	VersioningEnv        string
	VersioningEnvSet     bool
	LocalAutoStart       bool
	LocalDBFilename      string
	ConnectTimeoutMS     int64
}

type TemporalConnectionStatus struct {
	Checked   bool
	Reachable bool
	Error     string
}

type temporalRuntimeState struct {
	client temporalclient.Client
	info   TemporalRuntimeInfo
}

var activeTemporal struct {
	mu    sync.RWMutex
	state *temporalRuntimeState
}

func ResolveTemporalConfig(appName string, cfg TemporalConfig) TemporalRuntimeInfo {
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = DefaultTemporalMode
	}
	addressEnv := strings.TrimSpace(cfg.AddressEnv)
	if addressEnv == "" {
		addressEnv = DefaultTemporalAddressEnv
	}
	address, addressEnvSet := envValue(addressEnv)
	if address == "" {
		address = DefaultTemporalAddress
	}
	namespace := strings.TrimSpace(cfg.Namespace)
	namespaceEnvSet := false
	if namespace == "" {
		namespace, namespaceEnvSet = envValue(DefaultTemporalNamespaceEnv)
	}
	if namespace == "" {
		namespace = DefaultTemporalNamespace
	}
	taskQueuePrefix := strings.TrimSpace(cfg.TaskQueuePrefix)
	if taskQueuePrefix == "" {
		taskQueuePrefix = defaultTemporalTaskQueuePrefix(appName)
	}
	payloadCodec := strings.TrimSpace(cfg.PayloadCodec)
	if payloadCodec == "" {
		payloadCodec = DefaultTemporalPayloadCodec
	}
	apiKeyEnv := strings.TrimSpace(cfg.APIKeyEnv)
	apiKeyEnvSet := false
	if apiKeyEnv != "" {
		_, apiKeyEnvSet = envValue(apiKeyEnv)
	}
	tlsServerNameEnv := firstNonEmpty(cfg.TLS.ServerNameEnv, DefaultTemporalTLSServerNameEnv)
	tlsCACertFileEnv := firstNonEmpty(cfg.TLS.CACertFileEnv, DefaultTemporalTLSCACertFileEnv)
	tlsClientCertFileEnv := firstNonEmpty(cfg.TLS.ClientCertFileEnv, DefaultTemporalTLSClientCertFileEnv)
	tlsClientKeyFileEnv := firstNonEmpty(cfg.TLS.ClientKeyFileEnv, DefaultTemporalTLSClientKeyFileEnv)
	tlsServerName := ""
	if tlsServerNameEnv != "" {
		tlsServerName, _ = envValue(tlsServerNameEnv)
	}
	deploymentName, deploymentEnvSet := envValue(DefaultTemporalDeploymentEnv)
	if deploymentName == "" {
		deploymentName = defaultTemporalDeploymentName(taskQueuePrefix)
	} else {
		deploymentName = sanitizeTemporalDeploymentName(deploymentName)
	}
	workerBuildID, workerBuildIDSet := envValue(DefaultTemporalBuildIDEnv)
	if workerBuildID == "" {
		workerBuildID = DefaultTemporalBuildID
	}
	versioning, versioningEnvSet := envValue(DefaultTemporalVersioningEnv)
	if versioning == "" {
		versioning = DefaultTemporalVersioning
	}
	versioning = normalizeTemporalVersioning(versioning)
	dbFile := strings.TrimSpace(cfg.Local.DBFilename)
	if dbFile == "" {
		dbFile = DefaultTemporalLocalDBFile
	}
	return TemporalRuntimeInfo{
		Enabled:              cfg.Enabled,
		Mode:                 mode,
		Address:              address,
		AddressEnv:           addressEnv,
		AddressEnvSet:        addressEnvSet,
		Namespace:            namespace,
		NamespaceEnvSet:      namespaceEnvSet,
		TaskQueuePrefix:      taskQueuePrefix,
		PayloadCodec:         payloadCodec,
		APIKeyEnv:            apiKeyEnv,
		APIKeyEnvSet:         apiKeyEnvSet,
		TLSEnabled:           cfg.TLS.Enabled,
		TLSServerNameEnv:     tlsServerNameEnv,
		TLSServerName:        tlsServerName,
		TLSCACertFileEnv:     tlsCACertFileEnv,
		TLSClientCertFileEnv: tlsClientCertFileEnv,
		TLSClientKeyFileEnv:  tlsClientKeyFileEnv,
		DeploymentName:       deploymentName,
		DeploymentEnv:        DefaultTemporalDeploymentEnv,
		DeploymentEnvSet:     deploymentEnvSet,
		WorkerBuildID:        workerBuildID,
		WorkerBuildIDEnv:     DefaultTemporalBuildIDEnv,
		WorkerBuildIDSet:     workerBuildIDSet,
		Versioning:           versioning,
		VersioningEnv:        DefaultTemporalVersioningEnv,
		VersioningEnvSet:     versioningEnvSet,
		LocalAutoStart:       cfg.Local.AutoStart,
		LocalDBFilename:      dbFile,
		ConnectTimeoutMS:     DefaultTemporalConnectWait.Milliseconds(),
	}
}

func StartTemporalRuntime(ctx context.Context, cfg AppConfig) (func(context.Context) error, error) {
	info := ResolveTemporalConfig(cfg.Name, cfg.Temporal)
	if !info.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	if err := validateTemporalVersioning(info); err != nil {
		return nil, err
	}
	if err := validateTemporalPayloadCodec(info); err != nil {
		return nil, err
	}
	client, err := dialTemporal(ctx, info)
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

func ActiveTemporalClient() (temporalclient.Client, TemporalRuntimeInfo, bool) {
	activeTemporal.mu.RLock()
	defer activeTemporal.mu.RUnlock()
	if activeTemporal.state == nil {
		return nil, TemporalRuntimeInfo{}, false
	}
	return activeTemporal.state.client, activeTemporal.state.info, true
}

func CheckTemporalConnection(ctx context.Context, appName string, cfg TemporalConfig) (TemporalRuntimeInfo, TemporalConnectionStatus) {
	info := ResolveTemporalConfig(appName, cfg)
	if !info.Enabled {
		return info, TemporalConnectionStatus{}
	}
	if err := validateTemporalPayloadCodec(info); err != nil {
		return info, TemporalConnectionStatus{
			Checked: true,
			Error:   err.Error(),
		}
	}
	client, err := dialTemporal(ctx, info)
	if err != nil {
		return info, TemporalConnectionStatus{
			Checked: true,
			Error:   err.Error(),
		}
	}
	client.Close()
	return info, TemporalConnectionStatus{
		Checked:   true,
		Reachable: true,
	}
}

func dialTemporal(ctx context.Context, info TemporalRuntimeInfo) (temporalclient.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, cancel := context.WithTimeout(ctx, DefaultTemporalConnectWait)
	defer cancel()
	opts := temporalclient.Options{
		HostPort:      info.Address,
		Namespace:     info.Namespace,
		Identity:      temporalIdentity(info),
		DataConverter: TemporalDataConverter(info),
	}
	if info.APIKeyEnv != "" {
		if apiKey, ok := envValue(info.APIKeyEnv); ok {
			opts.Credentials = temporalclient.NewAPIKeyStaticCredentials(apiKey)
		}
	}
	tlsConfig, err := temporalTLSConfig(info)
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		opts.ConnectionOptions.TLS = tlsConfig
	}
	client, err := temporalclient.DialContext(dialCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("temporal: connect to %s namespace %s: %w", info.Address, info.Namespace, err)
	}
	return client, nil
}

func temporalIdentity(info TemporalRuntimeInfo) string {
	pid := os.Getpid()
	if info.TaskQueuePrefix == "" {
		return fmt.Sprintf("onlava:%d", pid)
	}
	return fmt.Sprintf("%s:%d", info.TaskQueuePrefix, pid)
}

func TemporalWorkerBuildID(info TemporalRuntimeInfo) string {
	buildID := strings.TrimSpace(info.WorkerBuildID)
	if buildID == "" {
		return DefaultTemporalBuildID
	}
	return buildID
}

func TemporalDeploymentName(info TemporalRuntimeInfo) string {
	deploymentName := strings.TrimSpace(info.DeploymentName)
	if deploymentName == "" {
		deploymentName = defaultTemporalDeploymentName(info.TaskQueuePrefix)
	}
	return sanitizeTemporalDeploymentName(deploymentName)
}

func TemporalWorkerOptions(info TemporalRuntimeInfo, role, taskQueue string) temporalworker.Options {
	buildID := TemporalWorkerBuildID(info)
	return temporalworker.Options{
		DisableRegistrationAliasing: true,
		Identity:                    TemporalWorkerIdentity(info, role, taskQueue),
		BuildID:                     buildID,
		DeploymentOptions: temporalworker.DeploymentOptions{
			UseVersioning: true,
			Version: temporalworker.WorkerDeploymentVersion{
				DeploymentName: TemporalDeploymentName(info),
				BuildID:        buildID,
			},
			DefaultVersioningBehavior: TemporalWorkflowVersioningBehavior(info),
		},
	}
}

func TemporalDataConverter(info TemporalRuntimeInfo) converter.DataConverter {
	switch strings.TrimSpace(info.PayloadCodec) {
	case "", DefaultTemporalPayloadCodec:
		return converter.GetDefaultDataConverter()
	default:
		panic(fmt.Sprintf("temporal: unsupported payload codec %q", info.PayloadCodec))
	}
}

func TemporalWorkflowVersioningBehavior(info TemporalRuntimeInfo) workflow.VersioningBehavior {
	switch normalizeTemporalVersioning(info.Versioning) {
	case TemporalVersioningAutoUpgrade:
		return workflow.VersioningBehaviorAutoUpgrade
	default:
		return workflow.VersioningBehaviorPinned
	}
}

func TemporalWorkflowVersioningOverride(info TemporalRuntimeInfo) temporalclient.VersioningOverride {
	switch normalizeTemporalVersioning(info.Versioning) {
	case TemporalVersioningAutoUpgrade:
		return &temporalclient.AutoUpgradeVersioningOverride{}
	default:
		return &temporalclient.PinnedVersioningOverride{
			Version: temporalworker.WorkerDeploymentVersion{
				DeploymentName: TemporalDeploymentName(info),
				BuildID:        TemporalWorkerBuildID(info),
			},
		}
	}
}

func EnsureTemporalWorkerDeploymentCurrentVersion(ctx context.Context, client temporalclient.Client, info TemporalRuntimeInfo) error {
	if client == nil {
		return fmt.Errorf("temporal: missing client for worker deployment versioning")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	updateCtx, cancel := context.WithTimeout(ctx, DefaultTemporalConnectWait)
	defer cancel()
	deploymentName := TemporalDeploymentName(info)
	buildID := TemporalWorkerBuildID(info)
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

func TemporalShouldAutoPromoteWorkers(info TemporalRuntimeInfo) bool {
	return strings.EqualFold(strings.TrimSpace(info.Mode), DefaultTemporalMode)
}

func TemporalWorkerIdentity(info TemporalRuntimeInfo, role, taskQueue string) string {
	role = sanitizeTemporalName(firstNonEmpty(role, "all"))
	taskQueue = sanitizeTemporalName(firstNonEmpty(taskQueue, "default"))
	buildID := sanitizeTemporalName(TemporalWorkerBuildID(info))
	return fmt.Sprintf("onlava:%s:%s:%s:pid-%d:build-%s", TemporalDeploymentName(info), role, taskQueue, os.Getpid(), buildID)
}

func envValue(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func defaultTemporalTaskQueuePrefix(appName string) string {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return defaultTemporalTaskQueuePart
	}
	sanitized := sanitizeTemporalName(appName)
	if sanitized == "" {
		return defaultTemporalTaskQueuePart
	}
	return defaultTemporalTaskQueuePart + "." + sanitized
}

func defaultTemporalDeploymentName(taskQueuePrefix string) string {
	taskQueuePrefix = strings.TrimSpace(taskQueuePrefix)
	if taskQueuePrefix == "" {
		return defaultTemporalTaskQueuePart
	}
	return sanitizeTemporalDeploymentName(taskQueuePrefix)
}

func validateTemporalVersioning(info TemporalRuntimeInfo) error {
	switch normalizeTemporalVersioning(info.Versioning) {
	case TemporalVersioningPinned, TemporalVersioningAutoUpgrade:
		return nil
	default:
		return fmt.Errorf("temporal: unsupported %s %q; expected pinned or auto_upgrade", info.VersioningEnv, info.Versioning)
	}
}

func validateTemporalPayloadCodec(info TemporalRuntimeInfo) error {
	switch strings.TrimSpace(info.PayloadCodec) {
	case "", DefaultTemporalPayloadCodec:
		return nil
	default:
		return fmt.Errorf("temporal: unsupported payload_codec %q; expected %s", info.PayloadCodec, DefaultTemporalPayloadCodec)
	}
}

func temporalTLSConfig(info TemporalRuntimeInfo) (*tls.Config, error) {
	if !info.TLSEnabled {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: info.TLSServerName,
	}
	if caPath, ok := envValue(info.TLSCACertFileEnv); ok {
		pool := x509.NewCertPool()
		data, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: read TLS CA certificate %s: %w", caPath, err)
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("temporal: parse TLS CA certificate %s", caPath)
		}
		cfg.RootCAs = pool
	}
	certPath, certOK := envValue(info.TLSClientCertFileEnv)
	keyPath, keyOK := envValue(info.TLSClientKeyFileEnv)
	if certOK != keyOK {
		return nil, fmt.Errorf("temporal: TLS client certificate and key must be configured together")
	}
	if certOK {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("temporal: load TLS client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func normalizeTemporalVersioning(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "", "pin", "pinned":
		return TemporalVersioningPinned
	case "auto", "autoupgrade", "auto_upgrade":
		return TemporalVersioningAutoUpgrade
	default:
		return value
	}
}

func sanitizeTemporalName(value string) string {
	var b strings.Builder
	lastDot := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDot = false
		case r == '.', r == '-', r == '_':
			if b.Len() > 0 && !lastDot {
				b.WriteByte('.')
				lastDot = true
			}
		default:
			if b.Len() > 0 && !lastDot {
				b.WriteByte('.')
				lastDot = true
			}
		}
	}
	return strings.Trim(b.String(), ".")
}

func sanitizeTemporalDeploymentName(value string) string {
	var b strings.Builder
	lastSep := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSep = false
		case r == '-', r == '_', r == '.':
			if b.Len() > 0 && !lastSep {
				b.WriteByte('-')
				lastSep = true
			}
		default:
			if b.Len() > 0 && !lastSep {
				b.WriteByte('-')
				lastSep = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return defaultTemporalTaskQueuePart
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
