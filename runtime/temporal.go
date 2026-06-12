package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/envpolicy"
)

const (
	DefaultTemporalAddress            = "127.0.0.1:7233"
	DefaultTemporalAddressEnv         = "TEMPORAL_ADDRESS"
	DefaultTemporalNamespace          = "default"
	DefaultTemporalNamespaceEnv       = "TEMPORAL_NAMESPACE"
	DefaultTemporalTaskQueueEnv       = "SCENERY_TEMPORAL_TASK_QUEUE_PREFIX"
	DefaultTemporalTestQueueSuffixEnv = "SCENERY_TEMPORAL_TASK_QUEUE_TEST_SUFFIX"
	DefaultTemporalBuildID            = "dev"
	DefaultTemporalBuildIDEnv         = "SCENERY_BUILD_ID"
	DefaultTemporalDeploymentEnv      = "SCENERY_TEMPORAL_DEPLOYMENT_NAME"
	DefaultTemporalVersioningEnv      = "SCENERY_TEMPORAL_VERSIONING_BEHAVIOR"
	DefaultTemporalVersioning         = "pinned"
	DefaultTemporalPayloadCodec       = "scenery-json-v1"
	DefaultTemporalAPIKeyEnv          = "TEMPORAL_API_KEY"
	DefaultTemporalTLSServerNameEnv   = "TEMPORAL_TLS_SERVER_NAME"
	DefaultTemporalTLSCACertFileEnv   = "TEMPORAL_TLS_CA_CERT_FILE"
	DefaultTemporalTLSCertFileEnv     = "TEMPORAL_TLS_CERT_FILE"
	DefaultTemporalTLSKeyFileEnv      = "TEMPORAL_TLS_KEY_FILE"
	DefaultTemporalHostReportingEnv   = "SCENERY_TEMPORAL_HOST_RESOURCE_REPORTING"
	DefaultScenerySessionIDEnv        = "SCENERY_SESSION_ID"
	DefaultSceneryRuntimeEnv          = "SCENERY_RUNTIME_ENV"
	DefaultTemporalMode               = "local"
	DefaultTemporalConnectWait        = 5 * time.Second
	DefaultTemporalLocalDBFile        = ".scenery/temporal/dev.db"
	defaultTemporalTaskQueuePart      = "scenery"
)

var temporalTestQueueSuffix = struct {
	once  sync.Once
	value string
}{}

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
	TaskQueueEnv     string
	TaskQueueEnvSet  bool
	SessionID        string
	SessionIDEnv     string
	SessionIDEnvSet  bool
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
	HostReporting    bool
	HostReportingEnv string
	HostReportingSet bool
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
	taskQueueEnvValue, taskQueueEnvSet := envValue(DefaultTemporalTaskQueueEnv)
	if taskQueueEnvSet {
		taskQueuePrefix = taskQueueEnvValue
	}
	if taskQueuePrefix == "" {
		taskQueuePrefix = defaultTemporalTaskQueuePrefix(appName)
	}
	sessionID, sessionIDEnvSet := envValue(DefaultScenerySessionIDEnv)
	if temporalRuntimeEnvIsTest() {
		suffix := TemporalTestTaskQueueSuffix()
		taskQueuePrefix = temporalTestTaskQueuePrefix(taskQueuePrefix, suffix)
		if strings.TrimSpace(sessionID) == "" {
			sessionID = temporalTestSessionID(suffix)
		}
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
	hostReporting, hostReportingSet := temporalHostResourceReportingFromEnv()
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
		TaskQueueEnv:     DefaultTemporalTaskQueueEnv,
		TaskQueueEnvSet:  taskQueueEnvSet,
		SessionID:        sessionID,
		SessionIDEnv:     DefaultScenerySessionIDEnv,
		SessionIDEnvSet:  sessionIDEnvSet,
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
		HostReporting:    hostReporting,
		HostReportingEnv: DefaultTemporalHostReportingEnv,
		HostReportingSet: hostReportingSet,
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

func temporalRuntimeEnvIsTest() bool {
	return strings.EqualFold(strings.TrimSpace(envpolicy.Get(DefaultSceneryRuntimeEnv)), "test")
}

// TemporalTestTaskQueueSuffix returns the process-stable suffix used to isolate test-marked Temporal queues.
func TemporalTestTaskQueueSuffix() string {
	if suffix, ok := envValue(DefaultTemporalTestQueueSuffixEnv); ok {
		return sanitizeTemporalName(suffix)
	}
	temporalTestQueueSuffix.once.Do(func() {
		temporalTestQueueSuffix.value = randomTemporalTestTaskQueueSuffix()
	})
	return temporalTestQueueSuffix.value
}

func temporalTestTaskQueuePrefix(prefix, suffix string) string {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), ".")
	if prefix == "" {
		prefix = defaultTemporalTaskQueuePart
	}
	suffix = sanitizeTemporalName(suffix)
	if suffix == "" {
		return prefix
	}
	marker := ".test." + suffix
	if strings.HasSuffix(prefix, marker) {
		return prefix
	}
	return prefix + marker
}

