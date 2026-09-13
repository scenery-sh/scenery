package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

const (
	ownedGoModuleSourceVersion = "v1"
	ownedGoModuleSourceLimit   = 64
	ownedGoModuleSourceBytes   = int64(4 << 30)
)

// OwnedGoModuleSource binds one authored local replacement to the immutable
// application-owned generation consumed by Go discovery and compilation.
type OwnedGoModuleSource struct {
	ModulePath     string `json:"module_path"`
	OriginRoot     string `json:"origin_root"`
	GenerationRoot string `json:"generation_root"`
	Digest         string `json:"digest"`
}

type ownedGoModuleEntry struct {
	Path       string
	Kind       string
	Executable bool
	Size       int64
	Digest     string
}

type ownedGoModuleManifest struct {
	Entries []ownedGoModuleEntry
	Digest  string
}

func cloneOwnedGoModuleSources(sources []OwnedGoModuleSource) []OwnedGoModuleSource {
	return slices.Clone(sources)
}

func bindOwnedGoModuleSources(ctx context.Context, appRoot, workspace string, snapshot *SourceSnapshot, mutation *workspaceMutation) (sources []OwnedGoModuleSource, resultErr error) {
	started := time.Now()
	var cacheHits, cacheMisses int
	defer func() {
		reason := "no_local_replacements"
		if len(sources) > 0 {
			reason = "owned_content_generations"
		}
		RecordStep(ctx, Step{
			Name: "source.local_modules", StartedAt: started, Duration: time.Since(started),
			Cache: "content_addressed", Reason: reason, OK: resultErr == nil,
			Actions: len(sources), CacheHits: cacheHits, CacheMisses: cacheMisses,
		})
	}()
	data, err := capturedRootGoMod(appRoot, snapshot)
	if err != nil {
		return nil, err
	}
	authored, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	workspaceData, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		return nil, err
	}
	file, err := modfile.Parse("go.mod", workspaceData, nil)
	if err != nil {
		return nil, err
	}
	// Tidy owns dependency requirements in the private workspace; the captured
	// authored module owns replacements. Reapply exactly that replacement set
	// without resetting tidy's otherwise-current module bytes on every edit.
	for _, replacement := range slices.Clone(file.Replace) {
		if err := file.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
			return nil, err
		}
	}
	byOrigin := map[string]OwnedGoModuleSource{}
	for _, replacement := range authored.Replace {
		newPath, newVersion := replacement.New.Path, replacement.New.Version
		if replacement.New.Version != "" || replacement.New.Path == "" {
			// Versioned replacements have no local source root to capture.
		} else if replacement.Old.Path == "scenery.sh" {
			// Framework selection already owns and verifies this exact authored
			// source generation. Preserve its spelling as well as its ownership;
			// canonicalizing /var and /private aliases would cause a needless
			// workspace go.mod rewrite.
		} else {
			origin := replacement.New.Path
			if !filepath.IsAbs(origin) {
				origin = filepath.Join(appRoot, origin)
			}
			origin, err = canonicalOwnedGoModuleRoot(origin)
			if err != nil {
				return nil, fmt.Errorf("resolve local replacement %s: %w", replacement.Old.Path, err)
			}
			owned, ok := byOrigin[origin]
			if !ok {
				var reused bool
				owned, reused, err = materializeOwnedGoModuleSource(ctx, appRoot, replacement.Old.Path, origin)
				if err != nil {
					return nil, err
				}
				if reused {
					cacheHits++
				} else {
					cacheMisses++
				}
				byOrigin[origin] = owned
			}
			owned.ModulePath = replacement.Old.Path
			sources = append(sources, owned)
			newPath = owned.GenerationRoot
		}
		if err := file.AddReplace(replacement.Old.Path, replacement.Old.Version, newPath, newVersion); err != nil {
			return nil, err
		}
	}
	formatted, err := file.Format()
	if err != nil {
		return nil, err
	}
	if _, err := writeFileIfChangedObserved(workspace, "go.mod", formatted, mutation); err != nil {
		return nil, err
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ModulePath == sources[j].ModulePath {
			return sources[i].OriginRoot < sources[j].OriginRoot
		}
		return sources[i].ModulePath < sources[j].ModulePath
	})
	protected := make(map[string]bool, len(sources))
	for _, source := range sources {
		protected[source.GenerationRoot] = true
	}
	if state, stateErr := loadBuildState(workspace); stateErr != nil {
		return nil, stateErr
	} else {
		for _, source := range state.OwnedGoModuleSources {
			protected[source.GenerationRoot] = true
		}
	}
	if cacheMisses > 0 {
		if err := pruneOwnedGoModuleSources(appRoot, protected); err != nil {
			return nil, err
		}
	}
	return sources, nil
}

