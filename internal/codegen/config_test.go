package codegen

import (
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestGeneratedPreflightPrecedesApplicationInitialization(t *testing.T) {
	generated, err := generateMain("fixture", app.Config{Auth: app.AuthConfig{Enabled: true}}, "example.test/fixture/internal/composition", nil)
	if err != nil {
		t.Fatal(err)
	}
	source := string(generated)
	if !strings.Contains(source, `Name: "fixture"`) {
		t.Fatal("entrypoint lost the explicitly supplied application name")
	}
	preflight := strings.Index(source, "sceneryruntime.WriteRuntimePreflight(")
	if preflight < 0 {
		t.Fatal("missing read-only runtime handshake")
	}
	for _, call := range []string{"sceneryauth.RegisterStandard(", "scenerycomposition.Register(", "sceneryruntime.Main("} {
		if position := strings.Index(source, call); position < preflight {
			t.Fatalf("%s runs before preflight or is missing", call)
		}
	}
	if !strings.Contains(source[:strings.Index(source, "sceneryauth.RegisterStandard(")], "return\n") {
		t.Fatal("preflight falls through to application initialization")
	}
}
