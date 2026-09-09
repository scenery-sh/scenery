package storageconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/storagefs"
)

const (
	RuntimeConfigEnv        = "SCENERY_STORAGE_CONFIG"
	RuntimeKind             = "scenery.storage.runtime"
	runtimeSchemaDescriptor = `{"identity":"artifact","namespace":{"root":"string","binding":{"app_id":"string","app_root":"string","worktree_key":"string","user_id":"integer","managed":"boolean"},"incarnation":"string"},"default":"string","stores":{"additionalProperties":{"kind":"string","root":"string","proxy_socket":"string","access":"string","tenant_scoped":"boolean","max_object_bytes":"integer"}}}`
)

type RuntimeConfig struct {
	machine.ArtifactIdentity
	Namespace *Namespace                    `json:"namespace,omitempty"`
	Default   string                        `json:"default,omitempty"`
	Stores    map[string]RuntimeStoreConfig `json:"stores"`
}

// Namespace is the explicit binding shared by a development runtime's stores.
// Independent external local roots omit it and retain no managed purge power.
type Namespace struct {
	Root        string            `json:"root"`
	Binding     storagefs.Binding `json:"binding"`
	Incarnation string            `json:"incarnation"`
}

func (n Namespace) Handle() (*storagefs.Namespace, error) {
	return storagefs.Bind(n.Root, n.Binding, n.Incarnation)
}
func (n Namespace) ProxyBinding() string {
	data, _ := json.Marshal(n)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type RuntimeStoreConfig struct {
	Kind           string `json:"kind"`
	Root           string `json:"root,omitempty"`
	ProxySocket    string `json:"proxy_socket,omitempty"`
	Access         string `json:"access,omitempty"`
	TenantScoped   bool   `json:"tenant_scoped,omitempty"`
	MaxObjectBytes int64  `json:"max_object_bytes,omitempty"`
}

func LoadRuntimeConfigValue(raw string) (RuntimeConfig, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RuntimeConfig{}, false, nil
	}
	if !strings.HasPrefix(raw, "{") {
		data, err := os.ReadFile(raw)
		if err != nil {
			return RuntimeConfig{}, true, fmt.Errorf("read %s: %w", RuntimeConfigEnv, err)
		}
		raw = string(data)
	}
	var cfg RuntimeConfig
	if err := machine.DecodeArtifact([]byte(raw), &cfg, &cfg.ArtifactIdentity, RuntimeKind, runtimeSchemaDescriptor, "regenerate the runtime storage configuration"); err != nil {
		return RuntimeConfig{}, true, fmt.Errorf("decode %s: %w", RuntimeConfigEnv, err)
	}
	if len(cfg.Stores) == 0 {
		return RuntimeConfig{}, false, nil
	}
	if cfg.Namespace != nil {
		if _, err := cfg.Namespace.Handle(); err != nil {
			return RuntimeConfig{}, true, err
		}
	}
	return cfg, true, nil
}

func NewRuntimeIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(RuntimeKind, runtimeSchemaDescriptor)
}
