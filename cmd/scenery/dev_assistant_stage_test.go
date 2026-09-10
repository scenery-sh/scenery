package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func assistantStageFixture(t *testing.T) (*assistantSupervisor, *compiler.Result, string) {
	t.Helper()
	control, mcp := assistantControlURLAllocator, assistantMCPListenAddressAllocator
	assistantControlURLAllocator = func() (string, error) { return "http://127.0.0.1:4101", nil }
	assistantMCPListenAddressAllocator = func() (string, error) { return "127.0.0.1:4102", nil }
	t.Cleanup(func() { assistantControlURLAllocator, assistantMCPListenAddressAllocator = control, mcp })
	root := t.TempDir()
	source := filepath.Join(root, "assistants", "support")
	if err := os.MkdirAll(filepath.Join(source, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"package.json": "{}\n", "package-lock.json": "{}\n", "index.ts": "export const value = 'original';\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "original", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	s := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{
		Root: root, StateRoot: filepath.Join(root, "state"), UseAppGateway: true,
		NodeResolver: func(context.Context) (string, string, string, error) { return "/node", "/npm", "/home", nil },
		InstallDeps:  func(context.Context, string, string, string) error { return nil },
		BuildOverlay: func(context.Context, string, string, string, string) error { return nil },
	})
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	return s, result, source
}

func nextAssistantStageResult(original *compiler.Result) *compiler.Result {
	result := *original
	manifest := *original.Manifest
	manifest.ContractRevision = "candidate"
	result.Manifest = &manifest
	return &result
}

func TestAssistantStageFailureLeavesActiveStateUntouched(t *testing.T) {
	s, result, _ := assistantStageFixture(t)
	beforeConfig, beforeStatus := s.RuntimeConfig(), s.Status()
	before := s.captureStage()
	publications := 0
	s.config.OnStatus = func([]AssistantStatusRecord) { publications++ }
	s.config.BuildOverlay = func(context.Context, string, string, string, string) error {
		return errors.New("candidate build failed")
	}
	stage, err := s.stage(context.Background(), nextAssistantStageResult(result))
	if err == nil {
		t.Fatal("failed preparation was accepted")
	}
	s.releaseStage(stage)
	if publications != 0 || !reflect.DeepEqual(beforeConfig, s.RuntimeConfig()) || !reflect.DeepEqual(beforeStatus, s.Status()) || s.contract != result {
		t.Fatal("candidate preparation mutated active descriptors, status or contract")
	}
	for _, prepared := range before.prepared {
		if _, err := os.Stat(prepared.overlay.Root); err != nil {
			t.Fatalf("active overlay was retired: %v", err)
		}
	}
	entries, err := os.ReadDir(s.config.StateRoot)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed candidate leaked its tree: %v, %v", entries, err)
	}
}

func TestAssistantStageRollbackRetainsExactPrivateBytes(t *testing.T) {
	s, result, source := assistantStageFixture(t)
	beforeConfig := s.RuntimeConfig()
	previous := s.captureStage()
	old := previous.prepared["app/assistant/support"]
	if err := os.WriteFile(filepath.Join(source, "index.ts"), []byte("export const value = 'candidate';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.stage(context.Background(), nextAssistantStageResult(result))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeConfig, s.RuntimeConfig()) {
		t.Fatal("staging published a new descriptor")
	}
	previousApp := &runningApp{launch: &appStartPlan{assistants: previous}}
	candidatePlan := &appStartPlan{assistants: candidate}
	current, recovered, err := replaceAppGeneration(context.Background(), previousApp, candidatePlan,
		func(*runningApp) error { return nil },
		func(ctx context.Context, plan *appStartPlan) (*runningApp, error) {
			if err := s.activateStage(ctx, plan.assistants); err != nil {
				return nil, err
			}
			if plan == candidatePlan {
				if err := os.Remove(filepath.Join(source, "index.ts")); err != nil {
					t.Fatal(err)
				}
				return nil, errors.New("candidate listener failed")
			}
			return &runningApp{launch: plan}, nil
		})
	if err == nil || !recovered || current.launch != previousApp.launch {
		t.Fatalf("handoff did not restore previous generation: recovered=%v err=%v", recovered, err)
	}
	s.releaseStage(candidate)
	s.releaseStage(previous)
	if !reflect.DeepEqual(beforeConfig, s.RuntimeConfig()) {
		t.Fatal("rollback rotated retained descriptor or secrets")
	}
	data, err := os.ReadFile(filepath.Join(old.overlay.Root, "index.ts"))
	if err != nil || string(data) != "export const value = 'original';\n" {
		t.Fatalf("rollback did not retain original bytes: %q, %v", data, err)
	}
	for _, prepared := range candidate.prepared {
		if _, err := os.Stat(prepared.ownedRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired candidate root remains: %v", err)
		}
	}
}

func TestAssistantOldRestartCannotResurrectRetiredDefinition(t *testing.T) {
	s, result, _ := assistantStageFixture(t)
	old := s.captureStage()
	candidate, err := s.stage(context.Background(), nextAssistantStageResult(result))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.activateStage(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	s.releaseStage(old)
	before := s.RuntimeConfig()
	for _, prepared := range old.prepared {
		if err := s.startDefinition(context.Background(), prepared.definition); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, s.RuntimeConfig()) || s.contract == result {
		t.Fatal("delayed restart restored the old definition")
	}
}
