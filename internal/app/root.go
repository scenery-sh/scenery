package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
)

type Config struct {
	Name           string              `json:"name"`
	ID             string              `json:"id"`
	Proxy          ProxyConfig         `json:"proxy"`
	Observability  ObservabilityConfig `json:"observability"`
	EnableDBStudio bool                `json:"-"`
}

type ProxyConfig struct {
	Workspace   string                    `json:"workspace"`
	APIHost     string                    `json:"api_host"`
	ConsoleHost string                    `json:"console_host"`
	MCPHost     string                    `json:"mcp_host"`
	Frontends   map[string]FrontendConfig `json:"frontends"`
}

type FrontendConfig struct {
	Host     string `json:"host"`
	Root     string `json:"root"`
	Upstream string `json:"upstream"`
}

type ObservabilityConfig struct {
	Logs    EndpointFilterConfig `json:"logs"`
	Tracing EndpointFilterConfig `json:"tracing"`
}

type EndpointFilterConfig struct {
	IncludeEndpoints []string `json:"include_endpoints"`
	ExcludeEndpoints []string `json:"exclude_endpoints"`
}

func DiscoverRoot(start string) (string, Config, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, err
	}
	for {
		path := filepath.Join(dir, ".onlava.json")
		if data, err := os.ReadFile(path); err == nil {
			var cfg Config
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&cfg); err != nil {
				return "", Config{}, err
			}
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}
			if cfg.Name == "" {
				return "", Config{}, errors.New(".onlava.json must define a non-empty name or id")
			}
			return dir, cfg, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", Config{}, errors.New("no .onlava.json found in current directory or any parent")
}

func RepoRoot() string {
	_, file, _, _ := goruntime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
