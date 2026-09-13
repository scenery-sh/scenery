package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

const (
	sharedCompositionKind                   = "scenery.shared-workspace-composition"
	sharedCompositionSchemaDescriptor       = `{"files":"map<path,bytes>","generator_fingerprint":"digest","key":"digest","kind":"scenery.shared-workspace-composition","payload_sha256":"digest","producer":"producer","schema_revision":"digest","spec_revision":"digest"}`
	sharedCompositionCacheVersion           = "v1"
	sharedCompositionCacheEntries           = 64
	sharedCompositionCacheBytes       int64 = 32 << 20
)

type sharedCompositionArtifact struct {
	machine.ArtifactIdentity
	Key                  string            `json:"key"`
	GeneratorFingerprint string            `json:"generator_fingerprint"`
	PayloadSHA256        string            `json:"payload_sha256"`
	Files                map[string][]byte `json:"files"`
}

type sharedCompositionKeyInput struct {
	Producer             machine.Producer         `json:"producer"`
	GeneratorFingerprint string                   `json:"generator_fingerprint"`
	AppName              string                   `json:"app_name"`
	Config               app.Config               `json:"config"`
	CompositionImport    string                   `json:"composition_import"`
	SQLRequirements      compiler.SQLRequirements `json:"sql_requirements"`
}

func sharedCompositionKey(appName string, cfg app.Config, compositionImport string, sql compiler.SQLRequirements, generatorFingerprint string) (string, error) {
	if strings.TrimSpace(generatorFingerprint) == "" {
		return "", fmt.Errorf("shared composition cache requires generator identity")
	}
	encoded, err := json.Marshal(sharedCompositionKeyInput{
		Producer: machine.RuntimeProducer(), GeneratorFingerprint: generatorFingerprint,
		AppName: appName, Config: cfg, CompositionImport: compositionImport,
		SQLRequirements: sql,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func sharedCompositionRoot() (string, error) {
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "build", "shared-compositions", sharedCompositionCacheVersion), nil
}

func renderSharedCompositionContext(ctx context.Context, appName string, cfg app.Config, compositionImport string, sql compiler.SQLRequirements, generatorFingerprint string) (*codegen.Output, error) {
	started := time.Now()
	key, err := sharedCompositionKey(appName, cfg, compositionImport, sql, generatorFingerprint)
	if err != nil {
		finishStep(ctx, "workspace.render", started, "miss", "invalid_content_key", err)
		return nil, err
	}
	root, err := sharedCompositionRoot()
	if err == nil {
		if files, hit := loadSharedComposition(root, key, generatorFingerprint); hit {
			finishStep(ctx, "workspace.render", started, "hit", "shared_content_artifact", nil)
			return &codegen.Output{Generated: files}, nil
		}
	}

	var release func()
	if err == nil {
		release, err = acquireSharedBinaryLock(ctx, filepath.Join(root, "locks", "action-"+key[:2]+".lock"))
		if err != nil {
			finishStep(ctx, "workspace.render", started, "miss", "shared_lock_wait", err)
			return nil, err
		}
		defer release()
		if files, hit := loadSharedComposition(root, key, generatorFingerprint); hit {
			finishStep(ctx, "workspace.render", started, "hit", "joined_shared_content_artifact", nil)
			return &codegen.Output{Generated: files}, nil
		}
	}

	output, renderErr := codegen.Generate(appName, cfg, compositionImport, sql)
	if renderErr != nil {
		finishStep(ctx, "workspace.render", started, "miss", "render_failed", renderErr)
		return nil, renderErr
	}
	if root != "" {
		// Cache publication and retention are optimizations. The exact freshly
		// rendered bytes remain authoritative when the shared cache is unavailable.
		_ = publishSharedComposition(root, key, generatorFingerprint, output.Generated)
		_ = pruneSharedCompositions(root, key)
	}
	finishStep(ctx, "workspace.render", started, "miss", "rendered_and_published", nil)
	return output, nil
}

func loadSharedComposition(root, key, generatorFingerprint string) (map[string][]byte, bool) {
	if len(key) != 64 {
		return nil, false
	}
	for _, char := range key {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return nil, false
		}
	}
	path := filepath.Join(root, "artifacts", key+".json")
	if ok, err := regularArtifactPath(path); err != nil || !ok {
		return nil, false
	}
	if info, err := os.Stat(path); err != nil || info.Size() > sharedCompositionCacheBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var artifact sharedCompositionArtifact
	if err := machine.DecodeArtifact(data, &artifact, &artifact.ArtifactIdentity, sharedCompositionKind, sharedCompositionSchemaDescriptor, "regenerate the shared workspace composition"); err != nil {
		return nil, false
	}
	if artifact.Producer != machine.RuntimeProducer() || artifact.Key != key || artifact.GeneratorFingerprint != generatorFingerprint ||
		!validSharedCompositionFiles(artifact.Files) || artifact.PayloadSHA256 != compositionPayloadDigest(artifact.Files) {
		return nil, false
	}
	return cloneCompositionFiles(artifact.Files), true
}

func publishSharedComposition(root, key, generatorFingerprint string, files map[string][]byte) error {
	if !validSharedCompositionFiles(files) {
		return fmt.Errorf("shared composition contains invalid generated paths or exceeds its payload bound")
	}
	artifact := sharedCompositionArtifact{
		ArtifactIdentity: machine.NewArtifactIdentity(sharedCompositionKind, sharedCompositionSchemaDescriptor),
		Key:              key, GeneratorFingerprint: generatorFingerprint, Files: cloneCompositionFiles(files),
	}
	artifact.PayloadSHA256 = compositionPayloadDigest(artifact.Files)
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".publish-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	final := filepath.Join(dir, key+".json")
	if err := os.Remove(final); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, final)
}

func validSharedCompositionFiles(files map[string][]byte) bool {
	if len(files) == 0 {
		return false
	}
	var total int64
	for path, data := range files {
		clean := filepath.ToSlash(filepath.Clean(path))
		if path == "" || clean != path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(path) {
			return false
		}
		total += int64(len(path) + len(data))
		if total > sharedCompositionCacheBytes {
			return false
		}
	}
	return true
}

func compositionPayloadDigest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, filepath.ToSlash(path))
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(files[path])
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cloneCompositionFiles(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files))
	for path, data := range files {
		cloned[filepath.ToSlash(path)] = append([]byte(nil), data...)
	}
	return cloned
}

func pruneSharedCompositions(root, keep string) error {
	dir := filepath.Join(root, "artifacts")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type entry struct {
		name    string
		modTime time.Time
		size    int64
	}
	var cached []entry
	var total int64
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") || len(strings.TrimSuffix(item.Name(), ".json")) != 64 {
			continue
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		cached = append(cached, entry{name: item.Name(), modTime: info.ModTime(), size: info.Size()})
		total += info.Size()
	}
	sort.Slice(cached, func(i, j int) bool { return cached[i].modTime.Before(cached[j].modTime) })
	keep += ".json"
	for len(cached) > sharedCompositionCacheEntries || total > sharedCompositionCacheBytes {
		item := cached[0]
		cached = cached[1:]
		if item.name == keep {
			cached = append(cached, item)
			if len(cached) == 1 {
				break
			}
			continue
		}
		if err := os.Remove(filepath.Join(dir, item.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= item.size
	}
	return nil
}
