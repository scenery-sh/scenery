package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"scenery.sh/internal/atomicfile"
)

// buildInputGraphFile keeps a workspace's retained Go build input graph for
// the next process that builds the workspace. It is disposable: a graph that
// is absent, unreadable or of another tool identity is listed again.
const buildInputGraphFile = ".scenery-build-inputs.json"

type retainedBuildInputGraphDocument struct {
	Version int
	ToolKey string
	Graph   *retainedBuildInputGraph
}

// goToolEnvironment keeps the effective variables the Go tool and its C
// toolchain read, sorted; other variables of a process do not reach them.
func goToolEnvironment(env []string) []string {
	effective := map[string]string{}
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found && (strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "CGO_") ||
			name == "AR" || name == "CC" || name == "CXX" || name == "PKG_CONFIG" || name == "PATH" || name == "HOME") {
			effective[name] = entry
		}
	}
	return slices.Sorted(maps.Values(effective))
}

// goToolIdentity is goToolEnvironment together with the go command those
// variables select, by resolved path, size and modification time, so a
// replaced toolchain is another identity even at the same path.
func goToolIdentity(env []string) []string {
	identity := goToolEnvironment(env)
	search := ""
	for _, entry := range identity {
		if value, found := strings.CutPrefix(entry, "PATH="); found {
			search = value
		}
	}
	for _, directory := range filepath.SplitList(search) {
		candidate := filepath.Join(directory, "go")
		if resolved, err := exec.LookPath(candidate); err == nil {
			if real, err := filepath.EvalSymlinks(resolved); err == nil {
				if info, err := os.Stat(real); err == nil {
					return append(identity, "go="+real, "size="+strconv.FormatInt(info.Size(), 10), "modified="+strconv.FormatInt(info.ModTime().UnixNano(), 10))
				}
			}
			break
		}
	}
	return append(identity, "go=unresolved")
}

// goToolIdentityKey names a Go listing by its arguments and tool identity. It
// is what one process may trust of a listing another process retained; the
// retained graph then proves its directories, selections and module files
// current exactly as a graph of this process does.
func goToolIdentityKey(args, env []string) string {
	identity := goToolIdentity(env)
	if identity[len(identity)-1] == "go=unresolved" {
		return ""
	}
	encoded, err := json.Marshal(struct {
		Arguments []string
		Tool      []string
	}{args, identity})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func loadRetainedBuildInputGraph(workspace, toolKey, graphKey string) *retainedBuildInputGraph {
	if toolKey == "" || !filepath.IsAbs(workspace) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(workspace, buildInputGraphFile))
	if err != nil {
		return nil
	}
	var document retainedBuildInputGraphDocument
	if json.Unmarshal(data, &document) != nil || document.Version != 1 || document.ToolKey != toolKey ||
		document.Graph == nil || len(document.Graph.Output) == 0 || document.Graph.Directories == nil || document.Graph.Selection == nil {
		return nil
	}
	// The in-process key also names this process's whole environment.
	document.Graph.Key = graphKey
	return document.Graph
}

func saveRetainedBuildInputGraph(workspace, toolKey string, state *retainedBuildInputGraph) {
	if toolKey == "" || state == nil || !filepath.IsAbs(workspace) {
		return
	}
	path := filepath.Join(workspace, buildInputGraphFile)
	data, err := json.Marshal(retainedBuildInputGraphDocument{Version: 1, ToolKey: toolKey, Graph: state})
	if err != nil || atomicfile.Write(path, data, 0o644, atomicfile.Options{}) != nil {
		_ = os.Remove(path)
	}
}
