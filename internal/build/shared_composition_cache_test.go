package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestSharedCompositionReusesCrossWorktreeArtifactAndRepairsCorruption(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	cfg := appcfg.Config{Name: "cross-worktree-composition", ConfigPath: filepath.Join(t.TempDir(), ".scenery.json")}
	var steps []Step
	ctx := WithTrace(context.Background(), func(step Step) { steps = append(steps, step) })
	first, err := renderSharedCompositionContext(ctx, cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	// ConfigPath is machine-local discovery state and is intentionally omitted
	// from Config JSON. Equivalent authored worktrees therefore share bytes.
	cfg.ConfigPath = filepath.Join(t.TempDir(), ".scenery.json")
	second, err := renderSharedCompositionContext(ctx, cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Cache != "miss" || steps[1].Cache != "hit" || steps[1].Reason != "shared_content_artifact" {
		t.Fatalf("shared composition steps = %+v", steps)
	}
	if string(first.Generated["scenery_internal_main/main.go"]) != string(second.Generated["scenery_internal_main/main.go"]) {
		t.Fatal("cross-worktree composition bytes differ")
	}
	first.Generated["scenery_internal_main/main.go"][0] ^= 0xff
	if first.Generated["scenery_internal_main/main.go"][0] == second.Generated["scenery_internal_main/main.go"][0] {
		t.Fatal("cache result shares mutable returned bytes")
	}

	key, err := sharedCompositionKey(cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	root, err := sharedCompositionRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", key+".json"), []byte(`{"kind":"corrupt"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	steps = nil
	if _, err := renderSharedCompositionContext(ctx, cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one"); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Cache != "miss" || steps[0].Reason != "rendered_and_published" {
		t.Fatalf("corrupt composition cache steps = %+v", steps)
	}
}

func TestSharedCompositionKeyRejectsDifferentGenerator(t *testing.T) {
	cfg := appcfg.Config{Name: "generator-key"}
	first, err := sharedCompositionKey(cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sharedCompositionKey(cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-two")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("incompatible generator producers received one composition key")
	}
}

func TestSharedCompositionRejectsIncompatibleProducer(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	cfg := appcfg.Config{Name: "producer-rejection"}
	ctx := context.Background()
	if _, err := renderSharedCompositionContext(ctx, cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one"); err != nil {
		t.Fatal(err)
	}
	key, err := sharedCompositionKey(cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	root, err := sharedCompositionRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifacts", key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact sharedCompositionArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	artifact.Producer.Version += "-incompatible"
	data, err = json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit := loadSharedComposition(root, key, "generator-one"); hit {
		t.Fatal("shared composition accepted an incompatible producer")
	}
}

func TestSharedCompositionRejectsRehashedEscapingPayload(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	cfg := appcfg.Config{Name: "path-rejection"}
	if _, err := renderSharedCompositionContext(context.Background(), cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one"); err != nil {
		t.Fatal(err)
	}
	key, err := sharedCompositionKey(cfg.Name, cfg, "example.com/app/internal/scenerygen", nil, "generator-one")
	if err != nil {
		t.Fatal(err)
	}
	root, err := sharedCompositionRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifacts", key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact sharedCompositionArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	artifact.Files = map[string][]byte{"../../outside.go": []byte("package outside\n")}
	artifact.PayloadSHA256 = compositionPayloadDigest(artifact.Files)
	data, err = json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit := loadSharedComposition(root, key, "generator-one"); hit {
		t.Fatal("shared composition accepted an escaping generated path")
	}
}