func capturedRootGoMod(appRoot string, snapshot *SourceSnapshot) ([]byte, error) {
	if snapshot != nil {
		file, ok := snapshot.Files["go.mod"]
		if !ok {
			return nil, fmt.Errorf("captured source omits root go.mod")
		}
		return sourceSnapshotFileData(appRoot, "go.mod", file)
	}
	return sourceFileData(filepath.Join(appRoot, "go.mod"), "go.mod")
}

func canonicalOwnedGoModuleRoot(root string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("module source root is not a non-symlink directory: %s", canonical)
	}
	return canonical, nil
}

func ownedGoModuleRoot(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "build", "owned-go-modules", ownedGoModuleSourceVersion)
}

func materializeOwnedGoModuleSource(ctx context.Context, appRoot, modulePath, origin string) (OwnedGoModuleSource, bool, error) {
	manifest, err := ownedGoModuleSourceManifest(ctx, origin)
	if err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	root := ownedGoModuleRoot(appRoot)
	if err := ensureFrameworkStateRoot(appRoot, root); err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	destination := filepath.Join(root, strings.TrimPrefix(manifest.Digest, "sha256:"))
	if info, statErr := os.Lstat(destination); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return OwnedGoModuleSource{}, false, fmt.Errorf("owned Go module generation is not a non-symlink directory: %s", destination)
		}
		retained, verifyErr := ownedGoModuleSourceManifest(ctx, destination)
		if verifyErr == nil && retained.Digest == manifest.Digest {
			return OwnedGoModuleSource{ModulePath: modulePath, OriginRoot: origin, GenerationRoot: destination, Digest: manifest.Digest}, true, nil
		}
		quarantine := destination + fmt.Sprintf(".corrupt-%d", time.Now().UnixNano())
		if err := os.Rename(destination, quarantine); err != nil {
			return OwnedGoModuleSource{}, false, fmt.Errorf("quarantine corrupt owned Go module generation: %w", err)
		}
		defer func() { _ = removeOwnedGoModuleTree(quarantine) }()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return OwnedGoModuleSource{}, false, statErr
	}
	staging, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyOwnedGoModuleManifest(ctx, origin, staging, manifest); err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	staged, err := ownedGoModuleSourceManifest(ctx, staging)
	if err != nil || staged.Digest != manifest.Digest || !slices.Equal(staged.Entries, manifest.Entries) {
		return OwnedGoModuleSource{}, false, fmt.Errorf("owned Go module generation differs from captured source: %v", err)
	}
	current, err := ownedGoModuleSourceManifest(ctx, origin)
	if err != nil || current.Digest != manifest.Digest || !slices.Equal(current.Entries, manifest.Entries) {
		return OwnedGoModuleSource{}, false, fmt.Errorf("local module source changed during capture; retry from stable source: %v", err)
	}
	if err := makeOwnedGoModuleReadOnly(staging); err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	if err := os.Rename(staging, destination); err != nil {
		return OwnedGoModuleSource{}, false, err
	}
	return OwnedGoModuleSource{ModulePath: modulePath, OriginRoot: origin, GenerationRoot: destination, Digest: manifest.Digest}, false, nil
}

