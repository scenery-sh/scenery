package runtime

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/envpolicy"
)

func ListenAddrFromEnv() string {
	if value := envpolicy.Get("SCENERY_LISTEN_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:4000"
}

func listenRuntime(network, addr string) (net.Listener, error) {
	switch network {
	case "", "tcp":
		return net.Listen("tcp", addr)
	case "unix":
		if strings.TrimSpace(addr) == "" {
			return nil, fmt.Errorf("runtime: unix listen address is empty")
		}
		if err := os.MkdirAll(filepath.Dir(addr), 0o755); err != nil {
			return nil, err
		}
		if err := os.Remove(addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return net.Listen("unix", addr)
	default:
		return nil, fmt.Errorf("runtime: unsupported SCENERY_LISTEN_NETWORK %q", network)
	}
}

func launchedBySupervisor() bool {
	return envpolicy.Get("SCENERY_DEV_SUPERVISOR") == "1"
}
