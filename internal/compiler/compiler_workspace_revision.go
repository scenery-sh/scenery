package compiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/scn"
)

func computeWorkspaceRevision(root string, sources []*Source) (string, error) {
	entries := map[string][]byte{}
	for _, source := range sources {
		if source.External {
			continue
		}
		entries[source.Relative] = source.Bytes
	}
	declared, err := declaredWorkspaceEntries(root, sources)
	if err != nil {
		return "", err
	}
	maps.Copy(entries, declared)
	return workspaceRevisionForEntries(entries), nil
}

func workspaceRevisionForEntries(entries map[string][]byte) string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte("scenery.workspace-revision\x00"))
	for _, path := range paths {
		_ = binary.Write(h, binary.BigEndian, uint64(len([]byte(path))))
		_, _ = h.Write([]byte(path))
		_ = binary.Write(h, binary.BigEndian, uint64(len(entries[path])))
		_, _ = h.Write(entries[path])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// WorkspaceRevisionInput names one non-declaration file that contributes to
// the exact workspace revision. Implementation is false for files whose bytes
// also affect the compiled contract graph (for example a view SQL module).
type WorkspaceRevisionInput struct {
	Path           string
	Implementation bool
	// Present distinguishes a consumed file from a relevant absent resolver
	// alternative. Callers retain both states so a newly appearing higher-
	// priority module or optional revision input invalidates the snapshot.
	Present bool
}

// WorkspaceRevisionInputs resolves the complete current membership selected by
// a compiled graph. Callers capture these files together with the declaration
// sources, then use BindCapturedWorkspaceRevision without another tree read.
func WorkspaceRevisionInputs(result *Result) ([]WorkspaceRevisionInput, error) {
	return WorkspaceRevisionInputsWithGenerated(result, nil)
}

// WorkspaceRevisionInputsWithGenerated is WorkspaceRevisionInputs with an
// already captured generated-path set. It avoids repeating descriptor
// discovery in long-lived watch owners that necessarily classified those
// paths before scanning authored files.
func WorkspaceRevisionInputsWithGenerated(result *Result, generated map[string]bool) ([]WorkspaceRevisionInput, error) {
	if result == nil {
		return nil, errors.New("compiler result is unavailable")
	}
	paths, err := workspaceRevisionInputPathsWithGenerated(result.Root, result.Sources, generated)
	if err != nil {
		return nil, err
	}
	inputs := make([]WorkspaceRevisionInput, 0, len(paths))
	declarations := make(map[string]bool, len(result.Sources))
	for _, source := range result.Sources {
		if source != nil && !source.External {
			declarations[source.Relative] = true
		}
	}
	for _, path := range paths {
		if declarations[path.relative] {
			continue
		}
		inputs = append(inputs, WorkspaceRevisionInput{Path: path.relative, Implementation: path.implementation, Present: path.present})
	}
	return inputs, nil
}

// BindCapturedWorkspaceRevision binds an otherwise unchanged graph to exact
// captured revision bytes. The caller supplies precisely the membership from
// WorkspaceRevisionInputs; no filesystem reads occur here.
func BindCapturedWorkspaceRevision(result *Result, captured map[string][]byte) error {
	if result == nil {
		return errors.New("compiler result is unavailable")
	}
	entries := make(map[string][]byte, len(result.Sources)+len(captured))
	for _, source := range result.Sources {
		if source == nil || source.External {
			continue
		}
		entries[source.Relative] = source.Bytes
	}
	for path, data := range captured {
		clean := filepath.ToSlash(filepath.Clean(path))
		if path == "" || filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || forbiddenWorkspacePath(clean) {
			return fmt.Errorf("captured workspace revision input is unsafe: %s", path)
		}
		if _, declared := entries[clean]; declared {
			return fmt.Errorf("captured workspace revision input duplicates declaration source: %s", clean)
		}
		entries[clean] = data
	}
	result.WorkspaceRevision = workspaceRevisionForEntries(entries)
	return nil
}

// RefreshWorkspaceRevision re-hashes an unchanged compiler result after its
// already-verified generated artifacts have been materialized.
func RefreshWorkspaceRevision(result *Result) error {
	if result == nil {
		return errors.New("compiler result is unavailable")
	}
	revision, err := computeWorkspaceRevision(result.Root, result.Sources)
	if err != nil {
		return err
	}
	result.WorkspaceRevision = revision
	return nil
}

func declaredWorkspaceEntries(root string, sources []*Source) (map[string][]byte, error) {
	paths, err := workspaceRevisionInputPaths(root, sources)
	if err != nil {
		return nil, err
	}
	entries := make(map[string][]byte, len(paths))
	for _, input := range paths {
		if !input.present {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.relative)))
		if err != nil {
			return nil, err
		}
		entries[input.relative] = data
	}
	return entries, nil
}

