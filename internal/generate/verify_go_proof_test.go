package generate

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
)

func TestImplementationProofNamesProducerContractAndAnalysisInputs(t *testing.T) {
	previous := implementationProofProducer.Load()
	t.Cleanup(func() { implementationProofProducer.Store(previous) })
	root := t.TempDir()
	for name, contents := range map[string]string{"go.mod": "module example.test/app\n\ngo 1.27\n", "svc/service.go": "package svc\n"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := func() *compiler.Result {
		manifest := &compiler.Manifest{ContractRevision: "sha256:contract"}
		manifest.Application.Name = "app"
		return &compiler.Result{Root: root, WorkspaceRevision: "sha256:workspace", Manifest: manifest}
	}
	targets := []compiler.GoBuildTarget{{Address: "app/go_target/development", Role: "development", Context: gotarget.Context{ModuleRoot: root, Patterns: []string{"./..."}}}}
	overlay := map[string][]byte{filepath.Join(root, "svc", "contract.gen.go"): []byte("package svc\n")}

	// Without a producer identity nothing is proven or recorded.
	SetImplementationProofProducer("")
	if path := implementationProofPath(result(), overlay, targets); path != "" {
		t.Fatalf("an unbound producer has a proof path %q", path)
	}
	SetImplementationProofProducer("sha256:producer")
	path := implementationProofPath(result(), overlay, targets)
	if path == "" || filepath.Dir(path) != filepath.Join(root, ".scenery", "build", "implementation-check") || implementationProven(path) {
		t.Fatalf("proof path = %q", path)
	}
	recordImplementationProof(path)
	if !implementationProven(path) || implementationProofPath(result(), overlay, targets) != path {
		t.Fatal("a recorded proof of unchanged inputs was not found")
	}
	changed := map[string]func() string{
		"producer": func() string {
			SetImplementationProofProducer("sha256:other")
			defer SetImplementationProofProducer("sha256:producer")
			return implementationProofPath(result(), overlay, targets)
		},
		"workspace": func() string {
			other := result()
			other.WorkspaceRevision = "sha256:other"
			return implementationProofPath(other, overlay, targets)
		},
		"contract": func() string {
			other := result()
			other.Manifest.ContractRevision = "sha256:other"
			return implementationProofPath(other, overlay, targets)
		},
		"overlay": func() string {
			return implementationProofPath(result(), map[string][]byte{filepath.Join(root, "svc", "contract.gen.go"): []byte("package svc\n\nvar X int\n")}, targets)
		},
		"target role": func() string {
			other := []compiler.GoBuildTarget{targets[0]}
			other[0].Role = "contract"
			return implementationProofPath(result(), overlay, other)
		},
	}
	for name, proof := range changed {
		if other := proof(); other == "" || other == path || implementationProven(other) {
			t.Errorf("a changed %s is covered by the recorded proof (%q)", name, other)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "svc", "service.go"), []byte("package svc\n\nvar Changed int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if other := implementationProofPath(result(), overlay, targets); other == path || implementationProven(other) {
		t.Fatal("changed Go source is covered by the recorded proof")
	}
	// Unknown revisions, no targets and unnameable inputs have no proof.
	unknown := result()
	unknown.WorkspaceRevision = ""
	if implementationProofPath(unknown, overlay, targets) != "" || implementationProofPath(result(), overlay, nil) != "" {
		t.Fatal("unknown inputs have a proof path")
	}
	if err := os.Mkdir(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if implementationProofPath(result(), overlay, targets) != "" {
		t.Fatal("a vendored module has a proof path")
	}
	// Retention is bounded and keeps the newest proofs.
	dir := filepath.Dir(path)
	for index := range implementationProofLimit + 4 {
		name := filepath.Join(dir, "older-"+strconv.Itoa(index))
		if err := os.WriteFile(name, []byte("passed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-time.Duration(index+1) * time.Hour)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	latest := filepath.Join(dir, "latest")
	recordImplementationProof(latest)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != implementationProofLimit || !implementationProven(latest) || !implementationProven(filepath.Join(dir, "older-0")) || implementationProven(filepath.Join(dir, "older-"+strconv.Itoa(implementationProofLimit+3))) {
		t.Fatalf("retained %d proofs (%v), want the newest %d", len(entries), err, implementationProofLimit)
	}
	recordImplementationProof("")
}
