package agent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestListeningOnAnOverlongSocketPathNamesTheLimit(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), strings.Repeat("a", 120), "agent.sock")
	_, err := listenUnixSocket(path)
	refused, ok := errors.AsType[*SocketPathError](err)
	if !ok || refused.ExitCode() != 3 || refused.Limit != unixSocketPathLimit() || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("overlong socket path: %v", err)
	}
}
