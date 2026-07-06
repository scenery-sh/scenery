package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

const frameworkFingerprintCacheSchema = "scenery.framework-fingerprint.v1"

type frameworkFingerprintCache struct {
	SchemaVersion       string                          `json:"schema_version"`
	RepoRoot            string                          `json:"repo_root"`
	MetadataFingerprint string                          `json:"metadata_fingerprint"`
	Fingerprint         string                          `json:"fingerprint"`
	GoFiles             map[string]frameworkGoFileCache `json:"go_files,omitempty"`
}

type frameworkGoFileCache struct {
	Stamp         SourceStamp `json:"stamp"`
	EmbedPatterns []string    `json:"embed_patterns,omitempty"`
}

var cachedFrameworkFingerprintFunc = cachedFrameworkFingerprint

func currentFrameworkFingerprintFromWorkspace(workspaceDir string) (string, bool, error) {
	repoRoot, ok, err := localSceneryReplaceRoot(filepath.Join(workspaceDir, "go.mod"))
	if err != nil || !ok {
		return "", false, err
	}
	fingerprint, err := cachedFrameworkFingerprintFunc(repoRoot)
	if err != nil {
		return "", true, err
	}
	return fingerprint, true, nil
}

func localSceneryReplaceRoot(goModPath string) (string, bool, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	file, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", false, err
	}
	for _, replace := range file.Replace {
		if replace.Old.Path != "scenery.sh" || replace.New.Version != "" || replace.New.Path == "" {
			continue
		}
		path := replace.New.Path
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, ".") {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(goModPath), path)
		}
		return filepath.Clean(path), true, nil
	}
	return "", false, nil
}

func cachedFrameworkFingerprint(repoRoot string) (string, error) {
	cachePath, err := frameworkFingerprintCachePath(repoRoot)
	if err != nil {
		return "", err
	}
	cached, _, err := loadFrameworkFingerprintCache(cachePath)
	if err != nil {
		return "", err
	}
	files, goFiles, err := frameworkFingerprintFiles(repoRoot, cached.GoFiles)
	if err != nil {
		return "", err
	}
	metadataFingerprint, err := frameworkMetadataFingerprint(repoRoot, files)
	if err != nil {
		return "", err
	}
	if cached.SchemaVersion == frameworkFingerprintCacheSchema &&
		cached.RepoRoot == repoRoot &&
		cached.MetadataFingerprint == metadataFingerprint &&
		cached.Fingerprint != "" {
		return cached.Fingerprint, nil
	}
	fingerprint, err := computeFrameworkFingerprint(repoRoot, files)
	if err != nil {
		return "", err
	}
	if err := saveFrameworkFingerprintCache(cachePath, frameworkFingerprintCache{
		SchemaVersion:       frameworkFingerprintCacheSchema,
		RepoRoot:            repoRoot,
		MetadataFingerprint: metadataFingerprint,
		Fingerprint:         fingerprint,
		GoFiles:             goFiles,
	}); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func frameworkFingerprintCachePath(repoRoot string) (string, error) {
	cacheRoot, err := sceneryCacheRoot()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	return filepath.Join(cacheRoot, "build", "framework-fingerprint-"+hex.EncodeToString(sum[:8])+".json"), nil
}

func loadFrameworkFingerprintCache(path string) (frameworkFingerprintCache, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return frameworkFingerprintCache{}, false, nil
		}
		return frameworkFingerprintCache{}, false, err
	}
	var cached frameworkFingerprintCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return frameworkFingerprintCache{}, false, err
	}
	return cached, true, nil
}

func saveFrameworkFingerprintCache(path string, cached frameworkFingerprintCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func frameworkFingerprintFiles(repoRoot string, cachedGoFiles map[string]frameworkGoFileCache) ([]string, map[string]frameworkGoFileCache, error) {
	files := map[string]struct{}{}
	nextGoFiles := map[string]frameworkGoFileCache{}
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !frameworkSourceInputFile(rel) {
			return nil
		}
		files[rel] = struct{}{}
		if filepath.Ext(rel) != ".go" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		stamp := sourceStampFromInfo(info)
		entry, ok := cachedGoFiles[rel]
		if !ok || entry.Stamp != stamp {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry = frameworkGoFileCache{
				Stamp:         stamp,
				EmbedPatterns: parseGeneratorGoEmbedPatterns(string(data)),
			}
		}
		nextGoFiles[rel] = entry
		pkgDir := filepath.Dir(rel)
		for _, pattern := range entry.EmbedPatterns {
			if err := addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nextGoFiles, nil
}

func frameworkSourceInputFile(rel string) bool {
	base := filepath.Base(rel)
	if base == "" || shouldSkipFile(rel) {
		return false
	}
	return base == "go.mod" || base == "go.sum" || filepath.Ext(rel) == ".go"
}

func frameworkMetadataFingerprint(repoRoot string, files []string) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func computeFrameworkFingerprint(repoRoot string, files []string) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
