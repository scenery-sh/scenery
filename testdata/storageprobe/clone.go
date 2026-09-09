package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/storagefs"
)

func verifyCloneAndCopy(ctx context.Context, root string) (map[string]any, error) {
	value := strings.Repeat("native clone and forced copy\n", 4096)
	name := filepath.Join(root, "source")
	if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
		return nil, err
	}
	result := map[string]any{}
	var stores []*storagefs.Store
	for _, forceCopy := range []bool{false, true} {
		mode := "clone"
		if forceCopy {
			mode = "copy"
		}
		n, err := storagefs.Allocate(ctx, filepath.Join(root, mode), binding(root))
		if err != nil {
			return nil, err
		}
		source, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		r, err := n.BeginRestore(ctx, restoreDigest, false, "overwrite", "fail")
		if err != nil {
			_ = source.Close()
			return nil, err
		}
		cloned, copied := int64(0), int64(0)
		err = r.Stage(ctx, func(g *storagefs.Generation) error {
			var reader io.Reader = source
			if forceCopy {
				// A normal streaming reader selects the existing copy path;
				// no product environment switch or test hook is introduced.
				reader = struct{ io.Reader }{source}
			}
			err := g.Put(ctx, logicalObject(value), reader, "fail")
			cloned, copied = g.Cloned, g.Copied
			return err
		})
		err = errors.Join(err, source.Close())
		if err == nil {
			err = r.Complete(ctx, nil)
		}
		if err := errors.Join(err, r.Close()); err != nil {
			return nil, err
		}
		if cloned+copied != 1 || (forceCopy && (cloned != 0 || copied != 1)) {
			return nil, fmt.Errorf("wrong native materialization counters: %s clone=%d copy=%d", mode, cloned, copied)
		}
		result[mode+"_cloned"], result[mode+"_copied"] = cloned, copied
		s, err := n.Store(storagefs.Scope{Store: "files", Tenant: "tenant"}, 0)
		if err != nil {
			return nil, err
		}
		stores = append(stores, s)
	}
	if err := os.Remove(name); err != nil {
		return nil, err
	}
	for _, s := range stores {
		if err := checkBody(ctx, s, value); err != nil {
			return nil, err
		}
		o, err := s.Head(ctx, "a")
		if err != nil || o.Metadata["source"] != "native fixture" || !o.ModifiedAt.Equal(logicalObject(value).ModifiedAt) {
			return nil, fmt.Errorf("materialization changed logical metadata: %v", err)
		}
	}
	result["clone_copy_source_eviction"] = "passed"
	return result, nil
}
