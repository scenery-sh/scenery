package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/compiler"
)

func writeCheckCacheFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"app.scn": `application "cachecheck" {}
module "alpha" {
  source = "./alpha"
}
`,
		"alpha/package.scn": `package "alpha" {}
record "ping_out" {
  field "ok" {
    type = bool
  }
}
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckCacheHitInvalidationAndConcurrency(t *testing.T) {
	root := t.TempDir()
	writeCheckCacheFixture(t, root)

	var compiles atomic.Int64
	orig := compileForContractCheck
	compileForContractCheck = func(ctx context.Context, r string) (*compiler.Result, error) {
		compiles.Add(1)
		return orig(ctx, r)
	}
	t.Cleanup(func() { compileForContractCheck = orig })

	// 1. First run compiles and populates cache.
	var out1 bytes.Buffer
	if err := runContractCheck(&out1, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("first check failed: %v\n%s", err, out1.String())
	}
	if compiles.Load() != 1 {
		t.Fatalf("compiles after run 1 = %d, want 1", compiles.Load())
	}
	if _, err := os.Stat(checkCachePath(root)); err != nil {
		t.Fatalf("expected cache file: %v", err)
	}

	// 2. Second run hits cache without recompiling and emits identical JSON.
	var out2 bytes.Buffer
	if err := runContractCheck(&out2, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("second check failed: %v\n%s", err, out2.String())
	}
	if compiles.Load() != 1 {
		t.Fatalf("compiles after run 2 = %d, want 1 (cache hit)", compiles.Load())
	}
	if out1.String() != out2.String() {
		t.Fatalf("cached JSON mismatch:\nrun1=%s\nrun2=%s", out1.String(), out2.String())
	}

	// 3. Concurrent invocations hit cache safely under -race.
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			var buf bytes.Buffer
			if err := runContractCheck(&buf, []string{"--app-root", root, "-o", "json"}); err != nil {
				t.Errorf("concurrent check failed: %v", err)
				return
			}
			if buf.String() != out1.String() {
				t.Errorf("concurrent check output mismatch")
			}
		})
	}
	wg.Wait()
	if compiles.Load() != 1 {
		t.Fatalf("compiles after concurrent hits = %d, want 1", compiles.Load())
	}

	// 4. Editing package.scn invalidates cache.
	scnPath := filepath.Join(root, "alpha", "package.scn")
	if err := os.WriteFile(scnPath, []byte(`package "alpha" {}
record "ping_out" {
  field "ok" {
    type = bool
  }
  field "note" {
    type = string
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out3 bytes.Buffer
	if err := runContractCheck(&out3, []string{"--app-root", root, "-o", "json"}); err != nil {
		t.Fatalf("check after valid .scn edit failed: %v\n%s", err, out3.String())
	}
	if compiles.Load() != 2 {
		t.Fatalf("compiles after .scn edit = %d, want 2", compiles.Load())
	}

	// 5. Invalid .scn edit invalidates cache, fails with diagnostics, and never serves stale pass.
	if err := os.WriteFile(scnPath, []byte(`record "ping_out" { unknown_attr = 1 }`), 0o644); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		var failBuf bytes.Buffer
		err := runContractCheck(&failBuf, []string{"--app-root", root, "-o", "json"})
		if err == nil {
			t.Fatalf("attempt %d: expected failure on invalid .scn, got nil", attempt)
		}
		var env struct {
			OK          bool  `json:"ok"`
			Diagnostics []any `json:"diagnostics"`
		}
		if jsonErr := json.Unmarshal(failBuf.Bytes(), &env); jsonErr != nil {
			t.Fatalf("attempt %d: unmarshal error envelope: %v", attempt, jsonErr)
		}
		if env.OK || len(env.Diagnostics) == 0 {
			t.Fatalf("attempt %d: expected ok=false with diagnostics, got %+v", attempt, env)
		}
	}
}