type workspaceRevisionInputPath struct {
	relative       string
	implementation bool
	present        bool
}

func workspaceRevisionInputPaths(root string, sources []*Source) ([]workspaceRevisionInputPath, error) {
	return workspaceRevisionInputPathsWithGenerated(root, sources, nil)
}

func workspaceRevisionInputPathsWithGenerated(root string, sources []*Source, generatedPaths map[string]bool) ([]workspaceRevisionInputPath, error) {
	resourcePaths, err := declaredResourceFileInputs(root, sources)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]workspaceRevisionInputPath, len(resourcePaths))
	add := func(rel string, implementation, present bool) {
		rel = filepath.ToSlash(rel)
		if current, exists := paths[rel]; exists {
			// Graph inputs dominate implementation-only classification and a
			// present observation dominates a semantic absence.
			implementation = implementation && current.implementation
			present = present || current.present
		}
		paths[rel] = workspaceRevisionInputPath{relative: rel, implementation: implementation, present: present}
	}
	for _, input := range resourcePaths {
		add(input.relative, false, input.present)
	}
	lockPath := filepath.Join(root, scn.AppLockFilename)
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must be a regular workspace file", scn.AppLockFilename)
		}
		add(scn.AppLockFilename, false, true)
	} else if errors.Is(err, os.ErrNotExist) {
		add(scn.AppLockFilename, false, false)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var workspace *Block
	for _, source := range sources {
		if source.Relative != scn.AppFilename {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "workspace" {
				workspace = block
			}
		}
	}
	if workspace == nil {
		return sortedWorkspaceRevisionInputPaths(paths), nil
	}
	managedRoots, err := workspaceManagedGeneratedRoots(workspace)
	if err != nil {
		return nil, err
	}
	if generatedPaths == nil {
		generatedPaths, err = GeneratedPaths(root)
		if err != nil {
			return nil, err
		}
	}
	for _, implementationRoot := range workspace.Blocks {
		if implementationRoot.Type != "implementation_root" {
			continue
		}
		rootPath, ok := literalString(implementationRoot, "path")
		if !ok || filepath.IsAbs(rootPath) || strings.HasPrefix(filepath.Clean(rootPath), "..") {
			return nil, fmt.Errorf("workspace implementation_root requires a workspace-relative path")
		}
		includes := literalStringList(implementationRoot, "revision_include")
		excludes := literalStringList(implementationRoot, "revision_exclude")
		if err := validateWorkspaceGlobs(append(append([]string(nil), includes...), excludes...)); err != nil {
			return nil, err
		}
		includeMatcher, excludeMatcher := newGlobMatcher(includes), newGlobMatcher(excludes)
		walkRoot := filepath.Join(root, filepath.FromSlash(rootPath))
		if err := rejectPathSymlinks(root, walkRoot); err != nil {
			return nil, fmt.Errorf("workspace implementation_root %s: %w", rootPath, err)
		}
		err := filepath.WalkDir(walkRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			workspaceRelative, err := filepath.Rel(root, filePath)
			if err != nil {
				return err
			}
			workspaceRelative = filepath.ToSlash(workspaceRelative)
			if generatedPaths[workspaceRelative] && workspacePathWithinManagedRoot(workspaceRelative, managedRoots) {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == ".scenery" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			relToImplementation, err := filepath.Rel(walkRoot, filePath)
			if err != nil {
				return err
			}
			relToImplementation = filepath.ToSlash(relToImplementation)
			included, excluded := includeMatcher.matches(relToImplementation), excludeMatcher.matches(relToImplementation)
			if !included || excluded {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("workspace revision input is a symlink: %s", filePath)
			}
			add(workspaceRelative, true, true)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	seenRevisionInputs := map[string]bool{}
	for _, revisionInput := range workspace.Blocks {
		if revisionInput.Type != "revision_input" {
			continue
		}
		optional := false
		if expression, ok := revisionInput.Attributes["optional"]; ok {
			optional, _ = expression.Value.(bool)
		}
		for _, declared := range literalStringList(revisionInput, "paths") {
			clean := filepath.ToSlash(filepath.Clean(declared))
			if declared == "" || filepath.IsAbs(declared) || clean == "." || strings.HasPrefix(clean, "../") || forbiddenWorkspacePath(clean) {
				return nil, fmt.Errorf("revision_input path must be a safe exact workspace file: %s", declared)
			}
			if seenRevisionInputs[clean] {
				return nil, fmt.Errorf("revision_input path is declared more than once: %s", clean)
			}
			seenRevisionInputs[clean] = true
			path := filepath.Join(root, filepath.FromSlash(clean))
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) && optional {
				add(clean, true, false)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("revision_input %s: %w", clean, err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("revision_input must be a regular non-symlink file: %s", clean)
			}
			if err := rejectPathSymlinks(root, path); err != nil {
				return nil, fmt.Errorf("revision_input %s: %w", clean, err)
			}
			add(clean, true, true)
		}
	}
	return sortedWorkspaceRevisionInputPaths(paths), nil
}

func sortedWorkspaceRevisionInputPaths(paths map[string]workspaceRevisionInputPath) []workspaceRevisionInputPath {
	result := make([]workspaceRevisionInputPath, 0, len(paths))
	for _, input := range paths {
		result = append(result, input)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].relative < result[j].relative })
	return result
}

func workspaceManagedGeneratedRoots(workspace *Block) ([]string, error) {
	if workspace == nil {
		return nil, nil
	}
	roots := literalStringList(workspace, "managed_generated_roots")
	result := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, declared := range roots {
		clean := filepath.ToSlash(filepath.Clean(declared))
		if declared == "" || filepath.IsAbs(declared) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || forbiddenWorkspacePath(clean) {
			return nil, fmt.Errorf("managed_generated_roots requires a safe workspace-relative path: %s", declared)
		}
		if !seen[clean] {
			seen[clean] = true
			result = append(result, clean)
		}
	}
	sort.Strings(result)
	return result, nil
}

func workspacePathWithinManagedRoot(path string, roots []string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func validateWorkspaceGlobs(patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "" || filepath.IsAbs(pattern) || strings.Contains(pattern, "\\") || strings.ContainsAny(pattern, "[]\x00") || pathpkg.Clean(pattern) != pattern || strings.HasPrefix(pattern, "../") || pattern == ".." {
			return fmt.Errorf("workspace revision glob is invalid: %s", pattern)
		}
		for segment := range strings.SplitSeq(pattern, "/") {
			if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, "**") && segment != "**" {
				return fmt.Errorf("workspace revision glob is invalid: %s", pattern)
			}
			if segment == "**" {
				continue
			}
		}
	}
	return nil
}

