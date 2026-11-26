package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/pythonbuild"
)

func (s *moduleScaffold) addPythonWorkspace(resources []graph.Resource) (string, error) {
	var existingToolchain string
	for _, resource := range resources {
		if resource.Kind == "scenery.python-toolchain" && existingToolchain == "" {
			existingToolchain = cp.StringValue(resource.Spec["version"])
		}
		if appWorkspaceTarget(resource, resources, "python") {
			return "", nil
		}
	}
	version := firstNonEmpty(existingToolchain, pythonbuild.DetectInstalledVersion(context.Background()), "3.14.6")
	return scaffoldWorkspaceDeclarations(resources, "python", s.Module, ".", version), nil
}

func (s *moduleScaffold) addJSWorkspace(root string, resources []graph.Resource) (string, error) {
	for _, resource := range resources {
		if appWorkspaceTarget(resource, resources, "js") {
			return "", nil
		}
	}
	if _, err := readScaffoldFile(root, "package.json"); errors.Is(err, os.ErrNotExist) {
		s.create("package.json", "{\n  \"private\": true,\n  \"type\": \"module\"\n}\n")
	} else if err != nil {
		return "", err
	}
	return scaffoldWorkspaceDeclarations(resources, "js", s.Module, ".", "1.3.14"), nil
}

func (s *moduleScaffold) addRustWorkspace(root string, resources []graph.Resource) (string, error) {
	var nestedWorkspace bool
	var existingToolchain string
	for _, resource := range resources {
		if resource.Kind == "scenery.rust-workspace" {
			nestedWorkspace = nestedWorkspace || filepath.Clean(cp.StringValue(resource.Spec["root"])) != "."
		}
		if resource.Kind == "scenery.rust-toolchain" && existingToolchain == "" {
			existingToolchain = cp.StringValue(resource.Spec["version"])
		}
		if appWorkspaceTarget(resource, resources, "rust") {
			toolchain := workerReference(resource, resource.Spec["toolchain"], "rust_toolchain")
			for _, candidate := range resources {
				if candidate.Address == toolchain {
					s.rustTool = cp.StringValue(candidate.Spec["version"])
					break
				}
			}
		}
	}
	s.cargoDir = "."
	manifest, err := readScaffoldFile(root, "Cargo.toml")
	// A pre-existing standalone crate must keep its workspace boundary and
	// lockfile. A sibling standalone workspace keeps Cargo from adopting it.
	if errors.Is(err, os.ErrNotExist) && nestedWorkspace {
		s.cargoDir = s.Module
	}
	var declarations string
	if s.rustTool == "" {
		s.rustTool = firstNonEmpty(existingToolchain, "1.99.1")
		declarations = scaffoldWorkspaceDeclarations(resources, "rust", s.Module, s.cargoDir, s.rustTool)
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		if s.cargoDir == "." {
			s.create("Cargo.toml", fmt.Sprintf("[workspace]\nresolver = \"3\"\nmembers = [%q]\n", s.Module))
		}
	case err != nil:
		return "", err
	default:
		updated, joinErr := addCargoMember(manifest, s.Module)
		if joinErr != nil {
			return "", joinErr
		}
		s.update("Cargo.toml", manifest, updated)
	}
	return declarations, nil
}

func appWorkspaceTarget(resource graph.Resource, resources []graph.Resource, runtime string) bool {
	if resource.Kind != "scenery."+runtime+"-target" || resource.Spec["role"] != "development" {
		return false
	}
	address := workerReference(resource, resource.Spec["workspace"], runtime+"_workspace")
	for _, workspace := range resources {
		if workspace.Address == address && filepath.Clean(cp.StringValue(workspace.Spec["root"])) == "." {
			return true
		}
	}
	return false
}

