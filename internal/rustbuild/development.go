package rustbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"scenery.sh/internal/devcache"
	"slices"
	"strings"
)

// DevelopmentInputs records the local package closure for each worker. Cargo
// resolves it once per declaration change, including transitive path deps.
type DevelopmentInputs struct {
	packages map[Service][]string
	owners   map[Service][]string
}

func DiscoverDevelopmentInputs(ctx context.Context, root string, services []Service) (*DevelopmentInputs, error) {
	inputs := &DevelopmentInputs{packages: make(map[Service][]string), owners: make(map[Service][]string)}
	err := withSnapshot(ctx, root, services, nil, func(snapshot, target, _ string) error {
		type selection struct{ workspace, toolchain string }
		graphs := make(map[selection]cargoGraph)
		for _, service := range services {
			key := selection{service.Workspace, service.Toolchain}
			graph, known := graphs[key]
			if !known {
				var err error
				graph, err = readCargoGraph(ctx, filepath.Join(snapshot, service.Workspace), target, service.Toolchain)
				if err != nil {
					return err
				}
				graphs[key] = graph
			}
			if err := inputs.addService(snapshot, service, graph); err != nil {
				return err
			}
		}
		return nil
	})
	return inputs, err
}

type cargoGraph struct {
	Packages []struct {
		ID           string
		ManifestPath string `json:"manifest_path"`
	}
	Resolve struct {
		Nodes []struct {
			ID   string
			Deps []struct {
				Pkg      string
				DepKinds []struct{ Kind string } `json:"dep_kinds"`
			}
		}
	}
}

func readCargoGraph(ctx context.Context, directory, target, toolchain string) (cargoGraph, error) {
	command := exec.CommandContext(ctx, "cargo", "+"+toolchain, "metadata", "--locked", "--format-version", "1")
	command.Dir, command.Env = directory, rustBuildEnvironment(target, directory, false)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		return cargoGraph{}, fmt.Errorf("cargo metadata: %w\n%s", err, stderr.String())
	}
	var graph cargoGraph
	err = json.Unmarshal(data, &graph)
	return graph, err
}

func (inputs *DevelopmentInputs) addService(snapshot string, service Service, graph cargoGraph) error {
	dirs := make(map[string]string)
	var entrypoint string
	for _, pkg := range graph.Packages {
		dir, err := filepath.Rel(snapshot, filepath.Dir(pkg.ManifestPath))
		if err != nil || !filepath.IsLocal(dir) {
			continue
		}
		dirs[pkg.ID] = dir
		inputs.owners[service] = append(inputs.owners[service], dir)
		if dir == filepath.Clean(service.ModuleDirectory) {
			entrypoint = pkg.ID
		}
	}
	if entrypoint == "" {
		return fmt.Errorf("cargo metadata has no package for %s", service.ModuleDirectory)
	}
	imports := make(map[string][]string)
	for _, node := range graph.Resolve.Nodes {
		for _, dep := range node.Deps {
			for _, kind := range dep.DepKinds {
				if kind.Kind != "dev" {
					imports[node.ID] = append(imports[node.ID], dep.Pkg)
					break
				}
			}
		}
	}
	seen := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if dir, local := dirs[id]; local {
			inputs.packages[service] = append(inputs.packages[service], dir)
		}
		for _, dep := range imports[id] {
			visit(dep)
		}
	}
	visit(entrypoint)
	return nil
}

// Consumes attributes a workspace-relative path to a worker. Nested packages
// own their own sources even if an outer Cargo member contains them.
func (inputs *DevelopmentInputs) Consumes(service Service, path string) bool {
	base := filepath.Base(path)
	workspace := filepath.Clean(service.Workspace)
	if filepath.Dir(path) == workspace && (base == "Cargo.toml" || base == "Cargo.lock") {
		return true
	}
	if base == "rust-toolchain" || base == "rust-toolchain.toml" || filepath.Base(filepath.Dir(path)) == ".cargo" {
		dir := filepath.Dir(path)
		if filepath.Base(dir) == ".cargo" {
			dir = filepath.Dir(dir)
		}
		if within(dir, workspace) {
			return true
		}
	}
	owner := ""
	for _, dir := range inputs.owners[service] {
		if len(dir) > len(owner) && within(dir, path) {
			owner = dir
		}
	}
	if owner == "" {
		return within(workspace, path)
	}
	for _, dir := range inputs.packages[service] {
		if dir == owner {
			return true
		}
	}
	return false
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

// Only supply the fast development default when the user did not select a
// profile. A Cargo config can include another config, so its presence is
// enough to defer to Cargo's own profile resolution.
func useDefaultDevDebug(directory string, env []string) bool {
	var cargoHome string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "CARGO_PROFILE_DEV_DEBUG":
			return false
		case "CARGO_HOME":
			cargoHome = value
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "Cargo.toml"))
	if err != nil || bytes.Contains(data, []byte("profile")) || bytes.Contains(data, []byte{'\\'}) {
		return false
	}
	hasConfig := func(dir string) bool {
		for _, name := range []string{"config", "config.toml"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
				return true
			}
		}
		return false
	}
	for dir := directory; ; dir = filepath.Dir(dir) {
		if hasConfig(filepath.Join(dir, ".cargo")) {
			return false
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if cargoHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		cargoHome = filepath.Join(home, ".cargo")
	}
	return !hasConfig(cargoHome)
}

// DevelopmentCommand projects the prepared Cargo workspace and local dependency graph.
func (inputs *DevelopmentInputs) DevelopmentCommand(root string, service Service) (string, []string, []string, []string, []string, error) {
	cache, err := devcache.Root()
	if err != nil {
		return "", nil, nil, nil, nil, err
	}
	directory := filepath.Join(root, ".scenery", "rust", "snapshot", service.Workspace)
	args := []string{"+" + service.Toolchain, "build", "--locked", "--package", service.Crate,
		"--bin", binaryName(service), "--message-format=json-render-diagnostics"}
	return directory, args, rustBuildEnvironment(filepath.Join(cache, "rust-target"), directory, true),
		slices.Clone(inputs.packages[service]), slices.Clone(inputs.owners[service]), nil
}