func ownedGoModuleSourceManifest(ctx context.Context, root string) (ownedGoModuleManifest, error) {
	var manifest ownedGoModuleManifest
	root, err := canonicalOwnedGoModuleRoot(root)
	if err != nil {
		return manifest, err
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
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
		if shouldSkipOwnedGoModulePath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("local module source contains a symlink: %s", rel)
		}
		if entry.IsDir() {
			manifest.Entries = append(manifest.Entries, ownedGoModuleEntry{Path: rel, Kind: "directory"})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("local module source contains a non-regular file: %s", rel)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		read, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		manifest.Entries = append(manifest.Entries, ownedGoModuleEntry{
			Path: rel, Kind: "file", Executable: info.Mode().Perm()&0o111 != 0, Size: read,
			Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		})
		if read > ownedGoModuleSourceBytes {
			return fmt.Errorf("local module source file exceeds the owned generation limit: %s", rel)
		}
		return nil
	})
	if err != nil {
		return manifest, err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	var total int64
	for _, entry := range manifest.Entries {
		if entry.Size > ownedGoModuleSourceBytes-total {
			return manifest, fmt.Errorf("local module source exceeds the %d-byte owned generation limit", ownedGoModuleSourceBytes)
		}
		total += entry.Size
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "scenery.owned-go-module-source."+ownedGoModuleSourceVersion+"\x00")
	for _, entry := range manifest.Entries {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%t\x00%d\x00%s\x00", entry.Path, entry.Kind, entry.Executable, entry.Size, entry.Digest)
	}
	manifest.Digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return manifest, nil
}

func shouldSkipOwnedGoModulePath(rel string) bool {
	switch filepath.Base(rel) {
	case ".git", ".hg", ".svn", ".bzr", ".scenery":
		// Version-control metadata is disabled as a build input and Scenery's
		// private state is never application source. Everything else, including
		// dotfiles, native headers and embed assets, belongs to the generation.
		return true
	default:
		return false
	}
}

func copyOwnedGoModuleManifest(ctx context.Context, origin, destination string, manifest ownedGoModuleManifest) error {
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		if entry.Kind == "directory" {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(origin, filepath.FromSlash(entry.Path)))
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if int64(len(data)) != entry.Size || "sha256:"+hex.EncodeToString(hash[:]) != entry.Digest {
			return fmt.Errorf("local module source changed while copying %s", entry.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if entry.Executable {
			mode |= 0o111
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
	}
	return nil
}

func makeOwnedGoModuleReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// Keep directories owner-writable so the private retention owner can
			// reclaim a generation without first mutating every ancestor. Regular
			// files remain read-only and every reuse/activation rehashes content.
			return os.Chmod(path, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&0o111|0o444)
	})
}

func removeOwnedGoModuleTree(root string) error {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() {
			_ = os.Chmod(path, 0o755)
		}
		return nil
	})
	return os.RemoveAll(root)
}

// VerifyOwnedGoModuleSourcesContext proves both the retained generation and
// current authored origin by bytes and membership. Metadata is not equivalence.
func VerifyOwnedGoModuleSourcesContext(ctx context.Context, sources []OwnedGoModuleSource) error {
	for _, source := range sources {
		generation, err := ownedGoModuleSourceManifest(ctx, source.GenerationRoot)
		if err != nil || generation.Digest != source.Digest {
			return fmt.Errorf("owned local module generation changed for %s: %v", source.ModulePath, err)
		}
		origin, err := ownedGoModuleSourceManifest(ctx, source.OriginRoot)
		if err != nil || origin.Digest != source.Digest {
			return fmt.Errorf("local module source changed after capture for %s; discard this candidate: %v", source.ModulePath, err)
		}
	}
	return ctx.Err()
}

func pruneOwnedGoModuleSources(appRoot string, protected map[string]bool) error {
	root := ownedGoModuleRoot(appRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		path string
		at   time.Time
		size int64
	}
	var candidates []candidate
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 64 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var size int64
		if err := filepath.WalkDir(path, func(_ string, child os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !child.IsDir() {
				info, err := child.Info()
				if err != nil {
					return err
				}
				size += info.Size()
			}
			return nil
		}); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{path: path, at: info.ModTime(), size: size})
		total += size
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })
	for len(candidates) > ownedGoModuleSourceLimit || total > ownedGoModuleSourceBytes {
		index := -1
		for i, candidate := range candidates {
			if !protected[candidate.path] {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		candidate := candidates[index]
		if err := removeOwnedGoModuleTree(candidate.path); err != nil {
			return err
		}
		total -= candidate.size
		candidates = append(candidates[:index], candidates[index+1:]...)
	}
	return nil
}
