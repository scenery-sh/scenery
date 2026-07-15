package generate

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/atomicfile"
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
	return atomicfile.Write(path, data, 0o644, atomicfile.Options{})
}

func atomicWriteSet(root string, files []generatedFile) error {
	type stagedFile struct {
		path, temporary, backup string
		remove, existed         bool
	}
	staged := make([]stagedFile, 0, len(files))
	cleanup := func() {
		for _, file := range staged {
			if file.temporary != "" {
				_ = os.Remove(file.temporary)
			}
		}
	}
	for _, file := range files {
		if err := rejectGeneratedPathSymlinks(root, file.Path); err != nil {
			cleanup()
			return err
		}
		relative, err := filepath.Rel(root, file.Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			cleanup()
			return fmt.Errorf("generated artifact escapes app root: %s", file.Path)
		}
		info, statErr := os.Lstat(file.Path)
		exists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			cleanup()
			return statErr
		}
		if exists && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			cleanup()
			return fmt.Errorf("generated artifact is not a regular file: %s", file.Path)
		}
		if file.Remove && !exists {
			continue
		}
		entry := stagedFile{path: file.Path, remove: file.Remove, existed: exists}
		if !file.Remove {
			if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
				cleanup()
				return err
			}
			temporary, err := os.CreateTemp(filepath.Dir(file.Path), ".scenery-generate-*")
			if err != nil {
				cleanup()
				return err
			}
			entry.temporary = temporary.Name()
			if _, err := temporary.Write(file.Bytes); err != nil {
				_ = temporary.Close()
				staged = append(staged, entry)
				cleanup()
				return err
			}
			if err := temporary.Sync(); err != nil {
				_ = temporary.Close()
				staged = append(staged, entry)
				cleanup()
				return err
			}
			if err := temporary.Close(); err != nil {
				staged = append(staged, entry)
				cleanup()
				return err
			}
			if err := os.Chmod(entry.temporary, 0o644); err != nil {
				staged = append(staged, entry)
				cleanup()
				return err
			}
		}
		staged = append(staged, entry)
	}
	if len(staged) == 0 {
		return nil
	}
	rollback := func(last int) {
		for index := last; index >= 0; index-- {
			file := staged[index]
			_ = os.Remove(file.path)
			if file.backup != "" {
				_ = os.Rename(file.backup, file.path)
			}
		}
		cleanup()
	}
	for index := range staged {
		file := &staged[index]
		if file.existed {
			backup, err := os.CreateTemp(filepath.Dir(file.path), ".scenery-backup-*")
			if err != nil {
				rollback(index - 1)
				return err
			}
			file.backup = backup.Name()
			_ = backup.Close()
			_ = os.Remove(file.backup)
			if err := os.Rename(file.path, file.backup); err != nil {
				rollback(index - 1)
				return err
			}
		}
		if !file.remove {
			if err := os.Rename(file.temporary, file.path); err != nil {
				rollback(index)
				return err
			}
			file.temporary = ""
		}
	}
	for _, file := range staged {
		if file.backup != "" {
			_ = os.Remove(file.backup)
		}
	}
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
