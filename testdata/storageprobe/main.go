// Storage's native process/filesystem boundary probe. The repository verifier
// builds this fixture into an owned temporary directory; it is not a product CLI.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/storagefs"
)

const restoreDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func main() {
	root := flag.String("root", "", "probe-owned root")
	child := flag.String("child", "", "process boundary journey")
	flag.Parse()
	if !filepath.IsAbs(*root) {
		fmt.Fprintln(os.Stderr, "an absolute probe root is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var err error
	if *child != "" {
		err = runChild(ctx, *root, *child)
	} else {
		var result map[string]any
		result, err = run(ctx, *root)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(result)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func binding(root string) storagefs.Binding {
	return storagefs.Binding{AppID: "storage-native-probe", AppRoot: root, UserID: os.Getuid()}
}

func run(ctx context.Context, root string) (map[string]any, error) {
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	result := map[string]any{"platform": runtime.GOOS, "power_loss": "not_tested"}
	n, err := storagefs.Allocate(ctx, filepath.Join(root, "namespace"), binding(root))
	if err != nil {
		return nil, err
	}
	s, err := n.Store(storagefs.Scope{Store: "files", Tenant: "tenant"}, 0)
	if err != nil {
		return nil, err
	}
	old, err := s.Put(ctx, "a", strings.NewReader("old"), storagefs.PutOptions{Metadata: map[string]string{"version": "old"}})
	if err != nil {
		return nil, err
	}
	if err := killAtPhase(ctx, root, "upload", nil); err != nil {
		return nil, err
	}
	if err := checkBody(ctx, s, "old"); err != nil {
		return nil, err
	}
	after, err := s.Head(ctx, "a")
	if err != nil || after.ETag != old.ETag || after.Metadata["version"] != "old" {
		return nil, fmt.Errorf("crashed upload replaced committed metadata: %v", err)
	}
	result["upload_process_crash"] = "passed"
	for _, mode := range []string{"stream", "task"} {
		if err := killAtPhase(ctx, root, mode, func() error {
			blocked, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
			defer cancel()
			capture, err := n.Capture(blocked)
			if capture != nil {
				_ = capture.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("capture bypassed active %s: %v", mode, err)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		capture, err := n.Capture(ctx)
		if err != nil {
			return nil, err
		}
		if err := capture.Close(); err != nil {
			return nil, err
		}
		result[mode+"_cross_process_lease"] = "passed"
	}
	if err := killAtPhase(ctx, root, "restore", nil); err != nil {
		return nil, err
	}
	if err := n.CheckReady(ctx); !errors.Is(err, storagefs.ErrRecovery) {
		return nil, fmt.Errorf("crashed staged restore did not block ordinary access: %v", err)
	}
	wrong, err := n.BeginRestore(ctx, strings.Repeat("b", 64), false, "overwrite", "fail")
	if wrong != nil {
		_ = wrong.Close()
	}
	if !errors.Is(err, storagefs.ErrRecovery) {
		return nil, fmt.Errorf("wrong restore resume accepted: %v", err)
	}
	r, err := n.BeginRestore(ctx, restoreDigest, false, "overwrite", "fail")
	if err != nil {
		return nil, err
	}
	if err := r.Stage(ctx, func(*storagefs.Generation) error { return errors.New("repopulated a completed stage") }); err != nil {
		_ = r.Close()
		return nil, err
	}
	if err := errors.Join(r.Complete(ctx, nil), r.Close()); err != nil {
		return nil, err
	}
	if err := checkBody(ctx, s, "restored"); err != nil {
		return nil, err
	}
	result["restore_process_crash_pinned_resume"] = "passed"
	clone, err := verifyCloneAndCopy(ctx, root)
	if err != nil {
		return nil, err
	}
	for name, value := range clone {
		result[name] = value
	}
	if err := verifyHTTP(ctx, s); err != nil {
		return nil, err
	}
	result["native_http_head_range"] = "passed"
	return result, nil
}

func logicalObject(value string) storagefs.Object {
	digest := sha256.Sum256([]byte(value))
	return storagefs.Object{Store: "files", Tenant: "tenant", Key: "a", SizeBytes: int64(len(value)), SHA256: hex.EncodeToString(digest[:]), ModifiedAt: time.Unix(1, 0).UTC(), Metadata: map[string]string{"source": "native fixture"}}
}

func checkBody(ctx context.Context, s *storagefs.Store, want string) error {
	body, object, err := s.Get(ctx, "a", storagefs.GetOptions{})
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(body)
	if err := errors.Join(readErr, body.Close()); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if string(data) != want || object.SizeBytes != int64(len(data)) || object.SHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("native object bytes/metadata do not describe one version")
	}
	return nil
}

func runChild(ctx context.Context, root, mode string) error {
	n, err := storagefs.Open(ctx, filepath.Join(root, "namespace"), binding(root))
	if err != nil {
		return err
	}
	s, err := n.Store(storagefs.Scope{Store: "files", Tenant: "tenant"}, 0)
	if err != nil {
		return err
	}
	switch mode {
	case "upload":
		_, err := s.Put(ctx, "a", &interruptedUpload{}, storagefs.PutOptions{})
		return err
	case "stream":
		r, _, err := s.Get(ctx, "a", storagefs.GetOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return announceAndBlock(mode)
	case "task":
		lease, err := n.HoldTask(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = lease.Close() }()
		return announceAndBlock(mode)
	case "restore":
		r, err := n.BeginRestore(ctx, restoreDigest, false, "overwrite", "fail")
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		if err := r.Stage(ctx, func(g *storagefs.Generation) error {
			return g.Put(ctx, logicalObject("restored"), strings.NewReader("restored"), "fail")
		}); err != nil {
			return err
		}
		return announceAndBlock(mode)
	default:
		return fmt.Errorf("unknown native probe mode %q", mode)
	}
}

func announceAndBlock(phase string) error {
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"phase": phase}); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	return errors.Join(errors.New("parent released the process before the expected kill"), err)
}

type interruptedUpload struct{ started bool }

func (r *interruptedUpload) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		return copy(p, "partial upload"), nil
	}
	return 0, announceAndBlock("upload")
}
