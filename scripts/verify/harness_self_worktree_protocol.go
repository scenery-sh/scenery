package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

func (p *worktreeRuntimeProbe) mixedProtocol(rootA, rootB string, sentinel detachedDevResult) error {
	return p.scenario("A8", "real incompatible control-protocol binaries coexist on different roots", func(e map[string]any) error {
		stopSentinel := p.startSentinel(worktreeProbeAPI(sentinel) + "/books")
		defer func() {
			counts := stopSentinel()
			e["sentinel_requests"], e["sentinel_failures"] = counts[0], counts[1]
		}()
		beforeB, err := p.record(rootB)
		if err != nil {
			return err
		}
		root := filepath.Join(p.root, "protocol-b")
		p.roots = append(p.roots, root)
		if _, err := p.run(rootA, "git", "worktree", "add", "--quiet", "-b", "probe-protocol-b", root); err != nil {
			return err
		}
		variant, provenance, err := p.buildProtocolVariant(root)
		if err != nil {
			return err
		}
		e["binary_provenance"] = provenance
		p.binaries[root] = variant
		runtime, err := p.up(root)
		if err != nil {
			return err
		}
		if err := p.get(worktreeProbeAPI(runtime) + "/books"); err != nil {
			return err
		}
		pathsA, err := localagent.PathsForWorktree(p.home, rootA)
		if err != nil {
			return err
		}
		pathsVariant, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		clientA := localagent.NewClient(pathsA.Socket)
		defer clientA.CloseIdleConnections()
		clientVariant := localagent.NewClient(pathsVariant.Socket)
		defer clientVariant.CloseIdleConnections()
		healthA, err := clientA.Health(p.ctx)
		if err != nil {
			return err
		}
		healthVariant, err := clientVariant.Health(p.ctx)
		if err != nil {
			return err
		}
		// The variant renames an actual health/state wire field. Its own CLI
		// decodes that field; this candidate's reader sees no socket_path.
		if healthA.SchemaRevision == healthVariant.SchemaRevision || healthVariant.SocketPath != "" || healthVariant.PID != runtime.PID {
			return fmt.Errorf("the second binary did not expose a genuinely different control protocol")
		}
		e["schema_a"], e["schema_b"] = healthA.SchemaRevision, healthVariant.SchemaRevision
		e["spec_a"], e["spec_b"] = healthA.SpecRevision, healthVariant.SpecRevision
		beforeRecord, err := os.ReadFile(pathsA.Record)
		if err != nil {
			return err
		}
		output, rejected := p.run(rootA, variant, "up", "--app-root", rootA, "--detach", "-o", "json")
		if rejected == nil || !bytes.Contains(output, []byte("SCN8003")) {
			return fmt.Errorf("incompatible same-root access did not return its typed precondition: %s", output)
		}
		afterRecord, err := os.ReadFile(pathsA.Record)
		if err != nil {
			return err
		}
		afterHealth, err := clientA.Health(p.ctx)
		if err != nil {
			return err
		}
		if !bytes.Equal(beforeRecord, afterRecord) || afterHealth.PID != healthA.PID {
			return fmt.Errorf("incompatible CLI mutated or replaced the current owner")
		}
		afterB, err := p.record(rootB)
		if err != nil {
			return err
		}
		sessionB, err := p.liveSession(rootB)
		if err != nil {
			return err
		}
		if !sameWorktreeCluster(beforeB, afterB) || sessionB.OwnerPID != sentinel.PID {
			return fmt.Errorf("mixed-protocol startup changed the sibling runtime")
		}
		e["same_root_rejected_without_mutation"], e["sibling_ownership_unchanged"] = true, true
		counts := stopSentinel()
		if counts[0] < 2 || counts[1] != 0 {
			return fmt.Errorf("mixed-protocol sentinel had %d failures in %d requests", counts[1], counts[0])
		}
		return nil
	})
}

func (p *worktreeRuntimeProbe) buildProtocolVariant(appRoot string) (string, map[string]any, error) {
	dir := filepath.Join(p.root, "protocol-source")
	if err := copyHarnessFrameworkSource(p.repo, dir); err != nil {
		return "", nil, err
	}
	digests := map[string]any{}
	for _, name := range []string{"internal/agent/types.go", "internal/agent/artifact.go"} {
		original, err := os.ReadFile(filepath.Join(p.repo, name))
		if err != nil {
			return "", nil, err
		}
		var changed []byte
		if strings.HasSuffix(name, "types.go") {
			changed = bytes.ReplaceAll(original, []byte("`json:\"socket_path\"`"), []byte("`json:\"control_socket\"`"))
		} else {
			old := []byte(`{"identity":"artifact","state":"agent-process","worktree_edge_proxy":true}`)
			next := []byte(`{"identity":"artifact","state":"agent-process","worktree_edge_proxy":true,"socket_field":"control_socket"}`)
			changed = bytes.ReplaceAll(original, old, next)
		}
		if bytes.Equal(original, changed) {
			return "", nil, fmt.Errorf("protocol variant source anchor missing in %s", name)
		}
		target := filepath.Join(dir, name)
		if err := os.WriteFile(target, changed, 0o600); err != nil {
			return "", nil, err
		}
		before, after := sha256.Sum256(original), sha256.Sum256(changed)
		digests[name] = map[string]string{"base_sha256": hex.EncodeToString(before[:]), "variant_sha256": hex.EncodeToString(after[:])}
	}
	// The incompatible protocol is still one coherent producer: prepare the
	// mutated source through the same public selection used by application work.
	if _, err := p.run(appRoot, p.binary, "framework", "use", "--source", dir, "--app-root", appRoot, "-o", "json"); err != nil {
		return "", nil, err
	}
	selection, err := build.ReadFrameworkSelection(appRoot)
	if err != nil {
		return "", nil, err
	}
	binary := selection.Executable
	baseHash, err := worktreeProbeFileSHA(p.binary)
	if err != nil {
		return "", nil, err
	}
	variantHash, err := worktreeProbeFileSHA(binary)
	if err != nil {
		return "", nil, err
	}
	if baseHash == variantHash {
		return "", nil, fmt.Errorf("protocol binaries are identical")
	}
	return binary, map[string]any{"base_binary": p.binary, "base_sha256": baseHash, "variant_binary": binary, "variant_sha256": variantHash, "source_changes": digests, "wire_change": "health/state socket_path renamed to control_socket; matching exact schema descriptor changed"}, nil
}

func worktreeProbeFileSHA(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// The returned collector is idempotent so a scenario can assert counts and
// still record them from a deferred failure path.
func (p *worktreeRuntimeProbe) startSentinel(url string) func() [2]int {
	stop, done := make(chan struct{}), make(chan [2]int, 1)
	go func() {
		var counts [2]int
		for {
			counts[0]++
			if p.get(url) != nil {
				counts[1]++
			}
			select {
			case <-stop:
				done <- counts
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	var counts [2]int
	collected := false
	return func() [2]int {
		if !collected {
			close(stop)
			counts, collected = <-done, true
		}
		return counts
	}
}
