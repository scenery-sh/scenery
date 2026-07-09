package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"scenery.sh/internal/app"
)

var generatorFingerprint struct {
	once  sync.Once
	value string
	err   error
}

func currentGeneratorFingerprint() (string, error) {
	generatorFingerprint.once.Do(func() {
		generatorFingerprint.value, generatorFingerprint.err = cachedGeneratorFingerprint(app.RepoRoot())
	})
	return generatorFingerprint.value, generatorFingerprint.err
}

const generatorFingerprintCacheSchema = "scenery.generator-fingerprint.v1"

type generatorFingerprintCache struct {
	SchemaVersion       string `json:"schema_version"`
	RepoRoot            string `json:"repo_root"`
	MetadataFingerprint string `json:"metadata_fingerprint"`
	Fingerprint         string `json:"fingerprint"`
}

func cachedGeneratorFingerprint(repoRoot string) (string, error) {
	metadataFingerprint, err := generatorMetadataFingerprint(repoRoot)
	if err != nil {
		return "", err
	}
	cachePath, err := generatorFingerprintCachePath(repoRoot)
	if err != nil {
		return "", err
	}
	if cached, ok, err := loadGeneratorFingerprintCache(cachePath); err != nil {
		return "", err
	} else if ok &&
		cached.SchemaVersion == generatorFingerprintCacheSchema &&
		cached.RepoRoot == repoRoot &&
		cached.MetadataFingerprint == metadataFingerprint &&
		cached.Fingerprint != "" {
		return cached.Fingerprint, nil
	}
	fingerprint, err := computeGeneratorFingerprint(repoRoot)
	if err != nil {
		return "", err
	}
	if err := saveGeneratorFingerprintCache(cachePath, generatorFingerprintCache{
		SchemaVersion:       generatorFingerprintCacheSchema,
		RepoRoot:            repoRoot,
		MetadataFingerprint: metadataFingerprint,
		Fingerprint:         fingerprint,
	}); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func generatorMetadataFingerprint(repoRoot string) (string, error) {
	h := sha256.New()
	paths := generatorFingerprintPaths()
	for _, rel := range paths {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorMetadataPath(h, repoRoot, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func computeGeneratorFingerprint(repoRoot string) (string, error) {
	h := sha256.New()
	for _, rel := range generatorFingerprintPaths() {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorPath(h, repoRoot, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func generatorFingerprintPaths() []string {
	return []string{
		"go.mod",
		"go.sum",
		".",
		"auth",
		"cron",
		"errs",
		"internal/app",
		"internal/build",
		"internal/codegen",
		"internal/devreport",
		"internal/envfile",
		"internal/inspect",
		"internal/localproxy",
		"internal/model",
		"internal/parse",
		"internal/redact",
		"internal/runtimeapi",
		"internal/standardauthmeta",
		"internal/stdlog",
		"internal/termstyle",
		"middleware",
		"runtime",
	}
}

func generatorFingerprintCachePath(repoRoot string) (string, error) {
	cacheRoot, err := sceneryCacheRoot()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	return filepath.Join(cacheRoot, "build", "generator-fingerprint-"+hex.EncodeToString(sum[:8])+".json"), nil
}

func loadGeneratorFingerprintCache(path string) (generatorFingerprintCache, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return generatorFingerprintCache{}, false, nil
		}
		return generatorFingerprintCache{}, false, err
	}
	var cached generatorFingerprintCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return generatorFingerprintCache{}, false, err
	}
	return cached, true, nil
}

func saveGeneratorFingerprintCache(path string, cached generatorFingerprintCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func hashGeneratorMetadataPath(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	files, err := generatorFingerprintFiles(repoRoot, path)
	if err != nil {
		return err
	}
	for _, rel := range files {
		child := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(child)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := hashGeneratorFileMetadata(h, repoRoot, child, info); err != nil {
			return err
		}
	}
	return nil
}

func hashGeneratorFileMetadata(h interface{ Write([]byte) (int, error) }, repoRoot, path string, info os.FileInfo) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	_, _ = h.Write([]byte(filepath.ToSlash(rel)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))
	_, _ = h.Write([]byte{0})
	return nil
}

func hashGeneratorPath(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	files, err := generatorFingerprintFiles(repoRoot, path)
	if err != nil {
		return err
	}
	for _, rel := range files {
		child := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorFile(h, repoRoot, child); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func generatorFingerprintFiles(repoRoot, path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil, err
		}
		return []string{filepath.ToSlash(rel)}, nil
	}
	if rel, err := filepath.Rel(repoRoot, path); err != nil {
		return nil, err
	} else if rel == "." {
		return generatorRootPackageFiles(repoRoot)
	}
	files := map[string]struct{}{}
	err = filepath.WalkDir(path, func(child string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, child)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			switch filepath.Base(rel) {
			case "node_modules", "dist", "coverage":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if d.Type()&os.ModeSymlink != 0 || filepath.Ext(child) != ".go" || strings.HasSuffix(child, "_test.go") {
			return nil
		}
		repoRel, err := filepath.Rel(repoRoot, child)
		if err != nil {
			return err
		}
		repoRel = filepath.ToSlash(repoRel)
		files[repoRel] = struct{}{}
		if err := addGeneratorEmbeddedFiles(repoRoot, repoRel, files); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func generatorRootPackageFiles(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, err
	}
	files := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files[name] = struct{}{}
		if err := addGeneratorEmbeddedFiles(repoRoot, name, files); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func hashGeneratorFile(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, _ = h.Write([]byte(filepath.ToSlash(rel)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	_, _ = h.Write([]byte{0})
	return nil
}

func addGeneratorEmbeddedFiles(repoRoot, goRel string, files map[string]struct{}) error {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(goRel)))
	if err != nil {
		return err
	}
	patterns := parseGeneratorGoEmbedPatterns(string(data))
	if len(patterns) == 0 {
		return nil
	}
	pkgDir := filepath.Dir(goRel)
	for _, pattern := range patterns {
		if err := addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files); err != nil {
			return err
		}
	}
	return nil
}

func parseGeneratorGoEmbedPatterns(src string) []string {
	var patterns []string
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:embed") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "//go:embed"))
		for rest != "" {
			token, next, ok := nextGeneratorEmbedToken(rest)
			if !ok {
				break
			}
			if token != "" {
				patterns = append(patterns, token)
			}
			rest = next
		}
	}
	return patterns
}

func nextGeneratorEmbedToken(input string) (string, string, bool) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", "", false
	}
	if quote, _ := utf8.DecodeRuneInString(input); quote == '"' || quote == '`' {
		for i := 1; i <= len(input); i++ {
			token, err := strconv.Unquote(input[:i])
			if err == nil {
				return token, input[i:], true
			}
		}
		return "", "", false
	}
	i := 0
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return input[:i], input[i:], true
}

func addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern string, files map[string]struct{}) error {
	includeHidden := false
	if strings.HasPrefix(pattern, "all:") {
		includeHidden = true
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return nil
	}
	search := filepath.Join(repoRoot, filepath.FromSlash(pkgDir), filepath.FromSlash(pattern))
	matches, err := filepath.Glob(search)
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := addGeneratorEmbeddedPath(repoRoot, match, includeHidden, files); err != nil {
			return err
		}
	}
	return nil
}

func addGeneratorEmbeddedPath(repoRoot, path string, includeHidden bool, files map[string]struct{}) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if includeHidden || !hasGeneratorHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, child)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if !includeHidden && hasGeneratorHiddenOrUnderscorePart(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if includeHidden || !hasGeneratorHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	})
}

func hasGeneratorHiddenOrUnderscorePart(rel string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}
