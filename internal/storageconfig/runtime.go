package storageconfig

import (
	"fmt"
	"os"
	"strings"

	"scenery.sh/internal/machine"
)

const (
	RuntimeConfigEnv        = "SCENERY_STORAGE_CONFIG"
	RuntimeKind             = "scenery.storage.runtime"
	runtimeSchemaDescriptor = `{"identity":"artifact","cell_id":"string","default":"string","stores":"runtime-stores"}`
)

type RuntimeConfig struct {
	machine.ArtifactIdentity
	CellID  string                        `json:"cell_id"`
	Default string                        `json:"default,omitempty"`
	Stores  map[string]RuntimeStoreConfig `json:"stores"`
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
	return cfg, true, nil
}

func NewRuntimeIdentity() machine.ArtifactIdentity {
	return machine.NewArtifactIdentity(RuntimeKind, runtimeSchemaDescriptor)
}
