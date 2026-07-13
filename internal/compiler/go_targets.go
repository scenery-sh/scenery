package compiler

import (
	"go/build"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"scenery.sh/internal/parse"
)

func validateGoTargets(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	moduleRoots, moduleImports := map[string]string{}, map[string]string{}
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.go-module":
			rootValue, importPath := stringValue(resource.Spec["root"]), strings.TrimSuffix(stringValue(resource.Spec["import_path"]), "/")
			absolute := filepath.Clean(filepath.Join(root, filepath.FromSlash(rootValue)))
			if rootValue == "" || filepath.IsAbs(rootValue) || !pathWithin(root, absolute) {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6130", "Go module root must be a workspace-relative path", resource))
			} else if info, err := os.Lstat(absolute); err != nil || !info.IsDir() {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6130", "Go module root must be an available directory", resource))
			} else if err := rejectPathSymlinks(root, absolute); err != nil {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6130", "Go module root must not traverse symlinks", resource))
			}
			if importPath == "" || strings.HasPrefix(importPath, "/") || strings.Contains(importPath, "\\") {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6131", "Go module import_path must be portable", resource))
			}
			if previous := moduleRoots[absolute]; previous != "" {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6143", "duplicate Go module root also owned by "+previous, resource))
			}
			if previous := moduleImports[importPath]; previous != "" {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6143", "duplicate Go module import_path also owned by "+previous, resource))
			}
			moduleRoots[absolute], moduleImports[importPath] = resource.Address, resource.Address
		case "scenery.go-toolchain":
			version := strings.TrimPrefix(stringValue(resource.Spec["version"]), "go")
			if !semanticVersionExact(version) {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6132", "Go toolchain version must be exact semantic version", resource))
			}
			experiments := stringValues(resource.Spec["experiments"])
			if !sort.StringsAreSorted(experiments) || len(canonicalStrings(experiments)) != len(experiments) {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6133", "Go toolchain experiments must be sorted and unique", resource))
			}
		}
	}
	for _, resource := range resources {
		if resource.Kind != "scenery.go-target" {
			continue
		}
		if resource.Spec["extends"] != nil && resource.Spec["inherits"] != nil {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6134", "Go target cannot declare both extends and inherits", resource))
		}
		targets := map[string]Resource{}
		for _, candidate := range resources {
			if candidate.Kind == "scenery.go-target" {
				targets[candidate.Name] = candidate
			}
		}
		effective, err := effectiveGoTarget(resource, targets, nil)
		if err != nil {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6134", err.Error(), resource))
			continue
		}
		role := stringValue(effective["role"])
		if role != "contract" && role != "development" && role != "test" && role != "artifact" {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6135", "Go target role is unsupported", resource))
		}
		moduleAddress := resolveResourceRef(resource, refString(effective["module"]), "go_module")
		toolchainAddress := resolveResourceRef(resource, refString(effective["toolchain"]), "go_toolchain")
		if byAddress[moduleAddress].Kind != "scenery.go-module" || byAddress[toolchainAddress].Kind != "scenery.go-toolchain" {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6136", "Go target requires resolved go_module and go_toolchain references", resource))
		}
		platform, goos, goarch := stringValue(effective["platform"]), stringValue(effective["goos"]), stringValue(effective["goarch"])
		if platform != "host" && (goos == "" || goarch == "") {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6137", "fixed Go target requires goos and goarch", resource))
		}
		cgo := stringValue(effective["cgo"])
		if cgo != "" && cgo != "disabled" && cgo != "enabled" && cgo != "host" {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6138", "Go target cgo must be disabled, enabled, or host", resource))
		}
		if cgo == "host" && platform != "host" {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6138", "cgo = host is valid only for a host target", resource))
		}
		if cgo == "enabled" && platform != "host" {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6141", "fixed CGO artifact targets require the future content-addressed native-toolchain schema", resource))
		}
		if len(stringValues(effective["packages"])) == 0 {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6139", "Go target requires at least one package pattern", resource))
		}
		for _, flag := range stringValues(effective["go_flags"]) {
			if filepath.IsAbs(flag) || strings.HasPrefix(flag, "@") || strings.ContainsAny(flag, "$`\n\r") {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6140", "Go target flags must be portable and cannot use ambient shell or response files", resource))
			}
		}
		if environment, _ := effective["environment"].(map[string]any); len(environment) > 0 {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6141", "Go target environment requires a typed toolchain-provider schema", resource))
		}
		for _, field := range []string{"build_tags", "architecture_features"} {
			values := stringValues(effective[field])
			if !sort.StringsAreSorted(values) || len(canonicalStrings(values)) != len(values) {
				diagnostics = append(diagnostics, goTargetDiagnostic("SCN6142", field+" must be sorted and unique", resource))
			}
		}
		resolvedArch := goarch
		if platform == "host" {
			resolvedArch = goruntime.GOARCH
		}
		if _, _, err := goArchitectureContext(resolvedArch, stringValues(effective["architecture_features"])); err != nil {
			diagnostics = append(diagnostics, goTargetDiagnostic("SCN6142", err.Error(), resource))
		}
	}
	return diagnostics
}

func resolvedGoTargetContext(effective map[string]any, toolchain Resource, context *parse.GoTargetContext) map[string]any {
	resolved := cloneMapValue(effective)
	goos, goarch := stringValue(effective["goos"]), stringValue(effective["goarch"])
	if stringValue(effective["platform"]) == "host" {
		goos, goarch = goruntime.GOOS, goruntime.GOARCH
	}
	cgo := stringValue(effective["cgo"])
	if cgo == "" {
		if stringValue(effective["role"]) == "contract" || stringValue(effective["platform"]) != "host" {
			cgo = "disabled"
		} else {
			cgo = "host"
		}
	}
	if cgo == "host" {
		cgo = map[bool]string{true: "enabled", false: "disabled"}[build.Default.CgoEnabled]
	}
	_, architectureFeatures, _ := goArchitectureContext(goarch, stringValues(effective["architecture_features"]))
	platform := map[string]any{"goos": goos, "goarch": goarch, "cgo": cgo, "architecture_features": architectureFeatures}
	selectedToolchain := map[string]any{"declared_version": toolchain.Spec["version"], "selected_version": strings.TrimPrefix(stringValue(toolchain.Spec["version"]), "go"), "compiler": goruntime.Compiler, "experiments": toolchain.Spec["experiments"]}
	if context != nil {
		platform["native_tools"] = context.NativeToolIdentities
		selectedToolchain["distribution"] = context.ToolchainIdentity
	}
	resolved["resolved_platform"] = platform
	resolved["resolved_toolchain"] = selectedToolchain
	return resolved
}

func semanticVersionExact(value string) bool {
	_, err := parseSemanticVersion(value)
	return err == nil && strings.TrimSpace(value) == value
}

func goTargetDiagnostic(code, message string, resource Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address}
}
