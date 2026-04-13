package pulse

import (
	"testing"

	"pulse.dev/runtime"
)

func TestMetaIncludesLocalEnvironmentDefaults(t *testing.T) {
	runtime.SetAppConfig(runtime.AppConfig{
		Name:       "test-app",
		ListenAddr: "127.0.0.1:4000",
	})

	meta := Meta()
	if meta.AppID != "test-app" {
		t.Fatalf("Meta().AppID = %q, want %q", meta.AppID, "test-app")
	}
	if meta.Environment.Name != "local" {
		t.Fatalf("Meta().Environment.Name = %q, want %q", meta.Environment.Name, "local")
	}
	if meta.Environment.Type != EnvDevelopment {
		t.Fatalf("Meta().Environment.Type = %q, want %q", meta.Environment.Type, EnvDevelopment)
	}
	if meta.Environment.Cloud != CloudLocal {
		t.Fatalf("Meta().Environment.Cloud = %q, want %q", meta.Environment.Cloud, CloudLocal)
	}
}
