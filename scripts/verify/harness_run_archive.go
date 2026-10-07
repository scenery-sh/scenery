package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"scenery.sh/internal/repoinfo"
	"strings"
)

// Inventory includes tracked deletions and non-ignored untracked files. Hash
// link targets rather than following them outside the repository.
func harnessInputRevision(ctx context.Context, root string) (string, error) {
	output, err := runHarnessGit(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", fmt.Errorf("inventory validation inputs: %w", err)
	}
	return hashHarnessInputs(root, strings.Split(output, "\x00"))
}

func hashHarnessInputs(root string, paths []string) (string, error) {
	return repoinfo.HashInputs(root, paths)
}

// Publish all run observations together. Later runs only replace navigation
// copies; acceptance links always identify the immutable archive directory.
func publishHarnessRun(root string, resp harnessSelfResponse, contextPack harnessAgentContext) error {
	if resp.Run == nil || resp.Run.ID == "" || filepath.Base(resp.Run.ID) != resp.Run.ID || resp.Run.ID == "." || resp.Run.ID == ".." {
		return fmt.Errorf("invalid validation run identity")
	}
	if contextPack.Run == nil || *contextPack.Run != *resp.Run || resp.Run.ArchivePath != filepath.ToSlash(filepath.Join(".scenery", "harness", "runs", resp.Run.ID)) {
		return fmt.Errorf("report and context must share the same run archive and input identity")
	}
	runs := filepath.Join(root, ".scenery", "harness", "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return err
	}
	destination := filepath.Join(runs, resp.Run.ID)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("validation run already exists or cannot be inspected: %s", destination)
	}
	staging, err := os.MkdirTemp(runs, ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, item := range []struct {
		name  string
		value any
	}{
		{"self.json", resp},
		{"verification.json", resp.Verification},
		{"summary.json", buildHarnessSelfSummary(resp)},
		{"agent-context.json", contextPack},
		{"agent-context-summary.json", buildHarnessAgentContextSummary(resp, contextPack)},
	} {
		if err := writeHarnessJSONFile(filepath.Join(staging, item.name), item.value); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, destination); err != nil {
		return err
	}
	if err := writeHarnessJSONFile(filepath.Join(root, ".scenery", "harness", "self-latest.json"), resp); err != nil {
		return err
	}
	return writeHarnessSelfOracleArtifacts(root, resp, contextPack)
}
