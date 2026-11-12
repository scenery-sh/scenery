package gotarget

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ToolIdentity names what decides the result of a Go command run with env,
// apart from its arguments and the files it reads: the variables the Go tool
// and its C toolchain read, and the go command they select, by resolved path,
// size and modification time, so a replaced toolchain is another identity even
// at the same path. Other variables of a process do not reach the Go tool, so
// two processes with the same identity run the same Go command. It reports
// false when env selects no go command; such an identity must not be trusted.
func ToolIdentity(env []string) ([]string, bool) {
	effective := map[string]string{}
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found && (strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "CGO_") ||
			name == "AR" || name == "CC" || name == "CXX" || name == "PKG_CONFIG" || name == "PATH" || name == "HOME") {
			effective[name] = entry
		}
	}
	identity := slices.Sorted(maps.Values(effective))
	for _, directory := range filepath.SplitList(strings.TrimPrefix(effective["PATH"], "PATH=")) {
		resolved, err := exec.LookPath(filepath.Join(directory, "go"))
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(resolved)
		if err != nil {
			return identity, false
		}
		info, err := os.Stat(real)
		if err != nil {
			return identity, false
		}
		return append(identity, "go="+real, "size="+strconv.FormatInt(info.Size(), 10), "modified="+strconv.FormatInt(info.ModTime().UnixNano(), 10)), true
	}
	return identity, false
}
