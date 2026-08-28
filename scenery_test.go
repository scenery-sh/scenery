package scenery

import (
	"context"
	"testing"

	"scenery.sh/runtime"
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

func TestStartSpanWithoutRequestIsNoop(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "render")
	if ctx == nil || span == nil {
		t.Fatal("StartSpan returned a nil context or span")
	}
	span.End(nil)
}
