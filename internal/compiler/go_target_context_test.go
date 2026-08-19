package compiler

import (
	"go/build"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"scenery.sh/internal/gotarget"
)

func TestEnvironmentSelectsDeclaredToolchain(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go9.9.9+auto")
	t.Setenv("GOMAXPROCS", "99")
	t.Setenv("CC", "/ambient/cc")
	t.Setenv("PKG_CONFIG", "/ambient/pkg-config")

	environment := gotarget.Environment(gotarget.Context{
		ToolchainVersion: "1.26.3",
		GOOS:             "linux",
		GOARCH:           "amd64",
		NativeToolEnv:    map[string]string{"CC": "/declared/cc", "PKG_CONFIG": "/declared/pkg-config-disabled"},
	})
	values := map[string]string{}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	if values["GOTOOLCHAIN"] != "go1.26.3" {
		t.Fatalf("GOTOOLCHAIN = %q, want exact declared toolchain", values["GOTOOLCHAIN"])
	}
	if values["GOOS"] != "linux" || values["GOARCH"] != "amd64" {
		t.Fatalf("target environment = GOOS=%q GOARCH=%q", values["GOOS"], values["GOARCH"])
	}
	if values["CC"] != "/declared/cc" || values["PKG_CONFIG"] != "/declared/pkg-config-disabled" {
		t.Fatalf("native tools leaked ambient environment: CC=%q PKG_CONFIG=%q", values["CC"], values["PKG_CONFIG"])
	}
	if want := strconv.Itoa(min(runtime.GOMAXPROCS(0), gotarget.AnalysisMaxProcs)); values["GOMAXPROCS"] != want {
		t.Fatalf("Go analysis concurrency = %q, want bounded subprocess concurrency", values["GOMAXPROCS"])
	}
}

func TestResolvedGoTargetRecordsContentAddressedToolchainAndNativeTools(t *testing.T) {
	context := gotarget.Context{
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
	target := Resource{Address: "app/go_target/production", Module: "app", Name: "production", Kind: "scenery.go-target", Spec: map[string]any{
		"role": "artifact", "platform": "fixed", "toolchain": map[string]any{"$ref": "go_toolchain.application"}, "module": map[string]any{"$ref": "go_module.application"},
		"packages": []any{"./..."}, "goos": "linux", "goarch": "amd64", "cgo": "enabled",
	}}
	diagnostics := validateGoTargets(t.TempDir(), []Resource{target})
	if !diagnosticsContain(diagnostics, "SCN6141") {
		t.Fatalf("fixed CGO target diagnostics = %#v", diagnostics)
	}
}
