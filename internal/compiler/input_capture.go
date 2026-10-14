package compiler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// InputSelection extends compiler-owned membership resolution for a tool's
// declared files/directories/globs. Suffix applies to directory/glob selection;
// an explicitly named file always contributes its exact bytes.
type InputSelection struct{ Path, Suffix string }

type InputCapture struct {
	Files       map[string][]byte
	Directories []string
	Digest      string
}

// CaptureInputPaths uses the existing revision walker, path policy and exact
// byte hashing. Exclusions name proven producer outputs, never whole roots.
func CaptureInputPaths(root string, selections []InputSelection, excluded map[string]bool) (*InputCapture, error) {
	capture := &InputCapture{Files: map[string][]byte{}}
	entries := map[string][]byte{}
	directories := map[string]bool{}
	for _, selection := range selections {
		clean := filepath.ToSlash(filepath.Clean(selection.Path))
		if selection.Path == "" || filepath.IsAbs(selection.Path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || forbiddenWorkspacePath(clean) {
			return nil, fmt.Errorf("invalid_request: unsafe declared input %s", selection.Path)
		}
		if err := validateWorkspaceGlobs([]string{clean}); err != nil {
			return nil, err
		}
		path := filepath.Join(root, filepath.FromSlash(clean))
		info, statErr := os.Lstat(path)
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, statErr
		}
		entries["\x00selection:"+clean+":"+selection.Suffix] = []byte("absent")
		if statErr == nil && !info.IsDir() {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("failed_precondition: declared input is not regular: %s", clean)
			}
			if err := rejectPathSymlinks(root, path); err != nil {
				return nil, err
			}
			if excluded[clean] {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			capture.Files[clean] = data
			entries[clean] = data
			entries["\x00selection:"+clean+":"+selection.Suffix] = []byte("file")
			continue
		}
		matcher := newGlobMatcher([]string{clean})
		walkRoot := root
		if statErr == nil {
			if err := rejectPathSymlinks(root, path); err != nil {
				return nil, err
			}
			walkRoot = path
			matcher = newGlobMatcher([]string{clean + "/**"})
			entries["\x00selection:"+clean+":"+selection.Suffix] = []byte("directory")
		}
		if err := walkRevisionTree(walkRoot, os.ReadDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if entry.IsDir() {
				if relative != "." && forbiddenWorkspacePath(relative) {
					return filepath.SkipDir
				}
				if matcher.matches(relative) || path == walkRoot && walkRoot != root {
					directories[relative] = true
				}
				return nil
			}
			if !matcher.matches(relative) || excluded[relative] || selection.Suffix != "" && !strings.HasSuffix(relative, selection.Suffix) {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("failed_precondition: declared input is not regular: %s", relative)
			}
			if err := rejectPathSymlinks(root, path); err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			capture.Files[relative] = data
			entries[relative] = data
			return nil
		}); err != nil {
			return nil, err
		}
	}
	for directory := range directories {
		capture.Directories = append(capture.Directories, directory)
	}
	slices.Sort(capture.Directories)
	capture.Digest = workspaceRevisionForEntries(entries)
	return capture, nil
}
