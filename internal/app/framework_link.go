package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

const (
	frameworkLinkKind   = "scenery.framework-link"
	frameworkLinkSchema = `{"identity":"artifact","app_root":"path","source_origin":"path"}`
)

// FrameworkLink binds an app root to a local scenery.sh source checkout without
// mutating the app's tracked go.mod. It lives under the ignored .scenery tree.
type FrameworkLink struct {
	machine.ArtifactIdentity
	AppRoot      string `json:"app_root"`
	SourceOrigin string `json:"source_origin"`
}

func FrameworkLinkPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "framework", "link.json")
}

func FrameworkModFilePath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "framework", "go.mod")
}

func FrameworkGoSumPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "framework", "go.sum")
}

// ReadFrameworkLink reads and validates the app-local framework link if present.
// Missing link files return (zero, false, nil); malformed or invalid links fail closed.
func ReadFrameworkLink(appRoot string) (FrameworkLink, bool, error) {
	canonical, err := canonicalExistingPath(appRoot)
	if err != nil {
		return FrameworkLink{}, false, err
	}
	path := FrameworkLinkPath(canonical)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return FrameworkLink{}, false, nil
	}
	if err != nil {
		return FrameworkLink{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return FrameworkLink{}, false, fmt.Errorf("framework link must be a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FrameworkLink{}, false, err
	}
	var link FrameworkLink
	if err := machine.DecodeArtifact(data, &link, &link.ArtifactIdentity, frameworkLinkKind, frameworkLinkSchema, "run scenery framework link <checkout> or scenery framework unlink"); err != nil {
		return FrameworkLink{}, false, err
	}
	if link.AppRoot != canonical {
		return FrameworkLink{}, false, fmt.Errorf("framework link does not belong to this app root; run scenery framework link <checkout> or scenery framework unlink")
	}
	if _, err := ValidateFrameworkLinkOrigin(link.SourceOrigin); err != nil {
		return FrameworkLink{}, false, fmt.Errorf("framework link target %s is invalid: %w; run scenery framework link <checkout> or scenery framework unlink", link.SourceOrigin, err)
	}
	return link, true, nil
}

// ValidateFrameworkLinkOrigin resolves origin to a canonical non-symlink
// directory containing the scenery.sh Go module.
func ValidateFrameworkLinkOrigin(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return "", fmt.Errorf("framework source checkout path must not be empty")
	}
	canonical, err := canonicalExistingPath(origin)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("framework source checkout is not a non-symlink directory: %s", canonical)
	}
	modBytes, err := os.ReadFile(filepath.Join(canonical, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read framework source go.mod: %w", err)
	}
	if modfile.ModulePath(modBytes) != "scenery.sh" {
		return "", fmt.Errorf("framework source is not the scenery.sh module: %s", canonical)
	}
	return canonical, nil
}

// WriteFrameworkLink records a validated local checkout link under .scenery/framework.
func WriteFrameworkLink(appRoot, sourceOrigin string) (FrameworkLink, error) {
	canonicalRoot, err := canonicalExistingPath(appRoot)
	if err != nil {
		return FrameworkLink{}, err
	}
	canonicalOrigin, err := ValidateFrameworkLinkOrigin(sourceOrigin)
	if err != nil {
		return FrameworkLink{}, err
	}
	dir := filepath.Dir(FrameworkLinkPath(canonicalRoot))
	if err := ensureNonSymlinkDirUnder(canonicalRoot, dir); err != nil {
		return FrameworkLink{}, err
	}
	link := FrameworkLink{
		ArtifactIdentity: machine.NewArtifactIdentity(frameworkLinkKind, frameworkLinkSchema),
		AppRoot:          canonicalRoot,
		SourceOrigin:     canonicalOrigin,
	}
	data, err := jsonMarshalIndented(link)
	if err != nil {
		return FrameworkLink{}, err
	}
	if err := atomicfile.Write(FrameworkLinkPath(canonicalRoot), data, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return FrameworkLink{}, err
	}
	return link, nil
}

