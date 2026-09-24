package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/envpolicy"
)

// ConfigSnapshotFDEnv names the inherited descriptor carrying this process's
// configuration snapshot. The supervisor allocates the descriptor; its number
// is never assumed.
const ConfigSnapshotFDEnv = "SCENERY_CONFIG_SNAPSHOT_FD"

// ConfigSnapshotKind identifies the runtime configuration snapshot.
const ConfigSnapshotKind = "scenery.config.snapshot"

// configSecretPrefix marks secret references resolved from the snapshot.
const configSecretPrefix = "config:"

// maxConfigSnapshotBytes bounds one snapshot.
const maxConfigSnapshotBytes = 4 << 20

// ConfigSnapshot is the validated configuration of one process generation:
// the application, environment and revision it was resolved from, and exactly
// the values and secrets its consumers need.
type ConfigSnapshot struct {
	Kind            string                     `json:"kind"`
	AppID           string                     `json:"app_id"`
	Environment     string                     `json:"environment"`
	Revision        string                     `json:"revision"`
	CatalogRevision string                     `json:"catalog_revision"`
	Consumer        string                     `json:"consumer"`
	Values          map[string]json.RawMessage `json:"values"`
	Secrets         map[string][]byte          `json:"secrets"`
	// Public names the values this process serves as the application's
	// public configuration.
	Public []string `json:"public"`
}

func init() { contract.SetSecretRevealer(RevealSecret) }

var configSnapshotState struct {
	once     sync.Once
	snapshot *ConfigSnapshot
	err      error
}

// LoadConfigSnapshot decodes this process's snapshot once and closes its
// descriptor, so no descendant inherits it. A process started without one has
// an empty configuration: required inputs then fail when resolved.
func LoadConfigSnapshot() (*ConfigSnapshot, error) {
	configSnapshotState.once.Do(func() {
		configSnapshotState.snapshot, configSnapshotState.err = loadConfigSnapshotFromEnvironment()
	})
	return configSnapshotState.snapshot, configSnapshotState.err
}

func loadConfigSnapshotFromEnvironment() (*ConfigSnapshot, error) {
	text, present := envpolicy.Lookup(ConfigSnapshotFDEnv)
	if !present || strings.TrimSpace(text) == "" {
		return nil, nil
	}
	_ = envpolicy.Unset(ConfigSnapshotFDEnv)
	fd, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("runtime: %s names an invalid descriptor", ConfigSnapshotFDEnv)
	}
	file := os.NewFile(uintptr(fd), "scenery-config-snapshot")
	if file == nil {
		return nil, fmt.Errorf("runtime: configuration snapshot descriptor is unavailable")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("runtime: read configuration snapshot: %w", err)
	}
	if len(data) > maxConfigSnapshotBytes {
		return nil, fmt.Errorf("runtime: configuration snapshot exceeds %d bytes", maxConfigSnapshotBytes)
	}
	return DecodeConfigSnapshot(data)
}

// DecodeConfigSnapshot strictly decodes one snapshot.
func DecodeConfigSnapshot(data []byte) (*ConfigSnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot ConfigSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("runtime: decode configuration snapshot: %w", err)
	}
	if snapshot.Kind != ConfigSnapshotKind || snapshot.AppID == "" || snapshot.Environment == "" || snapshot.Revision == "" {
		return nil, errors.New("runtime: configuration snapshot identity is incomplete")
	}
	if snapshot.Values == nil {
		snapshot.Values = map[string]json.RawMessage{}
	}
	if snapshot.Secrets == nil {
		snapshot.Secrets = map[string][]byte{}
	}
	return &snapshot, nil
}

// DeploymentConfigValue returns the contract wire value of a configured
// input. ok is false when the snapshot has no value for key. A null value is
// an optional input configured as absent.
func DeploymentConfigValue(key string) (json.RawMessage, bool, error) {
	snapshot, err := LoadConfigSnapshot()
	if err != nil || snapshot == nil {
		return nil, false, err
	}
	value, ok := snapshot.Values[key]
	return value, ok, nil
}

// ResolveDeploymentConfig decodes a configured input into target. A required
// input without a value is an error that names the key; an optional one stays
// unset.
func ResolveDeploymentConfig(key string, target any, typeExpression string, optional bool) error {
	value, ok, err := DeploymentConfigValue(key)
	if err != nil {
		return err
	}
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if optional {
			return nil
		}
		return fmt.Errorf("configuration input %s is not configured; set it with scenery config set %s --env <environment>", key, key)
	}
	if err := contract.UnmarshalContractValue(value, target, typeExpression); err != nil {
		return fmt.Errorf("configuration input %s: %w", key, err)
	}
	return nil
}

// ConfigSecretRef names an environment-configured secret.
func ConfigSecretRef(key string) contract.SecretRef {
	return contract.SecretRef{Address: configSecretPrefix + key}
}

// RevealSecret returns the plaintext of a secret reference that the selected
// environment configured. The bytes are a copy the caller owns.
func RevealSecret(ref contract.SecretRef) ([]byte, bool, error) {
	key, ok := strings.CutPrefix(ref.Address, configSecretPrefix)
	if !ok {
		return nil, false, fmt.Errorf("secret %s is not an environment configuration secret", ref.Address)
	}
	return FrameworkConfigSecret(key)
}

// FrameworkConfigSecret returns a configured secret by key, for framework
// owners such as standard authentication.
func FrameworkConfigSecret(key string) ([]byte, bool, error) {
	snapshot, err := LoadConfigSnapshot()
	if err != nil || snapshot == nil {
		return nil, false, err
	}
	value, ok := snapshot.Secrets[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

// FrameworkConfigString returns a configured non-secret string by key.
func FrameworkConfigString(key string) (string, bool, error) {
	value, ok, err := DeploymentConfigValue(key)
	if err != nil || !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", false, err
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", false, fmt.Errorf("configuration input %s is not a string", key)
	}
	return text, true, nil
}

// CurrentConfigRevision reports the snapshot revision this process runs.
func CurrentConfigRevision() string {
	snapshot, err := LoadConfigSnapshot()
	if err != nil || snapshot == nil {
		return ""
	}
	return snapshot.Revision
}

// PublicConfigPath serves the application's public configuration: the
// values of inputs declared public, pinned to the configuration revision the
// process runs. Frontends read it at startup instead of build-time variables.
const PublicConfigPath = "/__scenery/public-config"

// PublicConfigKind identifies the public configuration response.
const PublicConfigKind = "scenery.public-config"

type publicConfigDocument struct {
	Kind     string                     `json:"kind"`
	Revision string                     `json:"revision"`
	Values   map[string]json.RawMessage `json:"values"`
}

// publicConfiguration returns this process's public values and revision.
func publicConfiguration() (publicConfigDocument, error) {
	document := publicConfigDocument{Kind: PublicConfigKind, Values: map[string]json.RawMessage{}}
	snapshot, err := LoadConfigSnapshot()
	if err != nil || snapshot == nil {
		return document, err
	}
	document.Revision = snapshot.Revision
	for _, key := range snapshot.Public {
		if value, ok := snapshot.Values[key]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			document.Values[key] = value
		}
	}
	return document, nil
}
