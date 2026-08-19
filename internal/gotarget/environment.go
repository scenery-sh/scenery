package gotarget

import (
	"runtime"
	"slices"
	"strconv"
	"strings"

	"scenery.sh/internal/envpolicy"
)

// Environment is the hermetic process environment implied by one Go target.
// The returned slice is part of implementation-revision identity; keep it
// byte-stable.
func Environment(target Context) []string {
	return hermetic(&target)
}

// Hermetic is Environment for a possibly nil target. A nil target uses the
// host GOOS/GOARCH with CGO disabled, matching the previous parse helper.
func Hermetic(target *Context) []string {
	return hermetic(target)
}

func hermetic(target *Context) []string {
	blocked := map[string]bool{
		"AR": true, "CC": true, "CXX": true, "PKG_CONFIG": true,
		"CGO_CFLAGS": true, "CGO_CPPFLAGS": true, "CGO_CXXFLAGS": true, "CGO_FFLAGS": true, "CGO_LDFLAGS": true,
		"CPATH": true, "C_INCLUDE_PATH": true, "CPLUS_INCLUDE_PATH": true, "LIBRARY_PATH": true, "LD_LIBRARY_PATH": true,
		"DYLD_LIBRARY_PATH": true, "DYLD_FALLBACK_LIBRARY_PATH": true,
		"CGO_ENABLED": true, "GOARCH": true, "GOENV": true, "GOEXPERIMENT": true, "GOFLAGS": true,
		"GOMAXPROCS": true, "GOOS": true, "GOPROXY": true, "GOTOOLCHAIN": true, "GOWORK": true,
		"GO386": true, "GOAMD64": true, "GOARM": true, "GOARM64": true, "GOMIPS": true,
		"GOMIPS64": true, "GOPPC64": true, "GORISCV64": true, "GOWASM": true,
	}
	processEnvironment := envpolicy.Environ()
	environment := make([]string, 0, len(processEnvironment)+10)
	for _, value := range processEnvironment {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			environment = append(environment, value)
		}
	}
	goos, goarch, cgo, experiments := runtime.GOOS, runtime.GOARCH, "0", ""
	if target != nil {
		goos, goarch = target.GOOS, target.GOARCH
		if target.CGOEnabled {
			cgo = "1"
		}
		experiments = strings.Join(target.Experiments, ",")
	}
	environment = append(environment,
		"CGO_ENABLED="+cgo,
		"GOARCH="+goarch,
		"GOENV=off",
		"GOEXPERIMENT="+experiments,
		"GOFLAGS=",
		"GOMAXPROCS="+strconv.Itoa(min(runtime.GOMAXPROCS(0), AnalysisMaxProcs)),
		"GOOS="+goos,
		"GOPROXY=off",
		"GOTOOLCHAIN="+resolvedGoToolchain(target),
		"GOWORK=off",
	)
	if target != nil {
		for name, value := range target.ArchitectureEnv {
			environment = append(environment, name+"="+value)
		}
		for name, value := range target.NativeToolEnv {
			environment = append(environment, name+"="+value)
		}
	}
	slices.Sort(environment)
	return environment
}

func resolvedGoToolchain(target *Context) string {
	if target == nil || strings.TrimSpace(target.ToolchainVersion) == "" {
		return "local"
	}
	return "go" + strings.TrimPrefix(strings.TrimSpace(target.ToolchainVersion), "go")
}
