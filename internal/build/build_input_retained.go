package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/gotarget"
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

// goToolIdentityKey names a Go listing by its arguments and tool identity. It
// is what one process may trust of a listing another process retained; the
// retained graph then proves its directories, selections and module files
// current exactly as a graph of this process does.
func goToolIdentityKey(args, env []string) string {
	identity, resolved := gotarget.ToolIdentity(env)
	if !resolved {
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
