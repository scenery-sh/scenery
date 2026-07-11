package vnext

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

func computeWorkspaceRevision(root string, sources []*Source, migration *Migration) (string, error) {
	entries := map[string][]byte{}
	for _, source := range sources {
		if source.External {
			continue
		}
		entries[source.Relative] = source.Bytes
	}
	if migration != nil {
		b, err := os.ReadFile(filepath.Join(root, "scenery.migration.scn"))
		if err != nil {
			return "", err
		}
		entries["scenery.migration.scn"] = b
		ledgerRoot := filepath.Join(root, "scenery.migration.ledger")
		if info, statErr := os.Lstat(ledgerRoot); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("scenery.migration.ledger must be a workspace directory")
			}
			if walkErr := filepath.WalkDir(ledgerRoot, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if path == ledgerRoot || entry.IsDir() {
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
					return fmt.Errorf("migration ledger contains a non-regular file")
				}
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				relative, _ := filepath.Rel(root, path)
				entries[filepath.ToSlash(relative)] = data
				return nil
			}); walkErr != nil {
				return "", walkErr
			}
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
	}
	lockPath := filepath.Join(root, "scenery.lock.scn")
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("scenery.lock.scn must be a regular workspace file")
		}
		b, err := os.ReadFile(lockPath)
		if err != nil {
			return "", err
		}
		entries["scenery.lock.scn"] = b
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	declared, err := declaredWorkspaceEntries(root, sources)
	if err != nil {
		return "", err
	}
	for path, bytes := range declared {
		entries[path] = bytes
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte("scenery.workspace-revision.v1\x00"))
	for _, path := range paths {
		_ = binary.Write(h, binary.BigEndian, uint64(len([]byte(path))))
		_, _ = h.Write([]byte(path))
		_ = binary.Write(h, binary.BigEndian, uint64(len(entries[path])))
		_, _ = h.Write(entries[path])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// refreshWorkspaceRevision re-hashes an unchanged compiler result after its
// already-verified generated artifacts have been materialized.
func refreshWorkspaceRevision(result *Result) error {
	if result == nil {
		return errors.New("compiler result is unavailable")
	}
	revision, err := computeWorkspaceRevision(result.Root, result.Sources, result.Migration)
	if err != nil {
		return err
	}
	result.WorkspaceRevision = revision
	return nil
}

func declaredWorkspaceEntries(root string, sources []*Source) (map[string][]byte, error) {
	entries, err := declaredResourceFileEntries(root, sources)
	if err != nil {
		return nil, err
	}
	var workspace *Block
	for _, source := range sources {
		if source.Relative != "scenery.scn" {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "workspace" {
				workspace = block
			}
		}
	}
	if workspace == nil {
		return entries, nil
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
		walkRoot := filepath.Join(root, filepath.FromSlash(rootPath))
		if err := rejectPathSymlinks(root, walkRoot); err != nil {
			return nil, fmt.Errorf("workspace implementation_root %s: %w", rootPath, err)
		}
		err := filepath.WalkDir(walkRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
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
			included, excluded := matchesAnyGlob(includes, relToImplementation), matchesAnyGlob(excludes, relToImplementation)
			if !included || excluded {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("workspace revision input is a symlink: %s", filePath)
			}
			workspaceRelative, err := filepath.Rel(root, filePath)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}
			entries[filepath.ToSlash(workspaceRelative)] = b
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
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			entries[clean] = data
		}
	}
	for _, managedRoot := range literalStringList(workspace, "managed_generated_roots") {
		generatedEntries, err := managedGeneratedEntries(root, managedRoot)
		if err != nil {
			return nil, err
		}
		for path, data := range generatedEntries {
			entries[path] = data
		}
	}
	return entries, nil
}

func validateWorkspaceGlobs(patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "" || filepath.IsAbs(pattern) || strings.Contains(pattern, "\\") || strings.ContainsAny(pattern, "[]\x00") || pathpkg.Clean(pattern) != pattern || strings.HasPrefix(pattern, "../") || pattern == ".." {
			return fmt.Errorf("workspace revision glob is invalid: %s", pattern)
		}
		for _, segment := range strings.Split(pattern, "/") {
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
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		switch segment {
		case ".git", ".hg", ".svn", "node_modules", ".scenery":
			return true
		}
	}
	return false
}

func declaredResourceFileEntries(root string, sources []*Source) (map[string][]byte, error) {
	entries := map[string][]byte{}
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
				readPath := path
				if block.Type == "renderer" {
					resolved, ok := resolveDeclaredModulePath(path)
					if !ok {
						return nil, fmt.Errorf("read declared resource file %s: file is unavailable", declared)
					}
					readPath = resolved
				}
				if err := rejectPathSymlinks(root, readPath); err != nil {
					return nil, fmt.Errorf("read declared resource file %s: %w", declared, err)
				}
				data, err := os.ReadFile(readPath)
				if err != nil {
					return nil, fmt.Errorf("read declared resource file %s: %w", declared, err)
				}
				relative, err := filepath.Rel(root, readPath)
				if err != nil {
					return nil, err
				}
				entries[filepath.ToSlash(relative)] = data
			}
		}
	}
	return entries, nil
}

func managedGeneratedEntries(root, declaredRoot string) (map[string][]byte, error) {
	entries := map[string][]byte{}
	if declaredRoot == "" || filepath.IsAbs(declaredRoot) || strings.HasPrefix(filepath.Clean(declaredRoot), "..") {
		return nil, fmt.Errorf("managed generated root must be workspace-relative")
	}
	absRoot := filepath.Join(root, filepath.FromSlash(declaredRoot))
	if !pathExists(absRoot) {
		return entries, nil
	}
	if err := rejectPathSymlinks(root, absRoot); err != nil {
		return nil, fmt.Errorf("managed generated root %s: %w", declaredRoot, err)
	}
	err := filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed generated artifact is a symlink: %s", path)
		}
		if !strings.HasSuffix(entry.Name(), ".json") || !strings.HasPrefix(entry.Name(), "scenery.") || !strings.Contains(entry.Name(), "generated") {
			return nil
		}
		descriptorBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var descriptor struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(descriptorBytes, &descriptor); err != nil {
			return fmt.Errorf("read generated descriptor %s: %w", path, err)
		}
		descriptorRel, _ := filepath.Rel(root, path)
		entries[filepath.ToSlash(descriptorRel)] = descriptorBytes
		descriptorRoot := filepath.Dir(path)
		for _, relative := range descriptor.Files {
			artifact := filepath.Clean(filepath.Join(descriptorRoot, filepath.FromSlash(relative)))
			if !pathWithin(descriptorRoot, artifact) {
				return fmt.Errorf("generated descriptor file escapes root: %s", relative)
			}
			info, err := os.Lstat(artifact)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("generated descriptor file is not regular: %s", artifact)
			}
			data, err := os.ReadFile(artifact)
			if err != nil {
				return err
			}
			artifactRel, _ := filepath.Rel(root, artifact)
			entries[filepath.ToSlash(artifactRel)] = data
		}
		return nil
	})
	return entries, err
}
