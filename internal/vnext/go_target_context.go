package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"scenery.sh/internal/parse"
)

type goVerificationTarget struct {
	Resource  Resource
	Effective map[string]any
	Context   parse.GoTargetContext
}

type GoBuildTarget struct {
	Name      string
	Address   string
	Role      string
	Effective map[string]any
	Resolved  map[string]any
	Context   parse.GoTargetContext
}

func ResolveGoBuildTarget(result *Result, name, defaultRole string) (GoBuildTarget, error) {
	if result == nil || result.Manifest == nil {
		return GoBuildTarget{}, fmt.Errorf("valid contract is required")
	}
	targets := map[string]Resource{}
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.go-target/v1" {
			targets[resource.Name] = resource
		}
	}
	if name == "" {
		for _, candidate := range sortedResourceNames(targets) {
			effective, err := effectiveGoTarget(targets[candidate], targets, nil)
			if err != nil {
				return GoBuildTarget{}, err
			}
			if stringValue(effective["role"]) == defaultRole {
				name = candidate
				break
			}
		}
	}
	target := targets[name]
	if target.Address == "" {
		return GoBuildTarget{}, fmt.Errorf("Go target %q is unavailable", name)
	}
	resolved, err := resolveGoVerificationTarget(result, targets, target)
	if err != nil {
		return GoBuildTarget{}, err
	}
	byAddress := resourcesByAddress(result.Manifest)
	toolchain := byAddress[resolveResourceRef(target, refString(resolved.Effective["toolchain"]), "go_toolchain")]
	return GoBuildTarget{
		Name: target.Name, Address: target.Address, Role: stringValue(resolved.Effective["role"]), Effective: resolved.Effective,
		Resolved: resolvedGoTargetContext(resolved.Effective, toolchain, &resolved.Context), Context: resolved.Context,
	}, nil
}

func goVerificationTargets(result *Result) ([]goVerificationTarget, error) {
	resources := result.Manifest.Resources
	targets := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind == "scenery.go-target/v1" {
			targets[resource.Name] = resource
		}
	}
	selected := make([]Resource, 0, len(targets))
	for _, name := range sortedResourceNames(targets) {
		target := targets[name]
		effective, err := effectiveGoTarget(target, targets, nil)
		if err != nil {
			return nil, err
		}
		verify, _ := effective["verify_by_default"].(bool)
		if verify {
			selected = append(selected, target)
		}
	}
	if len(selected) == 0 {
		for _, name := range sortedResourceNames(targets) {
			target := targets[name]
			effective, err := effectiveGoTarget(target, targets, nil)
			if err != nil {
				return nil, err
			}
			if stringValue(effective["role"]) == "development" {
				selected = append(selected, target)
				break
			}
		}
	}
	resolved := make([]goVerificationTarget, 0, len(selected))
	for _, target := range selected {
		resolvedTarget, err := resolveGoVerificationTarget(result, targets, target)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedTarget)
	}
	return resolved, nil
}

func resolveGoVerificationTarget(result *Result, targets map[string]Resource, target Resource) (goVerificationTarget, error) {
	byAddress := resourcesByAddress(result.Manifest)
	effective, err := effectiveGoTarget(target, targets, nil)
	if err != nil {
		return goVerificationTarget{}, err
	}
	module := byAddress[resolveResourceRef(target, refString(effective["module"]), "go_module")]
	toolchain := byAddress[resolveResourceRef(target, refString(effective["toolchain"]), "go_toolchain")]
	goos, goarch := stringValue(effective["goos"]), stringValue(effective["goarch"])
	if stringValue(effective["platform"]) == "host" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	cgo := stringValue(effective["cgo"])
	cgoEnabled := cgo == "enabled" || cgo == "host" && build.Default.CgoEnabled
	architecture, _, err := goArchitectureContext(goarch, stringValues(effective["architecture_features"]))
	if err != nil {
		return goVerificationTarget{}, fmt.Errorf("%s: %w", target.Address, err)
	}
	context := parse.GoTargetContext{
		ModuleRoot:       filepath.Join(result.Root, filepath.FromSlash(stringValue(module.Spec["root"]))),
		Patterns:         stringValues(effective["packages"]),
		ToolchainVersion: strings.TrimPrefix(stringValue(toolchain.Spec["version"]), "go"),
		GOOS:             goos,
		GOARCH:           goarch,
		CGOEnabled:       cgoEnabled,
		Experiments:      stringValues(toolchain.Spec["experiments"]),
		BuildTags:        stringValues(effective["build_tags"]),
		BuildFlags:       stringValues(effective["go_flags"]),
		ArchitectureEnv:  architecture,
	}
	if err := resolveGoToolIdentities(&context); err != nil {
		return goVerificationTarget{}, fmt.Errorf("%s: %w", target.Address, err)
	}
	return goVerificationTarget{Resource: target, Effective: effective, Context: context}, nil
}

