package feature

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Verify raw authored bytes against the commit, including paths Git's index
// might intentionally hide with assume-unchanged/skip-worktree flags.
func verifyCandidateTree(ctx context.Context, root string) error {
	entries, err := git(ctx, root, "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(entries, "\x00") {
		if entry == "" {
			continue
		}
		metadata, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			return fmt.Errorf("candidate has an unsupported tree entry %q", entry)
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		var bytes []byte
		if fields[0] == "120000" {
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("candidate mode differs from its commit: %s", name)
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			bytes = []byte(target)
		} else {
			if !info.Mode().IsRegular() || (info.Mode()&0o111 != 0) != (fields[0] == "100755") {
				return fmt.Errorf("candidate mode differs from its commit: %s", name)
			}
			bytes, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		if blobRevision(bytes, len(fields[2])) != fields[2] {
			return fmt.Errorf("candidate bytes differ from its committed checkpoint: %s", name)
		}
	}
	return nil
}

func blobRevision(bytes []byte, length int) string {
	digest := sha256.New()
	if length == 40 {
		digest = sha1.New()
	}
	_, _ = fmt.Fprintf(digest, "blob %d\x00", len(bytes))
	_, _ = digest.Write(bytes)
	return hex.EncodeToString(digest.Sum(nil))
}
