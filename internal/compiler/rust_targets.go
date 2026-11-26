package compiler

import (
	"os"
	"path/filepath"
	"regexp"
)

var rustCrateName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9-]*$`)

func validateRustTargets(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	workspaces := make(map[string]string)
	modules := make(map[string]Resource)
	targets := make(map[[2]string]string)
	var diagnostics []Diagnostic
	report := func(resource Resource, code, message string) {
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address})
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.module":
			modules[moduleInstancePath(resource)] = resource
		case "scenery.rust-workspace":
			relative := stringValue(resource.Spec["root"])
			directory := filepath.Join(root, filepath.FromSlash(relative))
			manifest := filepath.Join(directory, "Cargo.toml")
			info, err := os.Lstat(manifest)
			if relative == "" || filepath.IsAbs(relative) || !pathWithin(root, directory) || err != nil || !info.Mode().IsRegular() || rejectPathSymlinks(root, manifest) != nil {
				report(resource, "SCN6160", "rust_workspace root requires a workspace-relative regular Cargo.toml without symlinks")
				continue
			}
			if previous := workspaces[directory]; previous != "" {
				report(resource, "SCN6160", "rust_workspace root is also owned by "+previous)
				continue
			}
			workspaces[directory] = resource.Address
		case "scenery.rust-toolchain":
			if !semanticVersionExact(stringValue(resource.Spec["version"])) {
				report(resource, "SCN6161", "rust_toolchain version requires an exact release such as 1.99.1")
			}
		case "scenery.rust-target":
			role := stringValue(resource.Spec["role"])
			workspace := resolveResourceRef(resource, refString(resource.Spec["workspace"]), "rust_workspace")
			toolchain := resolveResourceRef(resource, refString(resource.Spec["toolchain"]), "rust_toolchain")
			if (role != "development" && role != "artifact") || byAddress[workspace].Kind != "scenery.rust-workspace" || byAddress[toolchain].Kind != "scenery.rust-toolchain" {
				report(resource, "SCN6162", "rust_target requires role development or artifact and resolved workspace and toolchain references")
				continue
			}
			key := [2]string{workspace, role}
			if previous := targets[key]; previous != "" {
				report(resource, "SCN6162", "rust_target duplicates the role and workspace of "+previous)
				continue
			}
			targets[key] = resource.Address
		}
	}
	for _, service := range resources {
		if service.Kind != "scenery.service" || stringValue(service.Spec["runtime"]) != "rust" {
			continue
		}
		module := modules[service.Module]
		metadata, _ := module.Spec["package"].(map[string]any)
		declared, _ := metadata["rust_contract"].(map[string]any)
		if !rustCrateName.MatchString(stringValue(declared["crate"])) {
			report(service, "SCN6163", "Rust service module requires rust_contract with a Cargo crate name")
			continue
		}
		directory := filepath.Join(root, filepath.FromSlash(stringValue(module.Spec["workspace_package_root"])))
		manifest := filepath.Join(directory, "Cargo.toml")
		info, err := os.Lstat(manifest)
		if err != nil || !info.Mode().IsRegular() || rejectPathSymlinks(root, manifest) != nil {
			report(service, "SCN6163", "Rust service module requires a regular Cargo.toml without symlinks")
			continue
		}
		covered := false
		for workspace, address := range workspaces {
			covered = covered || pathWithin(workspace, directory) && targets[[2]string{address, "development"}] != ""
		}
		if !covered {
			report(service, "SCN6163", "Rust service module requires a covering rust_target with role development")
		}
	}
	return diagnostics
}