func resolveGoToolIdentities(target *parse.GoTargetContext) error {
	if target == nil {
		return fmt.Errorf("Go target context is required")
	}
	command := exec.Command("go", "env", "-json", "GOROOT", "CC", "CXX")
	command.Env = parse.GoTargetEnvironment(*target)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("resolve declared Go toolchain: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var environment struct {
		GOROOT string
		CC     string
		CXX    string
	}
	if err := json.Unmarshal(output, &environment); err != nil || environment.GOROOT == "" {
		return fmt.Errorf("resolve declared Go toolchain metadata")
	}
	goBinary := filepath.Join(environment.GOROOT, "bin", map[bool]string{true: "go.exe", false: "go"}[runtime.GOOS == "windows"])
	compiler := filepath.Join(environment.GOROOT, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, map[bool]string{true: "compile.exe", false: "compile"}[runtime.GOOS == "windows"])
	goDigest, goPath, err := digestExecutable(goBinary)
	if err != nil {
		return fmt.Errorf("resolve selected Go command: %w", err)
	}
	compilerDigest, compilerPath, err := digestExecutable(compiler)
	if err != nil {
		return fmt.Errorf("resolve selected Go compiler: %w", err)
	}
	identityBytes := []byte(goDigest + "\x00" + compilerDigest)
	identityDigest := sha256.Sum256(identityBytes)
	target.ToolchainIdentity = map[string]string{
		"identity": "go" + strings.TrimPrefix(target.ToolchainVersion, "go") + "/" + runtime.GOOS + "/" + runtime.GOARCH,
		"digest":   "sha256:" + hex.EncodeToString(identityDigest[:]), "go_command": goPath, "go_command_digest": goDigest,
		"compiler": compilerPath, "compiler_digest": compilerDigest,
	}
	if !target.CGOEnabled {
		return nil
	}
	target.NativeToolEnv = map[string]string{"PKG_CONFIG": filepath.Join(environment.GOROOT, ".scenery-pkg-config-disabled")}
	for _, candidate := range []struct{ name, command string }{{"cc", environment.CC}, {"cxx", environment.CXX}} {
		path, err := exec.LookPath(candidate.command)
		if err != nil {
			return fmt.Errorf("resolve declared-target %s command %q: %w", candidate.name, candidate.command, err)
		}
		digest, resolved, err := digestExecutable(path)
		if err != nil {
			return fmt.Errorf("resolve declared-target %s command: %w", candidate.name, err)
		}
		environmentName := map[string]string{"cc": "CC", "cxx": "CXX"}[candidate.name]
		target.NativeToolEnv[environmentName] = resolved
		target.NativeToolIdentities = append(target.NativeToolIdentities, map[string]string{"name": candidate.name, "path": resolved, "digest": digest})
	}
	return nil
}

func digestExecutable(path string) (string, string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s is not a regular file", resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), resolved, nil
}

func goArchitectureContext(goarch string, features []string) (map[string]string, []string, error) {
	features = append([]string(nil), features...)
	if len(features) == 0 {
		switch goarch {
		case "amd64":
			features = []string{"amd64.v1"}
		case "arm64":
			features = []string{"arm64.v8.0"}
		}
	}
	sort.Strings(features)
	if len(features) == 0 {
		return map[string]string{}, features, nil
	}
	prefix, variable := goarch+".", map[string]string{
		"386": "GO386", "amd64": "GOAMD64", "arm": "GOARM", "arm64": "GOARM64",
		"mips": "GOMIPS", "mips64": "GOMIPS64", "ppc64": "GOPPC64", "riscv64": "GORISCV64", "wasm": "GOWASM",
	}[goarch]
	if variable == "" {
		return nil, nil, fmt.Errorf("architecture features are unsupported for GOARCH %s", goarch)
	}
	values := make([]string, 0, len(features))
	for _, feature := range features {
		if !strings.HasPrefix(feature, prefix) || strings.TrimPrefix(feature, prefix) == "" {
			return nil, nil, fmt.Errorf("architecture feature %q does not match GOARCH %s", feature, goarch)
		}
		values = append(values, strings.TrimPrefix(feature, prefix))
	}
	if goarch == "amd64" && (len(values) != 1 || values[0] < "v1" || values[0] > "v4") {
		return nil, nil, fmt.Errorf("amd64 architecture requires exactly one of amd64.v1 through amd64.v4")
	}
	return map[string]string{variable: strings.Join(values, ",")}, features, nil
}
