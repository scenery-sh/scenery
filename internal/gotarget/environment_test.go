package gotarget

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestEnvironmentSelectsDeclaredToolchain(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go9.9.9+auto")
	t.Setenv("GOMAXPROCS", "99")
	t.Setenv("CC", "/ambient/cc")
	t.Setenv("PKG_CONFIG", "/ambient/pkg-config")

	environment := Environment(Context{
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
	if want := strconv.Itoa(min(runtime.GOMAXPROCS(0), AnalysisMaxProcs)); values["GOMAXPROCS"] != want {
		t.Fatalf("Go analysis concurrency = %q, want bounded subprocess concurrency", values["GOMAXPROCS"])
	}
}
