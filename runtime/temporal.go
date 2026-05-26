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
	temporalworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	DefaultTemporalAddress          = "127.0.0.1:7233"
	DefaultTemporalAddressEnv       = "TEMPORAL_ADDRESS"
	DefaultTemporalNamespace        = "default"
	DefaultTemporalNamespaceEnv     = "TEMPORAL_NAMESPACE"
	DefaultTemporalBuildID          = "dev"
	DefaultTemporalBuildIDEnv       = "ONLAVA_BUILD_ID"
	DefaultTemporalDeploymentEnv    = "ONLAVA_TEMPORAL_DEPLOYMENT_NAME"
	DefaultTemporalVersioningEnv    = "ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR"
	DefaultTemporalVersioning       = "pinned"
	DefaultTemporalPayloadCodec     = "onlava-json-v1"
	DefaultTemporalAPIKeyEnv        = "TEMPORAL_API_KEY"
	DefaultTemporalTLSServerNameEnv = "TEMPORAL_TLS_SERVER_NAME"
	DefaultTemporalTLSCACertFileEnv = "TEMPORAL_TLS_CA_CERT_FILE"
	DefaultTemporalTLSCertFileEnv   = "TEMPORAL_TLS_CERT_FILE"
	DefaultTemporalTLSKeyFileEnv    = "TEMPORAL_TLS_KEY_FILE"
	DefaultTemporalMode             = "local"
	DefaultTemporalConnectWait      = 5 * time.Second
	DefaultTemporalLocalDBFile      = ".onlava/temporal/dev.sqlite"
	defaultTemporalTaskQueuePart    = "onlava"
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
	Enabled          bool
	Mode             string
	Address          string
	AddressEnv       string
	AddressEnvSet    bool
	Namespace        string
	NamespaceEnvSet  bool
	TaskQueuePrefix  string
	PayloadCodec     string
	APIKeyEnv        string
	APIKeyEnvSet     bool
	TLSEnabled       bool
	TLSServerName    string
	TLSServerNameEnv string
	TLSServerNameSet bool
	TLSCACertFileEnv string
	TLSCACertFileSet bool
	TLSCertFileEnv   string
	TLSCertFileSet   bool
	TLSKeyFileEnv    string
	TLSKeyFileSet    bool
	DeploymentName   string
	DeploymentEnv    string
	DeploymentEnvSet bool
	WorkerBuildID    string
	WorkerBuildIDEnv string
	WorkerBuildIDSet bool
	Versioning       string
	VersioningEnv    string
	VersioningEnvSet bool
	LocalAutoStart   bool
	LocalDBFilename  string
	ConnectTimeoutMS int64
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
	if apiKeyEnv == "" {
		apiKeyEnv = DefaultTemporalAPIKeyEnv
	}
	_, apiKeyEnvSet := envValue(apiKeyEnv)
	tlsServerNameEnv := strings.TrimSpace(cfg.TLS.ServerNameEnv)
	if tlsServerNameEnv == "" {
		tlsServerNameEnv = DefaultTemporalTLSServerNameEnv
	}
	tlsServerName, tlsServerNameSet := envValue(tlsServerNameEnv)
	tlsCAEnv := strings.TrimSpace(cfg.TLS.CACertFileEnv)
	if tlsCAEnv == "" {
		tlsCAEnv = DefaultTemporalTLSCACertFileEnv
	}
	_, tlsCASet := envValue(tlsCAEnv)
	tlsCertEnv := strings.TrimSpace(cfg.TLS.ClientCertFileEnv)
	if tlsCertEnv == "" {
		tlsCertEnv = DefaultTemporalTLSCertFileEnv
	}
	_, tlsCertSet := envValue(tlsCertEnv)
	tlsKeyEnv := strings.TrimSpace(cfg.TLS.ClientKeyFileEnv)
	if tlsKeyEnv == "" {
		tlsKeyEnv = DefaultTemporalTLSKeyFileEnv
	}
	_, tlsKeySet := envValue(tlsKeyEnv)
	tlsEnabled := cfg.TLS.Enabled || tlsServerNameSet || tlsCASet || tlsCertSet || tlsKeySet
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
		Enabled:          cfg.Enabled,
		Mode:             mode,
		Address:          address,
		AddressEnv:       addressEnv,
		AddressEnvSet:    addressEnvSet,
		Namespace:        namespace,
		NamespaceEnvSet:  namespaceEnvSet,
		TaskQueuePrefix:  taskQueuePrefix,
		PayloadCodec:     payloadCodec,
		APIKeyEnv:        apiKeyEnv,
		APIKeyEnvSet:     apiKeyEnvSet,
		TLSEnabled:       tlsEnabled,
		TLSServerName:    tlsServerName,
		TLSServerNameEnv: tlsServerNameEnv,
		TLSServerNameSet: tlsServerNameSet,
		TLSCACertFileEnv: tlsCAEnv,
		TLSCACertFileSet: tlsCASet,
		TLSCertFileEnv:   tlsCertEnv,
		TLSCertFileSet:   tlsCertSet,
		TLSKeyFileEnv:    tlsKeyEnv,
		TLSKeyFileSet:    tlsKeySet,
		DeploymentName:   deploymentName,
		DeploymentEnv:    DefaultTemporalDeploymentEnv,
		DeploymentEnvSet: deploymentEnvSet,
		WorkerBuildID:    workerBuildID,
		WorkerBuildIDEnv: DefaultTemporalBuildIDEnv,
		WorkerBuildIDSet: workerBuildIDSet,
		Versioning:       versioning,
		VersioningEnv:    DefaultTemporalVersioningEnv,
		VersioningEnvSet: versioningEnvSet,
		LocalAutoStart:   cfg.Local.AutoStart,
		LocalDBFilename:  dbFile,
		ConnectTimeoutMS: DefaultTemporalConnectWait.Milliseconds(),
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

func DialTemporal(ctx context.Context, info TemporalRuntimeInfo) (temporalclient.Client, error) {
	return dialTemporal(ctx, info)
}

func dialTemporal(ctx context.Context, info TemporalRuntimeInfo) (temporalclient.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options, err := temporalClientOptions(info)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, DefaultTemporalConnectWait)
	defer cancel()
	client, err := temporalclient.DialContext(dialCtx, options)
	if err != nil {
		return nil, fmt.Errorf("temporal: connect to %s namespace %s: %w", info.Address, info.Namespace, err)
	}
	return client, nil
}

func temporalClientOptions(info TemporalRuntimeInfo) (temporalclient.Options, error) {
	if err := validateTemporalPayloadCodec(info.PayloadCodec); err != nil {
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
	return options, nil
}

func validateTemporalPayloadCodec(profile string) error {
	if strings.TrimSpace(profile) == DefaultTemporalPayloadCodec {
		return nil
	}
	return fmt.Errorf("temporal: payload_codec must be %q", DefaultTemporalPayloadCodec)
}

func temporalTLSConfig(info TemporalRuntimeInfo) (*tls.Config, bool, error) {
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

func TemporalWorkflowVersioningBehavior(info TemporalRuntimeInfo) workflow.VersioningBehavior {
	switch normalizeTemporalVersioning(info.Versioning) {
	case TemporalVersioningAutoUpgrade:
		return workflow.VersioningBehaviorAutoUpgrade
	default:
		return workflow.VersioningBehaviorPinned
	}
}

func ShouldAutoPromoteTemporalWorkerDeployment(info TemporalRuntimeInfo) bool {
	mode := strings.TrimSpace(info.Mode)
	if mode == "" {
		mode = DefaultTemporalMode
	}
	return strings.EqualFold(mode, DefaultTemporalMode)
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
