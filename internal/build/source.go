package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/app"
	"scenery.sh/internal/generate"
)

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (shouldSkipDir(rel) || shouldSkipRuntimeArtifactDir(rel)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && (shouldSkipFile(rel) || generate.IsManagedEditorWorkFile(src, rel)) {
			return nil
		}
		if shouldSkipSymlink(path, d) {
			return nil
		}
		if !d.IsDir() && shouldSkipNonRegularFile(path, d) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// syncSourceFiles mirrors the app's source files into the workspace and
// returns the synced file list plus the stamp observed for every file. A file
// is skipped only when its current stat still matches the stamp recorded by a
// previous sync and the workspace copy exists; anything else is re-read and
// rewritten. The sync never trusts external change notifications, so changes
// missed by a file watcher (git pulls, edits while stopped) are still picked
// up here. Stamps are captured before reading the content: if a file changes
// mid-read, the recorded stamp is older than its stat and the next sync
// rewrites it. Files in skip are tracked and stamped but never written; their
// workspace content is owned by generated-file sync.
func syncSourceFiles(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}) ([]string, map[string]SourceStamp, error) {
	return syncSourceFilesWithSnapshot(root, appRoot, prevStamps, skip, nil)
}

func syncSourceFilesWithSnapshot(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}, snapshot *SourceSnapshot) ([]string, map[string]SourceStamp, error) {
	if snapshot == nil {
		return syncSourceFilesFromDisk(root, appRoot, prevStamps, skip)
	}
	currentFiles := snapshotSourceFilesForRoot(appRoot, snapshot)
	stamps := make(map[string]SourceStamp, len(currentFiles))
	for _, rel := range currentFiles {
		stamp := sourceStampFromSnapshot(snapshot.Files[rel])
		if _, ok := skip[rel]; ok {
			stamps[rel] = stamp
			continue
		}
		if prev, ok := prevStamps[rel]; ok && prev == stamp {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				stamps[rel] = stamp
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		}
		data, err := sourceFileData(filepath.Join(appRoot, filepath.FromSlash(rel)), rel)
		if err != nil {
			return nil, nil, err
		}
		if err := writeFileIfChanged(root, rel, data); err != nil {
			return nil, nil, err
		}
		stamps[rel] = stamp
	}
	for rel := range prevStamps {
		if _, ok := stamps[rel]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	return sourceFilesFromStamps(stamps), stamps, nil
}

func syncSourceFilesFromDisk(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}) ([]string, map[string]SourceStamp, error) {
	currentFiles, err := listSourceFiles(appRoot)
	if err != nil {
		return nil, nil, err
	}
	stamps := make(map[string]SourceStamp, len(currentFiles))
	for _, rel := range currentFiles {
		src := filepath.Join(appRoot, filepath.FromSlash(rel))
		info, err := os.Stat(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		stamp := sourceStampFromInfo(info)
		if _, ok := skip[rel]; ok {
			stamps[rel] = stamp
			continue
		}
		if prev, ok := prevStamps[rel]; ok && prev == stamp {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				stamps[rel] = stamp
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		}
		data, err := sourceFileData(src, rel)
		if err != nil {
			return nil, nil, err
		}
		if err := writeFileIfChanged(root, rel, data); err != nil {
			return nil, nil, err
		}
		stamps[rel] = stamp
	}
	for rel := range prevStamps {
		if _, ok := stamps[rel]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	return sourceFilesFromStamps(stamps), stamps, nil
}

func sourceStampFromSnapshot(file SourceSnapshotFile) SourceStamp {
	return SourceStamp{
		Size:        file.Size,
		ModTimeNano: file.ModTimeNano,
		Perm:        file.Perm,
	}
}

func sourceStampFromInfo(info os.FileInfo) SourceStamp {
	return SourceStamp{
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UnixNano(),
		Perm:        uint32(info.Mode().Perm()),
	}
}

func sourceFilesFromStamps(stamps map[string]SourceStamp) []string {
	files := make([]string, 0, len(stamps))
	for rel := range stamps {
		files = append(files, filepath.ToSlash(rel))
	}
	sort.Strings(files)
	return files
}

// sourceStampsFingerprint hashes the stamps recorded while syncing, not a
// fresh stat pass over the app root. The distinction matters: a fresh stat
// pass can pick up changes made after the sync read its data, which would
// bless a workspace that does not actually contain them.
func sourceStampsFingerprint(stamps map[string]SourceStamp) string {
	h := sha256.New()
	for _, rel := range sourceFilesFromStamps(stamps) {
		stamp := stamps[rel]
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o", stamp.Size, stamp.ModTimeNano, stamp.Perm))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func listSourceFiles(appRoot string) ([]string, error) {
	files := make(map[string]struct{})
	err := filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (shouldSkipDir(rel) || shouldSkipRuntimeArtifactDir(rel)) {
			return filepath.SkipDir
		}
		if d.IsDir() || !isGoWorkspaceSourceFile(rel) || shouldSkipFile(rel) || shouldSkipSymlink(path, d) || shouldSkipNonRegularFile(path, d) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if generate.IsManagedEditorWorkFile(appRoot, rel) {
			return nil
		}
		files[rel] = struct{}{}
		if filepath.Ext(rel) == ".go" {
			if err := addAppEmbeddedFiles(appRoot, rel, files); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedKeys(files), nil
}

func isGoWorkspaceSourceFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if app.IsConfigFilename(base) || pathHasSegment(rel, "testdata") {
		return true
	}
	switch base {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".m", ".mm", ".s", ".S", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

func pathHasSegment(path, want string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == want {
			return true
		}
	}
	return false
}

func addAppEmbeddedFiles(appRoot, goRel string, files map[string]struct{}) error {
	data, err := os.ReadFile(filepath.Join(appRoot, filepath.FromSlash(goRel)))
	if err != nil {
		return err
	}
	patterns := parseGeneratorGoEmbedPatterns(string(data))
	if len(patterns) == 0 {
		return nil
	}
	pkgDir := filepath.Dir(goRel)
	for _, pattern := range patterns {
		if err := addGeneratorEmbeddedPatternFiles(appRoot, pkgDir, pattern, files); err != nil {
			return err
		}
	}
	return nil
}

func currentAppSourceFingerprintWithSnapshot(appRoot string, snapshot *SourceSnapshot) (string, error) {
	if snapshot == nil {
		return currentAppSourceFingerprintFromDisk(appRoot)
	}
	h := sha256.New()
	configPath, err := app.ResolveConfigPath(appRoot)
	if err != nil {
		return "", err
	}
	if rel, ok, err := snapshotRel(appRoot, configPath); err != nil {
		return "", err
	} else if ok {
		if file, exists := snapshot.Files[rel]; exists {
			_, _ = h.Write([]byte(rel))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(file.Hash))
			_, _ = h.Write([]byte{0})
		}
	}
	for _, rel := range snapshotSourceFilesForRoot(appRoot, snapshot) {
		file := snapshot.Files[rel]
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(file.Hash))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func currentAppSourceFingerprintFromDisk(appRoot string) (string, error) {
	files, err := listSourceFiles(appRoot)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	configPath, err := app.ResolveConfigPath(appRoot)
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(configPath); err == nil {
		rel, relErr := filepath.Rel(appRoot, configPath)
		if relErr != nil {
			rel = filepath.Base(configPath)
		}
		sum := sha256.Sum256(data)
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(hex.EncodeToString(sum[:])))
		_, _ = h.Write([]byte{0})
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, rel := range files {
		data, err := sourceFileData(filepath.Join(appRoot, filepath.FromSlash(rel)), rel)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(hex.EncodeToString(sum[:])))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func snapshotRel(root, path string) (string, bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, nil
	}
	return filepath.ToSlash(rel), true, nil
}

func snapshotSourceFiles(snapshot *SourceSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	files := make([]string, 0, len(snapshot.Files))
	for rel, file := range snapshot.Files {
		rel = filepath.ToSlash(rel)
		if file.Embedded || isGoWorkspaceSourceFile(rel) {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files
}

func snapshotSourceFilesForRoot(appRoot string, snapshot *SourceSnapshot) []string {
	files := snapshotSourceFiles(snapshot)
	if !generate.InspectEditorWorkspace(appRoot).Managed {
		return files
	}
	return slices.DeleteFunc(files, func(relative string) bool {
		return generate.IsManagedEditorWorkFile(appRoot, relative)
	})
}

func shouldSkipDir(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "scenery_internal_main", "__MACOSX", "coverage":
		return true
	default:
		return false
	}
}

func shouldSkipRuntimeArtifactDir(rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	switch {
	case rel == "var/browser", strings.HasPrefix(rel, "var/browser/"):
		return true
	case rel == "var/chrome", strings.HasPrefix(rel, "var/chrome/"):
		return true
	case rel == "var/playwright", strings.HasPrefix(rel, "var/playwright/"):
		return true
	default:
		return false
	}
}

func shouldSkipFile(rel string) bool {
	base := filepath.Base(rel)
	if base == ".DS_Store" {
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	return false
}

func shouldSkipNonRegularFile(path string, d os.DirEntry) bool {
	if d == nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
		return false
	}
	info, err := d.Info()
	if err != nil {
		return true
	}
	return !info.Mode().IsRegular()
}

func shouldSkipSymlink(path string, d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && info.IsDir()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func sourceFileData(path, rel string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch rel {
	case "go.mod":
		return patchGoModData(data, app.RepoRoot())
	}
	return data, nil
}

func writeFileIfChanged(root, rel string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil && string(current) == string(data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func patchGoModData(data []byte, repoRoot string) ([]byte, error) {
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	if err := file.AddRequire("scenery.sh", "v0.0.0"); err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, err
	}
	_ = file.DropReplace("scenery.sh", "")
	if err := file.AddReplace("scenery.sh", "", repoRoot, ""); err != nil {
		return nil, err
	}
	formatted, err := file.Format()
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

func seedSceneryGoSum(workspaceDir, repoRoot string) error {
	repoSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	workspaceSumPath := filepath.Join(workspaceDir, "go.sum")
	workspaceSum, err := os.ReadFile(workspaceSumPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := map[string]struct{}{}
	for _, data := range [][]byte{workspaceSum, repoSum} {
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
	return writeFileIfChanged(workspaceDir, "go.sum", []byte(strings.Join(merged, "\n")+"\n"))
}

func removeUnexpectedFilesFromLists(root string, sourceFiles, generatedFiles []string) error {
	keepFiles := make(map[string]struct{}, len(sourceFiles)+len(generatedFiles)+2)
	keepDirs := map[string]struct{}{
		".": {},
	}
	for _, rel := range append(append([]string(nil), sourceFiles...), generatedFiles...) {
		rel = filepath.ToSlash(rel)
		keepFiles[rel] = struct{}{}
		dir := filepath.Dir(rel)
		for dir != "." && dir != "/" {
			keepDirs[filepath.ToSlash(dir)] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}
	keepFiles["scenery-app"] = struct{}{}
	keepFiles[".scenery-workspace.lock"] = struct{}{}
	keepFiles[buildStateFile] = struct{}{}
	keepFiles["go.sum"] = struct{}{}

	var files []string
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if _, ok := keepFiles[rel]; ok || strings.HasPrefix(rel, "scenery-app-") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, path := range dirs {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := keepDirs[filepath.ToSlash(rel)]; ok {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fs.ErrExist) {
			if pathErr, ok := errors.AsType[*fs.PathError](err); ok && errors.Is(pathErr.Err, fs.ErrExist) {
				continue
			}
			if strings.Contains(err.Error(), "directory not empty") {
				continue
			}
			return err
		}
	}
	return nil
}
