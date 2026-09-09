package storagefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/atomicfile"
)

func TestPurgeRetirementPublicationUncertaintyRetainsRecovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := testBinding(root)
	binding.Managed, binding.WorktreeKey = true, strings.Repeat("a", 64)
	n, err := allocate(ctx, filepath.Join(root, "storage"), binding, testDiskIO())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := n.PreviewPurge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	replace := n.io.replace
	cut := errors.New("retirement parent sync failed")
	n.io.replace = func(r *os.Root, name string, data []byte) error {
		if err := replace(r, name, data); err != nil {
			return err
		}
		if name == "owner.json" {
			return &atomicfile.PublicationError{Err: cut}
		}
		return nil
	}
	result, err := n.ApplyPurge(ctx, preview.SelectionRevision)
	var recovery *PurgeRecoveryError
	if !errors.As(err, &recovery) || !errors.Is(err, cut) || result.Retired != nil || recovery.Result.Retired != nil || result.Reclaimed {
		t.Fatalf("uncertain retirement: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(n.Path, "generations")); err != nil {
		t.Fatalf("reclaimed after uncertain retirement: %v", err)
	}
	n.io.replace = replace
	result, err = n.ApplyPurge(ctx, preview.SelectionRevision)
	if err != nil || result.Retired == nil || !*result.Retired || !result.Reclaimed {
		t.Fatalf("pinned retry: %+v, %v", result, err)
	}
}
