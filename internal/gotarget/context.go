// Package gotarget is the tiny shared Go target-context type used by both
// compiler and parse. Compiler must not import parse just for these values.
package gotarget

type Context struct {
	ModuleRoot           string
	Patterns             []string
	ToolchainVersion     string
	GOOS                 string
	GOARCH               string
	CGOEnabled           bool
	Experiments          []string
	BuildTags            []string
	BuildFlags           []string
	ArchitectureEnv      map[string]string
	NativeToolEnv        map[string]string
	ToolchainIdentity    map[string]string
	NativeToolIdentities []map[string]string
}

const AnalysisMaxProcs = 2