func temporalTestSessionID(suffix string) string {
	suffix = sanitizeTemporalName(suffix)
	if suffix == "" {
		return "test"
	}
	return "test." + suffix
}

func randomTemporalTestTaskQueueSuffix() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return sanitizeTemporalName(fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano()))
}

func validateTemporalPayloadCodec(profile string) error {
	if strings.TrimSpace(profile) == DefaultTemporalPayloadCodec {
		return nil
	}
	return fmt.Errorf("temporal: payload_codec must be %q", DefaultTemporalPayloadCodec)
}

func ValidateTemporalPayloadCodec(profile string) error {
	return validateTemporalPayloadCodec(profile)
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

func SessionScopedTemporalTaskQueue(info TemporalRuntimeInfo, queue string) string {
	queue = strings.TrimSpace(queue)
	if queue == "" || strings.TrimSpace(info.SessionID) == "" {
		return queue
	}
	prefix := strings.TrimSpace(info.TaskQueuePrefix)
	if prefix == "" {
		prefix = defaultTemporalTaskQueuePart
	}
	prefix = strings.TrimSuffix(prefix, ".")
	if queue == prefix || strings.HasPrefix(queue, prefix+".") {
		return queue
	}
	queue = sanitizeTemporalName(queue)
	if queue == "" {
		return prefix
	}
	return prefix + "." + queue
}

func SessionScopedTemporalTaskQueueFromEnv(queue string) string {
	prefix, _ := envValue(DefaultTemporalTaskQueueEnv)
	return SessionScopedTemporalTaskQueue(ResolveTemporalConfig("", TemporalConfig{TaskQueuePrefix: prefix}), queue)
}

func TemporalHostResourceReportingEnabled(info TemporalRuntimeInfo) bool {
	if info.HostReportingSet {
		return info.HostReporting
	}
	return true
}

func ShouldAutoPromoteTemporalWorkerDeployment(info TemporalRuntimeInfo) bool {
	mode := strings.TrimSpace(info.Mode)
	if mode == "" {
		mode = DefaultTemporalMode
	}
	return strings.EqualFold(mode, DefaultTemporalMode)
}

func TemporalWorkerIdentity(info TemporalRuntimeInfo, role, taskQueue string) string {
	role = sanitizeTemporalName(firstNonEmpty(role, "all"))
	taskQueue = sanitizeTemporalName(firstNonEmpty(taskQueue, "default"))
	buildID := sanitizeTemporalName(TemporalWorkerBuildID(info))
	return fmt.Sprintf("scenery:%s:%s:%s:pid-%d:build-%s", TemporalDeploymentName(info), role, taskQueue, os.Getpid(), buildID)
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

func temporalHostResourceReportingFromEnv() (bool, bool) {
	value, ok := envpolicy.Lookup(DefaultTemporalHostReportingEnv)
	if !ok {
		return true, false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return true, false
	}
	switch value {
	case "0", "false", "no", "off", "disable", "disabled":
		return false, true
	default:
		return true, true
	}
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

func ValidateTemporalVersioning(info TemporalRuntimeInfo) error {
	return validateTemporalVersioning(info)
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

func NormalizeTemporalVersioning(value string) string {
	return normalizeTemporalVersioning(value)
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

func SanitizeTemporalName(value string) string {
	return sanitizeTemporalName(value)
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
