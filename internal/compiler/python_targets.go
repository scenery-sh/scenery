package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

var pythonIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true,
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

type PythonWorkerTarget struct {
	Directory string
	Workspace string
	Entry     string
	Version   string
}

// IsValidPythonIdentifier reports whether name is a valid, non-keyword Python identifier.
func IsValidPythonIdentifier(name string) bool {
	return pythonIdentifier.MatchString(name) && !pythonKeywords[name]
}

func validatePythonTargets(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	workspaces := make(map[string]string)
	targets := make(map[[2]string]string)
	var diagnostics []Diagnostic
	report := func(resource Resource, code, message string) {
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address})
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.python-workspace":
			relative := stringValue(resource.Spec["root"])
			directory := filepath.Join(root, filepath.FromSlash(relative))
			info, err := os.Lstat(directory)
			if !filepath.IsLocal(relative) || err != nil || !info.IsDir() || rejectPathSymlinks(root, directory) != nil {
				report(resource, "SCN6168", "python_workspace root must be a workspace-relative directory without symlinks")
				continue
			}
			if previous := workspaces[directory]; previous != "" {
				report(resource, "SCN6168", "python_workspace root is also owned by "+previous)
			}
			workspaces[directory] = resource.Address
		case "scenery.python-toolchain":
			if !semanticVersionExact(stringValue(resource.Spec["version"])) {
				report(resource, "SCN6169", "python_toolchain version requires an exact Python release such as 3.14.6")
			}
		case "scenery.python-target":
			role := stringValue(resource.Spec["role"])
			workspace := resolveResourceRef(resource, refString(resource.Spec["workspace"]), "python_workspace")
			toolchain := resolveResourceRef(resource, refString(resource.Spec["toolchain"]), "python_toolchain")
			if (role != "development" && role != "artifact") || byAddress[workspace].Kind != "scenery.python-workspace" || byAddress[toolchain].Kind != "scenery.python-toolchain" {
				report(resource, "SCN6170", "python_target requires role development or artifact and resolved workspace and toolchain references")
				continue
			}
			key := [2]string{workspace, role}
			if previous := targets[key]; previous != "" {
				report(resource, "SCN6170", "python_target duplicates the role and workspace of "+previous)
			}
			targets[key] = resource.Address
		}
	}
	for _, resource := range resources {
		if resource.Kind != "scenery.service" || !IsPythonRuntime(stringValue(resource.Spec["runtime"])) {
			continue
		}
		if _, err := selectPythonWorkerTarget(root, resources, byAddress, resource, "development"); err != nil {
			report(resource, "SCN6171", err.Error())
		}
	}
	return diagnostics
}

func SelectPythonWorkerTarget(result *Result, service Resource, role string) (PythonWorkerTarget, error) {
	return selectPythonWorkerTarget(result.Root, result.Manifest.Resources, resourcesByAddress(result.Manifest), service, role)
}

func selectPythonWorkerTarget(root string, resources []Resource, byAddress map[string]Resource, service Resource, role string) (PythonWorkerTarget, error) {
	implementation, _ := service.Spec["implementation"].(map[string]any)
	if !IsValidPythonIdentifier(stringValue(implementation["constructor"])) {
		return PythonWorkerTarget{}, fmt.Errorf("python service constructor must name an exported function")
	}
	moduleIndex := slices.IndexFunc(resources, func(resource Resource) bool {
		return resource.Kind == "scenery.module" && moduleInstancePath(resource) == service.Module
	})
	if moduleIndex < 0 {
		return PythonWorkerTarget{}, fmt.Errorf("python service requires its own module directory")
	}
	module := resources[moduleIndex]
	metadata, _ := module.Spec["package"].(map[string]any)
	declaration, _ := metadata["python_contract"].(map[string]any)
	target := PythonWorkerTarget{
		Directory: stringValue(module.Spec["workspace_package_root"]),
		Entry:     stringValue(declaration["entry"]),
	}
	entryPath := filepath.Join(root, target.Directory, filepath.FromSlash(target.Entry))
	info, err := os.Lstat(entryPath)
	if !filepath.IsLocal(target.Directory) || !filepath.IsLocal(target.Entry) || filepath.Ext(target.Entry) != ".py" ||
		err != nil || !info.Mode().IsRegular() || rejectPathSymlinks(root, entryPath) != nil {
		return PythonWorkerTarget{}, fmt.Errorf("python_contract entry must be a regular module-relative .py source file")
	}

	best := -1
	for _, resource := range resources {
		if resource.Kind != "scenery.python-target" || stringValue(resource.Spec["role"]) != role {
			continue
		}
		workspace := byAddress[resolveResourceRef(resource, refString(resource.Spec["workspace"]), "python_workspace")]
		toolchain := byAddress[resolveResourceRef(resource, refString(resource.Spec["toolchain"]), "python_toolchain")]
		relative := filepath.FromSlash(stringValue(workspace.Spec["root"]))
		clean := filepath.Clean(relative)
		if !filepath.IsLocal(relative) || !pathWithin(filepath.Join(root, clean), filepath.Join(root, target.Directory)) || len(clean) <= best {
			continue
		}
		target.Workspace = clean
		target.Version = stringValue(toolchain.Spec["version"])
		best = len(clean)
	}
	if best < 0 || !semanticVersionExact(target.Version) {
		return PythonWorkerTarget{}, fmt.Errorf("python service requires a covering python_target with role %s", role)
	}
	return target, nil
}
