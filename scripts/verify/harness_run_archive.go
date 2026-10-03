package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	paths = slices.Clone(paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	hash := sha256.New()
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("validation input is outside repository: %q", path)
		}
		abs := filepath.Join(root, path)
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00missing\x00", path)
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", path, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return "", err
			}
			_, _ = fmt.Fprintf(hash, "%s\x00", target)
		case info.Mode().IsRegular():
			file, err := os.Open(abs)
			if err != nil {
				return "", err
			}
			content := sha256.New()
			_, copyErr := io.Copy(content, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			_, _ = fmt.Fprintf(hash, "%x\x00", content.Sum(nil))
		default:
			return "", fmt.Errorf("unsupported validation input: %s", path)
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// Publish all three observations together. Later runs only replace navigation
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
		{"summary.json", buildHarnessSelfSummary(resp)},
		{"agent-context.json", contextPack},
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
