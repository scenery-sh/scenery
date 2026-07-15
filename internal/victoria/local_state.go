package victoria

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/atomicfile"
)

const sceneryLocalStateGitignore = `# Managed by scenery. Local runtime state may include downloaded binaries,
# databases, logs, generated build outputs, and other machine-local files.
*
!.gitignore
`

func ensureLocalStateDirIgnored(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, ".gitignore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil {
		if localStateGitignoreCovers(current) {
			return nil
		}
		next := strings.TrimRight(string(current), "\n")
		if next != "" {
			next += "\n\n"
		}
		next += sceneryLocalStateGitignore
		return atomicWriteFile(path, []byte(next), 0o644)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicWriteFile(path, []byte(sceneryLocalStateGitignore), 0o644)
}

func localStateGitignoreCovers(data []byte) bool {
	hasAll := false
	hasSelfException := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "*" {
			hasAll = true
		}
		if line == "!.gitignore" {
			hasSelfException = true
		}
	}
	return hasAll && hasSelfException
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	return atomicfile.Write(path, data, mode, atomicfile.Options{SyncFile: true, SyncDir: true})
}
