package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/envpolicy"
)

const (
	envAgentHome       = "SCENERY_AGENT_HOME"
	envAgentSocket     = "SCENERY_AGENT_SOCKET"
	envAgentRouterAddr = "SCENERY_AGENT_ROUTER_ADDR"
	envAgentRouterTLS  = "SCENERY_AGENT_ROUTER_TLS"
	envAgentTrust      = "SCENERY_AGENT_TRUST"
	envAgentDisable    = "SCENERY_AGENT_DISABLE"

	defaultRouterAddr = "127.0.0.1:9440"
)

type Paths struct {
	Home           string
	RunDir         string
	AgentDir       string
	EdgeDir        string
	SocketPath     string
	StatePath      string
	EdgeStatePath  string
	EdgeTokenPath  string
	EdgeTargetPath string
	EdgeConfigPath string
	EdgeLogPath    string
	RegistryPath   string
	LogPath        string
}

func DefaultPaths() (Paths, error) {
	home := strings.TrimSpace(envpolicy.Get(envAgentHome))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = filepath.Join(userHome, ".scenery")
	}
	paths := PathsForHome(home)
	if socketPath := strings.TrimSpace(envpolicy.Get(envAgentSocket)); socketPath != "" {
		paths.SocketPath = filepath.Clean(socketPath)
	}
	return paths, nil
}

// PathsForHome derives the agent path layout for an explicit home directory
// without consulting the environment.
func PathsForHome(home string) Paths {
	home = filepath.Clean(home)
	runDir := filepath.Join(home, "run")
	agentDir := filepath.Join(home, "agent")
	edgeDir := filepath.Join(agentDir, "edge")
	socketPath := filepath.Join(runDir, "agent.sock")
	if len(socketPath) > 100 {
		sum := sha256.Sum256([]byte(home))
		socketPath = filepath.Join(os.TempDir(), "scenery-agent-"+hex.EncodeToString(sum[:])[:12]+".sock")
	}
	return Paths{
		Home:           home,
		RunDir:         runDir,
		AgentDir:       agentDir,
		EdgeDir:        edgeDir,
		SocketPath:     socketPath,
		StatePath:      filepath.Join(runDir, "agent.json"),
		EdgeStatePath:  filepath.Join(runDir, "edge.json"),
		EdgeTargetPath: filepath.Join(runDir, "edge-target.json"),
		EdgeTokenPath:  filepath.Join(edgeDir, "edge-token"),
		EdgeConfigPath: filepath.Join(edgeDir, "Caddyfile"),
		EdgeLogPath:    filepath.Join(edgeDir, "caddy.log"),
		RegistryPath:   filepath.Join(agentDir, "sessions.json"),
		LogPath:        filepath.Join(agentDir, "agent.log"),
	}
}

func RouterAddrFromEnv() string {
	if value := strings.TrimSpace(envpolicy.Get(envAgentRouterAddr)); value != "" {
		return value
	}
	return defaultRouterAddr
}

func RouterTLSFromEnv() bool {
	return envEnabled(envAgentRouterTLS)
}

func RouterTLSDefault() bool {
	value, ok := envpolicy.Lookup(envAgentRouterTLS)
	if !ok || strings.TrimSpace(value) == "" {
		return true
	}
	return envValueEnabled(value)
}

func TrustFromEnv() bool {
	return envEnabled(envAgentTrust)
}

func DisabledByEnv() bool {
	return envEnabled(envAgentDisable)
}

func envEnabled(name string) bool {
	return envValueEnabled(envpolicy.Get(name))
}

func envValueEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
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
	if err := os.MkdirAll(paths.AgentDir, 0o755); err != nil {
		return err
	}
	if paths.EdgeDir != "" {
		return os.MkdirAll(paths.EdgeDir, 0o700)
	}
	return nil
}

func StateRoot(appRoot, sessionID string) string {
	return filepath.Join(appRoot, ".scenery", "sessions", sessionID)
}
