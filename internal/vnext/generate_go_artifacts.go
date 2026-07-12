package vnext

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func optionalJSONSuffix(value any) string {
	if expr, ok := value.(map[string]any); ok {
		if raw, ok := expr["$expression"].(string); ok && strings.HasPrefix(strings.TrimSpace(raw), "optional(") {
			return ",omitempty"
		}
	}
	return ""
}
func wireName(field map[string]any, fallback string) string {
	for _, key := range []string{"wire_name", "wire_value", "wire_tag"} {
		if value, ok := field[key].(string); ok {
			return value
		}
	}
	return fallback
}
func goName(value string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' }) {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func atomicWriteSet(root string, files []generatedFile) error {
	edits := make([]SourceEdit, 0, len(files))
	for _, file := range files {
		if err := rejectGeneratedPathSymlinks(root, file.Path); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, file.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("generated artifact escapes app root: %s", file.Path)
		}
		info, statErr := os.Lstat(file.Path)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return fmt.Errorf("generated artifact is not a regular file: %s", file.Path)
		}
		before := []byte(nil)
		if exists {
			before, err = os.ReadFile(file.Path)
			if err != nil {
				return err
			}
		}
		if file.Remove && !exists {
			continue
		}
		edits = append(edits, SourceEdit{
			Path: filepath.ToSlash(relative), BeforeDigest: byteDigest(before), BeforeExists: exists,
			AfterExists: !file.Remove, After: append([]byte(nil), file.Bytes...), Mode: 0o644,
		})
	}
	if len(edits) == 0 {
		return nil
	}
	rollback, finalize, err := commitPlannedEdits(root, edits, "")
	if err != nil {
		return err
	}
	if rollback == nil || finalize == nil {
		return fmt.Errorf("generated transaction did not initialize")
	}
	finalize()
	return nil
}

func rejectGeneratedPathSymlinks(root, target string) error {
	root, rootErr := filepath.Abs(root)
	target, targetErr := filepath.Abs(target)
	if rootErr != nil || targetErr != nil || !pathWithin(root, target) {
		return fmt.Errorf("generated artifact escapes app root: %s", target)
	}
	relative, _ := filepath.Rel(root, target)
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated artifact path contains symlink: %s", filepath.ToSlash(relative))
		}
	}
	return nil
}

func artifactDigest(root string, files []generatedFile) string {
	sorted := append([]generatedFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		a, _ := filepath.Rel(root, sorted[i].Path)
		b, _ := filepath.Rel(root, sorted[j].Path)
		return filepath.ToSlash(a) < filepath.ToSlash(b)
	})
	h := sha256.New()
	var length [8]byte
	for _, file := range sorted {
		rel, _ := filepath.Rel(root, file.Path)
		path := []byte(filepath.ToSlash(rel))
		binary.BigEndian.PutUint64(length[:], uint64(len(path)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(path)
		binary.BigEndian.PutUint64(length[:], uint64(len(file.Bytes)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(file.Bytes)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func generatedFilePaths(root string, files []generatedFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		rel, _ := filepath.Rel(root, file.Path)
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths
}
