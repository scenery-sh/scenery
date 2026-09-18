package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assistantCacheTestWrite(t *testing.T, root, path, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assistantCacheTestInput(t *testing.T, root string) {
	t.Helper()
	assistantCacheTestWrite(t, root, "package.json", `{"dependencies":{"eve":"0.59.1"}}`)
	assistantCacheTestWrite(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	assistantCacheTestWrite(t, root, "agent/agent.ts", "authored")
	assistantCacheTestWrite(t, root, ".scenery/runtime-manifest.json", "runtime-capability-identity")
}

// A prepared build records the roots it was built in and its connections. The
// assistant's own connection is dynamic and carries no address, so relocation
// rewrites roots only.
func assistantCacheTestIndex(root string) string {
	return "const authored = 'do not replace " + root + "';\nconst manifest = {\n" +
		`"agentRoot":"` + filepath.ToSlash(filepath.Join(root, "agent")) + `",` + "\n" +
		`"appRoot":"` + filepath.ToSlash(root) + `",` + "\n" +
		`"connections":[{"connectionName":"authored","url":"http://127.0.0.1:9"}],` + "\n" +
		`"dynamicConnections":[{"slug":"scenery","logicalPath":"connections/scenery.ts"}]` + "\n};\nexport { manifest };"
}

func TestAssistantPreparedCacheRestoresPrivateVerifiedCopy(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	assistantCacheTestInput(t, first)
	assistantCacheTestInput(t, second)
	node := filepath.Join(root, "node")
	assistantCacheTestWrite(t, root, "node", "managed executable")
	cache, err := openAssistantOverlayCache(root, first, node)
	if err != nil {
		t.Fatal(err)
	}
	if hit, err := cache.restore(context.Background(), first); err != nil || hit {
		t.Fatalf("cold cache: %v %v", hit, err)
	}
	assistantCacheTestWrite(t, first, "node_modules/eve/index.js", "dependency")
	assistantCacheTestWrite(t, first, ".output/server/index.mjs", assistantCacheTestIndex(first))
	assistantCacheTestWrite(t, first, ".output/.eve/discovery/manifest.json", "ephemeral")
	assistantCacheTestWrite(t, first, ".home/private-token", "private")
	if err := cache.publish(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	next, err := openAssistantOverlayCache(root, second, node)
	if err != nil {
		t.Fatal(err)
	}
	if next.key != cache.key {
		t.Fatal("private paths/listeners changed immutable input key")
	}
	if hit, err := next.restore(context.Background(), second); err != nil || !hit {
		t.Fatalf("warm cache: %v %v", hit, err)
	}
	data, err := os.ReadFile(filepath.Join(second, ".output/server/index.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"appRoot": "`+second+`"`) {
		t.Fatalf("build manifest was not relocated: %s", data)
	}
	if !strings.Contains(string(data), "do not replace "+first) {
		t.Fatal("authored JavaScript was rewritten")
	}
	for _, path := range []string{".home", ".output/.eve"} {
		if _, err := os.Lstat(filepath.Join(second, path)); !os.IsNotExist(err) {
			t.Fatalf("private state copied: %s", path)
		}
	}
	assistantCacheTestWrite(t, second, "node_modules/eve/index.js", "runtime mutation")
	cached, err := os.ReadFile(filepath.Join(cache.path, "tree/node_modules/eve/index.js"))
	if err != nil || string(cached) != "dependency" {
		t.Fatal("runtime writes modified reusable cache")
	}
}

func TestAssistantPreparedCacheInvalidatesExactInputs(t *testing.T) {
	root := t.TempDir()
	overlay := filepath.Join(root, "overlay")
	assistantCacheTestInput(t, overlay)
	assistantCacheTestWrite(t, root, "node", "node1")
	node := filepath.Join(root, "node")
	previous := ""
	for _, change := range []struct{ path, value string }{
		{"agent/agent.ts", "original"}, {"agent/agent.ts", "source edit"},
		{"package.json", "package edit"}, {"package-lock.json", "lock edit"},
		{".scenery/runtime-manifest.json", "capability edit"}, {"agent/connections/scenery.ts", "adapter edit"},
	} {
		assistantCacheTestWrite(t, overlay, change.path, change.value)
		cache, err := openAssistantOverlayCache(root, overlay, node)
		if err != nil {
			t.Fatal(err)
		}
		if cache.key == previous {
			t.Fatalf("input did not invalidate: %s", change.path)
		}
		previous = cache.key
	}
	assistantCacheTestWrite(t, root, "node", "node2")
	cache, err := openAssistantOverlayCache(root, overlay, node)
	if err != nil || cache.key == previous {
		t.Fatalf("node change not invalidated: %v", err)
	}
}

func TestAssistantPreparedCacheRejectsTamperedOutput(t *testing.T) {
	for _, mutation := range []string{"bytes", "missing", "extra", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			source, dest := filepath.Join(root, "source"), filepath.Join(root, "dest")
			assistantCacheTestInput(t, source)
			assistantCacheTestInput(t, dest)
			assistantCacheTestWrite(t, root, "node", "node")
			cache, err := openAssistantOverlayCache(root, source, filepath.Join(root, "node"))
			if err != nil {
				t.Fatal(err)
			}
			assistantCacheTestWrite(t, source, "node_modules/eve/index.js", "dependency")
			assistantCacheTestWrite(t, source, ".output/server/index.mjs", assistantCacheTestIndex(source))
			if err := cache.publish(context.Background(), source); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cache.path, "tree/node_modules/eve/index.js")
			switch mutation {
			case "bytes":
				assistantCacheTestWrite(t, filepath.Join(cache.path, "tree"), "node_modules/eve/index.js", "changed")
			case "extra":
				assistantCacheTestWrite(t, filepath.Join(cache.path, "tree"), "node_modules/extra.js", "extra")
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "node"), path); err != nil {
					t.Fatal(err)
				}
			}
			if hit, err := cache.restore(context.Background(), dest); err == nil || hit {
				t.Fatalf("tampered cache reused: hit=%v err=%v", hit, err)
			}
		})
	}
}

func TestAssistantBuildManifestRelocationRejectsUnknownShape(t *testing.T) {
	// A build whose Scenery connection is static carries the address of its own
	// build; relocation must refuse it instead of serving an unreachable one.
	staticConnection := strings.Replace(assistantCacheTestIndex("/old"),
		`"dynamicConnections":[{"slug":"scenery","logicalPath":"connections/scenery.ts"}]`,
		`"dynamicConnections":[]`, 1)
	for _, data := range []string{"unknown output", assistantCacheTestIndex("/wrong"), staticConnection} {
		if _, err := relocateAssistantBuildManifest([]byte(data), "/old", "/new"); err == nil {
			t.Fatal("unsupported build manifest accepted")
		}
	}
}
