package generate

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/scn"
)

// MaterializeMissingCachedTypeScriptClients writes the cache-materialized
// clients of an app that declares one whose output was never written, and
// returns the names that were missing. A frontend dev server resolves the
// client when it starts and keeps a failed resolution, so the first start of
// a fresh checkout calls this before it starts one. An app whose clients all
// exist costs one parse of its root sources; a source that does not compile
// is left to the build, which reports it. The write is a workspace
// transaction, which a concurrent source read refuses: before runs once,
// only when a client is missing, so the caller can finish such a read first.
func MaterializeMissingCachedTypeScriptClients(ctx context.Context, root string, before func()) ([]string, error) {
	missing := missingCachedTypeScriptClients(root)
	if len(missing) == 0 {
		return nil, nil
	}
	if before != nil {
		before()
	}
	result, err := compiler.Check(root)
	if err != nil || result == nil || !result.Valid() {
		return missing, nil
	}
	_, err = SyncCachedTypeScriptClientsContext(ctx, result)
	return missing, err
}

// missingCachedTypeScriptClients reads the root sources the compiler merges.
func missingCachedTypeScriptClients(root string) []string {
	paths, err := scn.SourceFiles(root, true)
	if err != nil {
		return nil
	}
	var missing []string
	for _, path := range paths {
		source, _ := scn.Parse(root, path)
		if source == nil {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type != "typescript_client" || len(block.Labels) != 1 {
				continue
			}
			if materialization, _ := block.Attributes["materialization"].Value.(string); materialization != "cache" {
				continue
			}
			target := Resource{Name: block.Labels[0], Spec: map[string]any{"materialization": "cache"}}
			outputRoot, err := typeScriptOutputRoot(&compiler.Result{Root: root}, target)
			if err != nil {
				continue
			}
			if _, err := os.Lstat(filepath.Join(outputRoot, "scenery.typescript-client-generated.json")); errors.Is(err, os.ErrNotExist) {
				missing = append(missing, target.Name)
			}
		}
	}
	return missing
}
