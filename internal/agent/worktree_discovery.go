package agent

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// WorktreeDiscovery is an inspection locator, never a lifecycle capability.
// Incompatible entries remain visible without decoding or rewriting their data.
type WorktreeDiscovery struct {
	Key     string `json:"key"`
	AppRoot string `json:"app_root,omitempty"`
	Status  string `json:"status"`
}

func DiscoverWorktrees(home string) ([]WorktreeDiscovery, error) {
	root := filepath.Join(home, "worktrees")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []WorktreeDiscovery{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]WorktreeDiscovery, 0, len(entries))
	for _, entry := range entries {
		if decoded, err := hex.DecodeString(entry.Name()); err != nil || len(decoded) != 32 {
			continue
		}
		item := WorktreeDiscovery{Key: entry.Name(), Status: "incompatible-or-invalid"}
		result = append(result, item)
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name(), "worktree.json")
		if err := checkPrivateWorktreeFile(path); err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var locator struct {
			AppRoot string `json:"app_root"`
		}
		if json.Unmarshal(data, &locator) != nil || locator.AppRoot == "" {
			continue
		}
		paths, err := PathsForWorktree(home, locator.AppRoot)
		if err != nil || paths.Key != entry.Name() || paths.AppRoot != locator.AppRoot {
			continue
		}
		item.AppRoot = paths.AppRoot
		if _, err := paths.LoadRecord(""); err == nil {
			item.Status = "retained"
		}
		result[len(result)-1] = item
	}
	return result, nil
}
