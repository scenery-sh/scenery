package vnext

import (
	"go/build"
	"runtime"
	"strings"
	"testing"

	"scenery.sh/internal/parse"
)

func TestResolvedGoTargetRecordsContentAddressedToolchainAndNativeTools(t *testing.T) {
	context := parse.GoTargetContext{
		ToolchainVersion: strings.TrimPrefix(runtime.Version(), "go"),
		GOOS:             runtime.GOOS,
		GOARCH:           runtime.GOARCH,
		CGOEnabled:       build.Default.CgoEnabled,
	}
	if err := resolveGoToolIdentities(&context); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"identity", "digest", "go_command_digest", "compiler_digest"} {
		if context.ToolchainIdentity[field] == "" {
			t.Fatalf("toolchain identity is missing %s: %#v", field, context.ToolchainIdentity)
		}
	}
	for _, field := range []string{"digest", "go_command_digest", "compiler_digest"} {
		if !isCanonicalSHA256Digest(context.ToolchainIdentity[field]) {
			t.Fatalf("toolchain %s = %q", field, context.ToolchainIdentity[field])
		}
	}
	if build.Default.CgoEnabled {
		if len(context.NativeToolIdentities) != 2 || context.NativeToolEnv["CC"] == "" || context.NativeToolEnv["CXX"] == "" || context.NativeToolEnv["PKG_CONFIG"] == "" {
			t.Fatalf("native tool context = %#v %#v", context.NativeToolIdentities, context.NativeToolEnv)
		}
		for _, identity := range context.NativeToolIdentities {
			if !isCanonicalSHA256Digest(identity["digest"]) {
				t.Fatalf("native tool identity = %#v", identity)
			}
		}
	}
}

func TestFixedCGOTargetFailsUntilNativeToolchainSchemaExists(t *testing.T) {
	target := Resource{Address: "app/go_target/production", Module: "app", Name: "production", Kind: "scenery.go-target/v1", Spec: map[string]any{
		"role": "artifact", "platform": "fixed", "toolchain": map[string]any{"$ref": "go_toolchain.application"}, "module": map[string]any{"$ref": "go_module.application"},
		"packages": []any{"./..."}, "goos": "linux", "goarch": "amd64", "cgo": "enabled",
	}}
	diagnostics := validateGoTargets(t.TempDir(), []Resource{target})
	if !diagnosticsContain(diagnostics, "SCN6141") {
		t.Fatalf("fixed CGO target diagnostics = %#v", diagnostics)
	}
}
