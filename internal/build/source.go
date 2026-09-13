package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

const buildTracePathLimit = 32

// workspaceMutation records bounded evidence about bytes actually changed in
// the private workspace. Counts remain exact when the diagnostic path samples
// reach their cap.
type workspaceMutation struct {
	filesWritten int
	filesRemoved int
	bytesWritten int64
	cacheHits    int
	cacheMisses  int
	writtenPaths []string
	removedPaths []string
}

func (m *workspaceMutation) wrote(rel string, bytes int) {
	if m == nil {
		return
	}
	m.filesWritten++
	m.bytesWritten += int64(bytes)
	m.cacheMisses++
	if len(m.writtenPaths) < buildTracePathLimit {
		m.writtenPaths = append(m.writtenPaths, filepath.ToSlash(rel))
	}
}

func (m *workspaceMutation) removed(rel string) {
	if m == nil {
		return
	}
	m.filesRemoved++
	m.cacheMisses++
	if len(m.removedPaths) < buildTracePathLimit {
		m.removedPaths = append(m.removedPaths, filepath.ToSlash(rel))
	}
}

func (m *workspaceMutation) reused() {
	if m != nil {
		m.cacheHits++
	}
}

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
		if !d.IsDir() && shouldSkipFile(rel) {
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
	return syncSourceFilesWithSnapshotObserved(root, appRoot, prevStamps, skip, snapshot, nil)
}

func syncSourceFilesWithSnapshotObserved(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}, snapshot *SourceSnapshot, mutation *workspaceMutation) ([]string, map[string]SourceStamp, error) {
	if snapshot == nil {
		return syncSourceFilesFromDiskObserved(root, appRoot, prevStamps, skip, mutation)
	}
	currentFiles, err := snapshotSourceFilesForRoot(appRoot, snapshot)
	if err != nil {
		return nil, nil, err
	}
	stamps := make(map[string]SourceStamp, len(currentFiles))
	for _, rel := range currentFiles {
		stamp := sourceStampFromSnapshot(snapshot.Files[rel])
		if _, ok := skip[rel]; ok {
			stamps[rel] = stamp
			continue
		}
		if prev, ok := prevStamps[rel]; ok && prev == stamp {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				mutation.reused()
				stamps[rel] = stamp
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		}
		data, err := sourceSnapshotFileData(appRoot, rel, snapshot.Files[rel])
		if err != nil {
			return nil, nil, err
		}
		if _, err := writeFileIfChangedObserved(root, rel, data, mutation); err != nil {
			return nil, nil, err
		}
		stamps[rel] = stamp
	}
	for rel := range prevStamps {
		if _, generated := skip[rel]; generated {
			continue
		}
		if _, ok := stamps[rel]; ok {
			continue
		}
		if _, err := removeFileIfExistsObserved(root, rel, mutation); err != nil {
			return nil, nil, err
		}
	}
	return sourceFilesFromStamps(stamps), stamps, nil
}

