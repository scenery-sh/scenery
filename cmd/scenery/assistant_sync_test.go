package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

func TestAssistantDependencyCacheSchemaRevisionMatchesDescriptor(t *testing.T) {
	if got := string(spec.SchemaRevision(assistantDependencyCacheSchemaDescriptor)); got != assistantDependencyCacheRevision {
		t.Fatalf("dependency cache schema revision = %q, want %q", got, assistantDependencyCacheRevision)
	}
}

func TestAssistantSyncUsesManagedNodeCacheAndRejectsLockDrift(t *testing.T) {
	root := copyAssistantFixture(t)
	_, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = initializeAssistant(context.Background(), root, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api"})
	if err != nil {
		t.Fatal(err)
	}
	compiled = mustCompileAssistant(t, root)
	oldResolver, oldInstall := assistantManagedNodeResolver, assistantSyncInstall
	t.Cleanup(func() {
		assistantManagedNodeResolver = oldResolver
		assistantSyncInstall = oldInstall
	})
	installCount := 0
	assistantManagedNodeResolver = func(context.Context, string) (string, string, string, error) {
		return "/managed/node", "/managed/npm", "/managed/home", nil
	}
	assistantSyncInstall = func(_ context.Context, stage, _, _ string) error {
		installCount++
		return os.MkdirAll(filepath.Join(stage, "node_modules", "eve"), 0o755)
	}
	packageBefore := mustReadAssistant(t, filepath.Join(root, "assistants", "extra", "package.json"))
	lockBefore := mustReadAssistant(t, filepath.Join(root, "assistants", "extra", "package-lock.json"))
	first, err := syncAssistant(context.Background(), root, compiled, "extra")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "synced" || first.Reused || installCount != 1 {
		t.Fatalf("first sync=%#v installs=%d", first, installCount)
	}
	second, err := syncAssistant(context.Background(), root, compiled, "extra")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "reused" || !second.Reused || installCount != 1 || second.CachePath != first.CachePath {
		t.Fatalf("second sync=%#v installs=%d", second, installCount)
	}
	if got := mustReadAssistant(t, filepath.Join(root, "assistants", "extra", "package.json")); string(got) != string(packageBefore) {
		t.Fatal("sync modified authored package.json")
	}
	if got := mustReadAssistant(t, filepath.Join(root, "assistants", "extra", "package-lock.json")); string(got) != string(lockBefore) {
		t.Fatal("sync modified authored package-lock.json")
	}
	lockPath := filepath.Join(root, "assistants", "extra", "package-lock.json")
	drifted := strings.Replace(string(lockBefore), `"eve":"0.29.5"`, `"eve":"0.29.4"`, 1)
	if drifted == string(lockBefore) {
		drifted = strings.Replace(string(lockBefore), `"eve": "0.29.5"`, `"eve": "0.29.4"`, 1)
	}
	if err := os.WriteFile(lockPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncAssistant(context.Background(), root, compiled, "extra"); err == nil || !strings.Contains(err.Error(), "lock drift") {
		t.Fatalf("lock drift error=%v", err)
	}
}