// RemoveFrameworkLink removes the app-local link and its generated modfile overlay.
func RemoveFrameworkLink(appRoot string) (bool, error) {
	canonicalRoot, err := canonicalExistingPath(appRoot)
	if err != nil {
		return false, err
	}
	removed := false
	for _, path := range []string{FrameworkLinkPath(canonicalRoot), FrameworkModFilePath(canonicalRoot), FrameworkGoSumPath(canonicalRoot)} {
		if err := os.Remove(path); err == nil {
			if path == FrameworkLinkPath(canonicalRoot) {
				removed = true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
	}
	return removed, nil
}

// LinkedFrameworkReplaceRoot returns the scenery.sh module directory that Go
// builds and analysis should substitute when appRoot has an active local link.
// When running as an app-local prepared producer, it returns that producer's
// immutable content-addressed snapshot; otherwise it returns the linked origin.
func LinkedFrameworkReplaceRoot(appRoot string) (string, bool, error) {
	link, ok, err := ReadFrameworkLink(appRoot)
	if err != nil || !ok {
		return "", false, err
	}
	snapshotPrefix := filepath.Join(link.AppRoot, ".scenery", "framework", "source") + string(filepath.Separator)
	if repoRoot := RepoRoot(); repoRoot != "" {
		if canonicalRepo, evalErr := canonicalExistingPath(repoRoot); evalErr == nil && strings.HasPrefix(canonicalRepo, snapshotPrefix) {
			return canonicalRepo, true, nil
		}
	}
	return link.SourceOrigin, true, nil
}

// FormatLinkedGoMod copies goModData, rebases relative local replacements
// against moduleRoot, and replaces scenery.sh with frameworkRoot.
func FormatLinkedGoMod(goModData []byte, moduleRoot, frameworkRoot string) ([]byte, error) {
	file, err := modfile.Parse("go.mod", goModData, nil)
	if err != nil {
		return nil, err
	}
	hasRequire := false
	for _, req := range file.Require {
		if req.Mod.Path == "scenery.sh" {
			hasRequire = true
			break
		}
	}
	if !hasRequire {
		if err := file.AddRequire("scenery.sh", "v0.0.0"); err != nil {
			return nil, err
		}
	}
	for _, replacement := range append([]*modfile.Replace(nil), file.Replace...) {
		if replacement.Old.Path == "scenery.sh" {
			if err := file.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
				return nil, err
			}
			continue
		}
		if replacement.New.Version != "" || filepath.IsAbs(replacement.New.Path) {
			continue
		}
		rebased := filepath.Clean(filepath.Join(moduleRoot, replacement.New.Path))
		if err := file.AddReplace(replacement.Old.Path, replacement.Old.Version, rebased, ""); err != nil {
			return nil, err
		}
	}
	if err := file.AddReplace("scenery.sh", "", filepath.Clean(frameworkRoot), ""); err != nil {
		return nil, err
	}
	return file.Format()
}

// MergeGoSum combines two go.sum files into sorted unique lines.
func MergeGoSum(baseSum, extraSum []byte) []byte {
	lines := map[string]struct{}{}
	for _, data := range [][]byte{baseSum, extraSum} {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines[line] = struct{}{}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	merged := make([]string, 0, len(lines))
	for line := range lines {
		merged = append(merged, line)
	}
	sort.Strings(merged)
	return []byte(strings.Join(merged, "\n") + "\n")
}

// SyncLinkedGoModFile writes .scenery/framework/go.mod and go.sum from
// appRoot/go.mod plus frameworkRoot, so external go test/run invocations can
// use -modfile=.scenery/framework/go.mod without modifying appRoot/go.mod.
func SyncLinkedGoModFile(appRoot, frameworkRoot string) (string, error) {
	canonicalRoot, err := canonicalExistingPath(appRoot)
	if err != nil {
		return "", err
	}
	modPath := filepath.Join(canonicalRoot, "go.mod")
	modData, err := os.ReadFile(modPath)
	if err != nil {
		return "", err
	}
	formatted, err := FormatLinkedGoMod(modData, canonicalRoot, frameworkRoot)
	if err != nil {
		return "", err
	}
	outMod := FrameworkModFilePath(canonicalRoot)
	outSum := FrameworkGoSumPath(canonicalRoot)
	if err := ensureNonSymlinkDirUnder(canonicalRoot, filepath.Dir(outMod)); err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(outMod); err != nil || !bytes.Equal(existing, formatted) {
		if err := atomicfile.Write(outMod, formatted, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
			return "", err
		}
	}
	baseSum, err := os.ReadFile(filepath.Join(canonicalRoot, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	extraSum, err := os.ReadFile(filepath.Join(frameworkRoot, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if merged := MergeGoSum(baseSum, extraSum); len(merged) > 0 {
		if existing, err := os.ReadFile(outSum); err != nil || !bytes.Equal(existing, merged) {
			if err := atomicfile.Write(outSum, merged, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
				return "", err
			}
		}
	}
	return outMod, nil
}

func canonicalExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func ensureNonSymlinkDirUnder(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("framework state must remain beneath the app root")
	}
	current := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("framework state root must be a non-symlink directory: %s", current)
		}
	}
	return nil
}

func jsonMarshalIndented(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