func syncSourceFilesFromDiskObserved(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}, mutation *workspaceMutation) ([]string, map[string]SourceStamp, error) {
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
		raw, err := os.ReadFile(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		sum := sha256.Sum256(raw)
		stamp.Hash = hex.EncodeToString(sum[:])
		if _, ok := skip[rel]; ok {
			stamps[rel] = stamp
			continue
		}
		if prev, ok := prevStamps[rel]; ok && prev == stamp {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				mutation.reused()
				stamps[rel] = stamp
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		}
		data := raw
		if rel == "go.mod" {
			data, err = patchGoModData(raw, filepath.Dir(src))
			if err != nil {
				return nil, nil, err
			}
		}
		if _, err := writeFileIfChangedObserved(root, rel, data, mutation); err != nil {
			return nil, nil, err
		}
		stamps[rel] = stamp
	}
	for rel := range prevStamps {
		if _, generated := skip[rel]; generated {
			continue
		}
		if _, ok := stamps[rel]; ok {
			continue
		}
		if _, err := removeFileIfExistsObserved(root, rel, mutation); err != nil {
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
		Hash:        file.Hash,
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
		_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o:%s", stamp.Size, stamp.ModTimeNano, stamp.Perm, stamp.Hash))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func listSourceFiles(appRoot string) ([]string, error) {
	generated, err := compiler.GeneratedPaths(appRoot)
	if err != nil {
		return nil, err
	}
	files := make(map[string]struct{})
	err = filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
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
		if generated[rel] {
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
	files, err := snapshotSourceFilesForRoot(appRoot, snapshot)
	if err != nil {
		return "", err
	}
	for _, rel := range files {
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

func snapshotSourceFilesForRoot(appRoot string, snapshot *SourceSnapshot) ([]string, error) {
	generated, err := compiler.GeneratedPaths(appRoot)
	if err != nil {
		return nil, err
	}
	files := snapshotSourceFiles(snapshot)
	authored := files[:0]
	for _, file := range files {
		if !generated[file] {
			authored = append(authored, file)
		}
	}
	return authored, nil
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
		return patchGoModData(data, filepath.Dir(path))
	}
	return data, nil
}

func sourceSnapshotFileData(appRoot, rel string, file SourceSnapshotFile) ([]byte, error) {
	if file.Data == nil {
		return nil, fmt.Errorf("captured source bytes are unavailable for %s", rel)
	}
	digest := sha256.Sum256(file.Data)
	if int64(len(file.Data)) != file.Size || hex.EncodeToString(digest[:]) != file.Hash {
		return nil, fmt.Errorf("captured source identity does not match bytes for %s", rel)
	}
	data := append([]byte(nil), file.Data...)
	if rel == "go.mod" {
		return patchGoModData(data, appRoot)
	}
	return data, nil
}

func writeFileIfChanged(root, rel string, data []byte) error {
	_, err := writeFileIfChangedObserved(root, rel, data, nil)
	return err
}

func writeFileIfChangedObserved(root, rel string, data []byte, mutation *workspaceMutation) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		mutation.reused()
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	mutation.wrote(rel, len(data))
	return true, nil
}

func removeFileIfExistsObserved(root, rel string, mutation *workspaceMutation) (bool, error) {
	err := os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	mutation.removed(rel)
	return true, nil
}

func patchGoModData(data []byte, moduleRoot string) ([]byte, error) {
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	// The authored module is the dependency selection authority. Moving it to
	// the generated workspace only requires rebasing local replacement paths;
	// it must never silently substitute the CLI's source checkout or version.
	for _, replacement := range file.Replace {
		if replacement.New.Version != "" || filepath.IsAbs(replacement.New.Path) {
			continue
		}
		path := filepath.Clean(filepath.Join(moduleRoot, replacement.New.Path))
		if err := file.AddReplace(replacement.Old.Path, replacement.Old.Version, path, ""); err != nil {
			return nil, err
		}
	}
	formatted, err := file.Format()
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

func seedWorkspaceSceneryGoSumObserved(workspaceDir string, mutation *workspaceMutation) error {
	root, local, err := localSceneryReplaceRoot(filepath.Join(workspaceDir, "go.mod"))
	if err != nil || !local {
		return err
	}
	return seedSceneryGoSumObserved(workspaceDir, root, mutation)
}

func seedSceneryGoSum(workspaceDir, repoRoot string) error {
	return seedSceneryGoSumObserved(workspaceDir, repoRoot, nil)
}

func seedSceneryGoSumObserved(workspaceDir, repoRoot string, mutation *workspaceMutation) error {
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
	_, err = writeFileIfChangedObserved(workspaceDir, "go.sum", []byte(strings.Join(merged, "\n")+"\n"), mutation)
	return err
}

func removeUnexpectedFilesFromLists(root string, sourceFiles, generatedFiles []string) error {
	return removeUnexpectedFilesFromListsObserved(root, sourceFiles, generatedFiles, nil)
}

func removeUnexpectedFilesFromListsObserved(root string, sourceFiles, generatedFiles []string, mutation *workspaceMutation) error {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := removeFileIfExistsObserved(root, filepath.ToSlash(rel), mutation); err != nil {
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

func pruneStaleWorkspaceBinaries(root string, keepPaths ...string) error {
	keep := make(map[string]struct{}, len(keepPaths))
	for _, path := range keepPaths {
		if path != "" && filepath.Clean(filepath.Dir(path)) == filepath.Clean(root) {
			keep[filepath.Base(path)] = struct{}{}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isFingerprintBinaryName(name) {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		path := filepath.Join(root, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale build binary %s: %w", path, err)
		}
	}
	return nil
}

func isFingerprintBinaryName(name string) bool {
	const prefix = "scenery-app-"
	const fingerprintLength = 16
	if len(name) != len(prefix)+fingerprintLength || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, char := range name[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
