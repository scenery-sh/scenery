package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
)

type Config struct {
	Name  string      `json:"name"`
	ID    string      `json:"id"`
	Proxy ProxyConfig `json:"proxy"`
}

type ProxyConfig struct {
	Workspace    string `json:"workspace"`
	APIHost      string `json:"api_host"`
	ConsoleHost  string `json:"console_host"`
	MCPHost      string `json:"mcp_host"`
	FrontendHost string `json:"frontend_host"`
}

func DiscoverRoot(start string) (string, Config, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, err
	}
	for {
		path := filepath.Join(dir, "pulse.app")
		if data, err := os.ReadFile(path); err == nil {
			var cfg Config
			if err := json.Unmarshal(data, &cfg); err != nil {
				return "", Config{}, err
			}
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}
			if cfg.Name == "" {
				return "", Config{}, errors.New("pulse.app must define a non-empty name or id")
			}
			return dir, cfg, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", Config{}, errors.New("no pulse.app found in current directory or any parent")
}

func RepoRoot() string {
	_, file, _, _ := goruntime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