func forbiddenWorkspacePath(path string) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		switch segment {
		case ".git", ".hg", ".svn", "node_modules", ".scenery":
			return true
		}
	}
	return false
}

func declaredResourceFileInputs(root string, sources []*Source) ([]workspaceRevisionInputPath, error) {
	paths := map[string]workspaceRevisionInputPath{}
	add := func(path string, present bool) {
		paths[path] = workspaceRevisionInputPath{relative: path, present: present}
	}
	for _, source := range sources {
		for _, block := range source.Blocks {
			var declarations []string
			switch block.Type {
			case "view":
				for _, child := range block.Blocks {
					if child.Type == "implementation" {
						if file, ok := literalString(child, "file"); ok {
							declarations = append(declarations, file)
						}
					}
				}
			case "renderer":
				if module, ok := literalString(block, "module"); ok {
					declarations = append(declarations, module)
				}
			}
			for _, declared := range declarations {
				if filepath.IsAbs(declared) || strings.HasPrefix(filepath.Clean(declared), "..") {
					return nil, fmt.Errorf("declared resource file must be workspace-relative: %s", declared)
				}
				path := filepath.Clean(filepath.Join(filepath.Dir(source.Path), filepath.FromSlash(declared)))
				if !pathWithin(root, path) {
					return nil, fmt.Errorf("declared resource file escapes workspace: %s", declared)
				}
				readPaths := []string{path}
				if block.Type == "renderer" {
					readPaths = nil
					resolved := false
					for _, candidate := range declaredModuleCandidates(path) {
						info, statErr := os.Stat(candidate)
						if statErr != nil || !info.Mode().IsRegular() {
							readPaths = append(readPaths, candidate)
							continue
						}
						readPaths = append(readPaths, candidate)
						resolved = true
						break
					}
					if !resolved {
						return nil, fmt.Errorf("read declared resource file %s: file is unavailable", declared)
					}
				} else {
					if err := rejectPathSymlinks(root, path); err != nil {
						return nil, fmt.Errorf("read declared resource file %s: %w", declared, err)
					}
					if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
						if statErr != nil {
							return nil, fmt.Errorf("read declared resource file %s: %w", declared, statErr)
						}
						return nil, fmt.Errorf("read declared resource file %s: file is unavailable", declared)
					}
				}
				selected := false
				for _, readPath := range readPaths {
					info, statErr := os.Stat(readPath)
					present := statErr == nil && info.Mode().IsRegular()
					if present {
						if err := rejectPathSymlinks(root, readPath); err != nil {
							return nil, fmt.Errorf("read declared resource file %s: %w", declared, err)
						}
						selected = true
					}
					relative, err := filepath.Rel(root, readPath)
					if err != nil {
						return nil, err
					}
					add(filepath.ToSlash(relative), present)
					if selected {
						break
					}
				}
			}
		}
	}
	result := make([]workspaceRevisionInputPath, 0, len(paths))
	for _, input := range paths {
		result = append(result, input)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].relative < result[j].relative })
	return result, nil
}
