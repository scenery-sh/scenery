package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A changed public validation contract leaves checked clients intact but stale.
// Unrelated authored edits must keep its last good runtime without rebuilding;
// the public generator then repairs the relevant input and releases the block.
func (p *worktreeRuntimeProbe) staleClientRecovery(root string, runtime detachedDevResult) (map[string]any, error) {
	offset := func() (int64, error) {
		info, err := os.Stat(runtime.LogPath)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	start, err := offset()
	if err != nil {
		return nil, err
	}
	spec := filepath.Join(root, "library/package.scn")
	bytes, err := os.ReadFile(spec)
	if err != nil {
		return nil, err
	}
	changed := strings.Replace(string(bytes), "max_length = 200", "max_length = 199", 1)
	if changed == string(bytes) {
		return nil, fmt.Errorf("stale-client fixture has no expected public validation input")
	}
	if err := os.WriteFile(spec, []byte(changed), 0o644); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitLog(p.ctx, runtime.LogPath, start, "build.blocked"); err != nil {
		return nil, err
	}
	if _, err := p.verify(root, runtime, "persisted"); err != nil {
		return nil, err
	}
	unrelated, err := offset()
	if err != nil {
		return nil, err
	}
	source := filepath.Join(root, "library/service.go")
	bytes, err = os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(source, append(bytes, []byte("\n// Unrelated owned source edit while clients are stale.\n")...), 0o644); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitLog(p.ctx, runtime.LogPath, unrelated, "build.blocked"); err != nil {
		return nil, err
	}
	if err := harnessAssertWatchBuildCount(p.ctx, runtime.LogPath, unrelated, 0); err != nil {
		return nil, err
	}
	events, err := harnessWatchEvents(runtime.LogPath, unrelated)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.Type == "build.error" || (event.Type == "build.step" && event.Data.Name == "build.request") {
			return nil, fmt.Errorf("unrelated source repeated the stale-client build: %s", event.Type)
		}
	}
	repair, err := offset()
	if err != nil {
		return nil, err
	}
	if _, err := p.run(root, p.binary, "generate", "--app-root", root, "--target", "typescript_client.public_api", "-o", "json"); err != nil {
		return nil, err
	}
	if err := harnessWaitBuildRequest(p.ctx, runtime.LogPath, repair, true); err != nil {
		return nil, err
	}
	if err := harnessProcessModelWaitLog(p.ctx, runtime.LogPath, repair, "build.unblocked"); err != nil {
		return nil, err
	}
	if _, err := p.verify(root, runtime, "persisted"); err != nil {
		return nil, err
	}
	return map[string]any{"last_good_served": true, "unrelated_edit_builds": 0, "public_regeneration_recovered": true, "retained_sql_verified": true}, nil
}