func scaffoldWorkspaceDeclarations(resources []graph.Resource, runtime, module, directory, version string) string {
	label := "module_" + module
	workspace := ""
	for _, resource := range resources {
		if resource.Kind == "scenery."+runtime+"-workspace" && filepath.Clean(cp.StringValue(resource.Spec["root"])) == directory {
			workspace = resource.Name
			break
		}
	}
	var b strings.Builder
	if workspace == "" {
		workspace = label
		fmt.Fprintf(&b, "%[1]s_workspace %[2]q { root = %[3]q }\n", runtime, workspace, directory)
	}
	fmt.Fprintf(&b, "%[1]s_toolchain %[2]q { version = %[3]q }\n", runtime, label, version)
	for _, role := range []string{"development", "artifact"} {
		address := "app/" + runtime + "_workspace/" + workspace
		exists := slices.ContainsFunc(resources, func(resource graph.Resource) bool {
			return resource.Kind == "scenery."+runtime+"-target" && resource.Spec["role"] == role &&
				workerReference(resource, resource.Spec["workspace"], runtime+"_workspace") == address
		})
		if !exists {
			fmt.Fprintf(&b, `
%[1]s_target %[2]q {
  role = %[3]q
  workspace = %[1]s_workspace.%[4]s
  toolchain = %[1]s_toolchain.%[5]s
}
`, runtime, label+"_"+role, role, workspace, label)
		}
	}
	return b.String()
}

var (
	cargoWorkspaceHeader = regexp.MustCompile(`(?m)^[ \t]*\[[ \t]*(?:workspace|"workspace"|'workspace')[ \t]*\][ \t]*(#.*)?\r?$`)
	cargoTableHeader     = regexp.MustCompile(`(?m)^[ \t]*\[[\[\w"']`)
	cargoMembersKey      = regexp.MustCompile(`(?m)^[ \t]*(?:members|"members"|'members')[ \t]*=[ \t]*`)
)

func addCargoMember(manifest []byte, name string) ([]byte, error) {
	header := cargoWorkspaceHeader.FindIndex(manifest)
	if header == nil {
		return fmt.Appendf(manifest, "\n[workspace]\nresolver = \"3\"\nmembers = [%q]\n", name), nil
	}
	table := manifest[header[1]:]
	if next := cargoTableHeader.FindIndex(table); next != nil {
		table = table[:next[0]]
	}
	key := cargoMembersKey.FindIndex(table)
	if key == nil {
		return insertScaffoldText(manifest, header[1], fmt.Sprintf("\nmembers = [%q]", name)), nil
	}
	if key[1] >= len(table) || table[key[1]] != '[' {
		return nil, preconditionErrorf("Cargo workspace members must be an array in Cargo.toml")
	}
	offset := header[1] + key[1] + 1
	// Prepending avoids changing member strings, comments or trailing commas.
	rest := strings.TrimSpace(string(manifest[offset:]))
	member := fmt.Sprintf("%q", name)
	if !strings.HasPrefix(rest, "]") {
		member += ", "
	}
	return insertScaffoldText(manifest, offset, member), nil
}

func (s *moduleScaffold) updateCargoLock(ctx context.Context, root string) error {
	if s.cargoDir == "" {
		return nil
	}
	lockfile := filepath.Join(s.cargoDir, "Cargo.lock")
	before, err := readScaffoldFile(root, lockfile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command := exec.CommandContext(ctx, "cargo", "+"+s.rustTool, "metadata", "--format-version", "1")
	command.Dir, command.Env = filepath.Join(root, s.cargoDir), envpolicy.Environ()
	var output tailOutput
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("resolve Cargo workspace lock: %w\n%s", err, output.String())
	}
	after, err := readScaffoldFile(root, lockfile)
	if err != nil {
		return err
	}
	if before == nil {
		s.CreatedFiles = append(s.CreatedFiles, lockfile)
		slices.Sort(s.CreatedFiles)
	} else if string(before) != string(after) {
		s.UpdatedFiles = append(s.UpdatedFiles, lockfile)
		slices.Sort(s.UpdatedFiles)
	}
	return nil
}
