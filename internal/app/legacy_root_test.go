package app

import (
	"errors"
	"testing"
)

func TestRetiredConfigMarkerNeverDiscoversOrOverridesAnApp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeAppTestFile(t, root, ".config.json", `{"name":"legacy"}`)
	if _, _, err := DiscoverRoot(root); !errors.Is(err, ErrRootNotFound) {
		t.Fatalf("retired config discovered an app: %v", err)
	}
	writeAppTestFile(t, root, ".scenery.json", `{"name":"current"}`)
	if _, cfg, err := DiscoverRoot(root); err != nil || cfg.Name != "current" {
		t.Fatalf("retired config overrode current marker: %v %v", cfg.Name, err)
	}
}
