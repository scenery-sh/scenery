package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	envAgentHome       = "ONLAVA_AGENT_HOME"
	envAgentSocket     = "ONLAVA_AGENT_SOCKET"
	envAgentRouterAddr = "ONLAVA_AGENT_ROUTER_ADDR"
	envAgentDisable    = "ONLAVA_AGENT_DISABLE"

	defaultRouterAddr = "127.0.0.1:9440"
)

type Paths struct {
	Home         string
	RunDir       string
	AgentDir     string
	SocketPath   string
	StatePath    string
	RegistryPath string
	LogPath      string
}

func DefaultPaths() (Paths, error) {
	home := strings.TrimSpace(os.Getenv(envAgentHome))
	if home == "" {
		if cacheRoot := strings.TrimSpace(os.Getenv("ONLAVA_DEV_CACHE_DIR")); cacheRoot != "" {
			home = filepath.Join(cacheRoot, "agent-home")
		} else {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return Paths{}, err
			}
			home = filepath.Join(userHome, ".onlava")
		}
	}
	home = filepath.Clean(home)
	runDir := filepath.Join(home, "run")
	agentDir := filepath.Join(home, "agent")
	socketPath := strings.TrimSpace(os.Getenv(envAgentSocket))
	if socketPath == "" {
		socketPath = filepath.Join(runDir, "agent.sock")
		if len(socketPath) > 100 {
			sum := sha256.Sum256([]byte(home))
			socketPath = filepath.Join(os.TempDir(), "onlava-agent-"+hex.EncodeToString(sum[:])[:12]+".sock")
		}
	}
	return Paths{
		Home:         home,
		RunDir:       runDir,
		AgentDir:     agentDir,
		SocketPath:   filepath.Clean(socketPath),
		StatePath:    filepath.Join(runDir, "agent.json"),
		RegistryPath: filepath.Join(agentDir, "sessions.json"),
		LogPath:      filepath.Join(agentDir, "agent.log"),
	}, nil
}

func RouterAddrFromEnv() string {
	if value := strings.TrimSpace(os.Getenv(envAgentRouterAddr)); value != "" {
		return value
	}
	return defaultRouterAddr
}

func DisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envAgentDisable))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func EnsureDirs(paths Paths) error {
	if paths.RunDir == "" || paths.AgentDir == "" {
		return fmt.Errorf("agent paths are incomplete")
	}
	if err := os.MkdirAll(paths.RunDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(paths.AgentDir, 0o755)
}

func StateRoot(appRoot, sessionID string) string {
	return filepath.Join(appRoot, ".onlava", "sessions", sessionID)
}
