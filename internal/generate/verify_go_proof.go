package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/parse"
)

// implementationProofLimit bounds the proofs one application root retains.
const implementationProofLimit = 16

// implementationProofProducer identifies the producer whose analysis a proof
// records; empty disables proofs.
var implementationProofProducer atomic.Pointer[string]

// SetImplementationProofProducer lets implementation checks of this process
// leave and trust proofs. identity must name the producer's source content, so
// that a producer with another analysis never trusts this one's proof; an
// empty identity, the default, makes every check analyze.
func SetImplementationProofProducer(identity string) {
	implementationProofProducer.Store(&identity)
}

// implementationProofPath names the proof that the Go analysis of result's
// targets with overlay reported nothing. Its name digests the producer, the
// compiled workspace and contract, and, for every target, everything its
// analysis consumes (parse.AnalysisInputDigest). It is empty when any of those
// cannot be named, which makes the check analyze.
func implementationProofPath(result *compiler.Result, overlay map[string][]byte, targets []compiler.GoBuildTarget) string {
	producer := implementationProofProducer.Load()
	if producer == nil || *producer == "" || result == nil || result.Manifest == nil ||
		result.WorkspaceRevision == "" || result.Manifest.ContractRevision == "" || len(targets) == 0 {
		return ""
	}
	hash := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = hash.Write([]byte(part + "\x00"))
		}
	}
	write("scenery.implementation-proof/1", *producer, result.WorkspaceRevision, result.Manifest.ContractRevision, result.Manifest.Application.Name)
	for _, target := range targets {
		digest, err := parse.AnalysisInputDigest(result.Root, overlay, target.Context)
		if err != nil {
			return ""
		}
		write(target.Address, target.Role, digest)
	}
	return filepath.Join(result.Root, ".scenery", "build", "implementation-check", hex.EncodeToString(hash.Sum(nil)))
}

func implementationProven(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// recordImplementationProof keeps the proof at path and the newest proofs
// beside it. A proof is a convenience: failing to keep one changes nothing.
func recordImplementationProof(path string) {
	if path == "" {
		return
	}
	if atomicfile.Write(path, []byte("passed\n"), 0o644, atomicfile.Options{}) != nil {
		return
	}
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= implementationProofLimit {
		return
	}
	type proof struct {
		name     string
		modified int64
	}
	var proofs []proof
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			proofs = append(proofs, proof{entry.Name(), info.ModTime().UnixNano()})
		}
	}
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].modified > proofs[j].modified })
	for _, stale := range proofs[min(len(proofs), implementationProofLimit):] {
		if stale.name != filepath.Base(path) {
			_ = os.Remove(filepath.Join(dir, stale.name))
		}
	}
}
