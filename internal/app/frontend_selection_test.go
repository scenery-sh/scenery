package app

import (
	"strings"
	"testing"
)

func TestEnvironmentDisablesOnlySelectedOptionalFrontends(t *testing.T) {
	root := t.TempDir()
	writeAppTestFile(t, root, ".scenery.json", `{"name":"demo","root":"app","frontends":{"app":{"root":"app"},"admin":{"root":"admin"}},"envs":{"local":{"default":true,"frontends":{"admin":{"serve":"disabled"}}},"all":{}}}`)
	_, cfg, err := DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	local, err := cfg.ResolveEnv("")
	if err != nil || len(local.Frontends) != 1 || local.Frontends["app"].Serve != "development" {
		t.Fatalf("local = %+v, err = %v", local, err)
	}
	all, err := cfg.ResolveEnv("all")
	if err != nil || len(all.Frontends) != 2 || len(cfg.Frontends) != 2 {
		t.Fatalf("all = %+v, config = %+v, err = %v", all, cfg.Frontends, err)
	}
	writeAppTestFile(t, root, ".scenery.json", `{"name":"demo","frontends":{"app":{"root":"app"}},"envs":{"local":{"default":true,"frontends":{"app":{"serve":"disabled"}}}}}`)
	if _, _, err := DiscoverRoot(root); err == nil || !strings.Contains(err.Error(), "cannot disable the root frontend") {
		t.Fatalf("disabled root accepted: %v", err)
	}
}
