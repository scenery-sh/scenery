package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/toolchain"
)

// Prepared caches are never runtime working directories. Every hit hashes the
// copied bytes against the retained descriptor before starting a private copy.
// The input includes the complete materialized authored/generated tree, the
// verified managed toolchain manifest/platform and exact Node executable bytes.
type assistantOverlayCache struct {
	path       string
	key        string
	mcpURL     string
	onCopy     func(assistantCopyStats, error)
	onRelocate func(time.Time, error)
}

type assistantOverlayCacheRecord struct {
	Key       string                   `json:"key"`
	BuildRoot string                   `json:"build_root"`
	MCPURL    string                   `json:"mcp_url"`
	Tree      runtimeassets.Descriptor `json:"tree"`
}

func openAssistantOverlayCache(root, overlay, node, mcpURL string) (*assistantOverlayCache, error) {
	inputs, err := assistantOverlayInputDigest(overlay)
	if err != nil {
		return nil, err
	}
	nodeDigest, err := eve.PackageLockDigest(node)
	if err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(digestBytes([]byte(strings.Join([]string{
		"assistant-prepared-v1", inputs, nodeDigest,
		toolchain.BundledManifestSHA256(), toolchain.CurrentPlatform().String(),
	}, "\n"))), "sha256:")
	base := filepath.Join(root, ".scenery", "assistant-cache", "prepared")
	if err := rejectExistingAssistantPathSymlinks(root, base); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	return &assistantOverlayCache{path: filepath.Join(base, key), key: key, mcpURL: mcpURL}, nil
}

func assistantOverlayInputDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported assistant input %q", relative)
		}
		digest := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest = digestBytes(data)
		}
		_, _ = fmt.Fprintf(hash, "%q %o %s\n", filepath.ToSlash(relative), info.Mode(), digest)
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *assistantOverlayCache) restore(ctx context.Context, overlay string) (bool, error) {
	info, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("prepared cache is not a directory")
	}
	recordPath := filepath.Join(c.path, "record.json")
	info, err = os.Lstat(recordPath)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("prepared cache record is not an owner-only regular file")
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return false, err
	}
	var record assistantOverlayCacheRecord
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return false, err
	}
	if err := decodeJSONExact(data, &record); err != nil {
		return false, err
	}
	if record.Key != c.key || !filepath.IsAbs(record.BuildRoot) || record.MCPURL == "" {
		return false, errors.New("prepared cache input identity mismatch")
	}
	if err := record.Tree.Validate(); err != nil {
		return false, err
	}
	stats := assistantCopyStats{Started: time.Now()}
	err = copyAssistantPreparedTreeMeasured(ctx, filepath.Join(c.path, "tree"), overlay, &record.Tree, &stats)
	stats.Duration = time.Since(stats.Started)
	if c.onCopy != nil {
		c.onCopy(stats, err)
	}
	if err != nil {
		return false, err
	}
	relocationStarted := time.Now()
	defer func() {
		if c.onRelocate != nil {
			c.onRelocate(relocationStarted, err)
		}
	}()
	index := filepath.Join(overlay, ".output", "server", "index.mjs")
	data, err = os.ReadFile(index)
	if err != nil {
		return false, err
	}
	data, err = relocateAssistantBuildManifest(data, record.BuildRoot, overlay, record.MCPURL, c.mcpURL)
	if err != nil {
		return false, err
	}
	if err = os.WriteFile(index, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (c *assistantOverlayCache) publish(ctx context.Context, overlay string) error {
	// Validate the pinned provider output shape before making it reusable.
	index, err := os.ReadFile(filepath.Join(overlay, ".output", "server", "index.mjs"))
	if err != nil {
		return err
	}
	if _, err := relocateAssistantBuildManifest(index, overlay, overlay, c.mcpURL, c.mcpURL); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(c.path), ".stage-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	tree := filepath.Join(stage, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return err
	}
	if err := copyAssistantPreparedTree(ctx, overlay, tree, nil); err != nil {
		return err
	}
	descriptor, err := runtimeassets.DescribeTree(tree)
	if err != nil {
		return err
	}
	record := assistantOverlayCacheRecord{Key: c.key, BuildRoot: overlay, MCPURL: c.mcpURL, Tree: descriptor}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "record.json"), data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(stage, c.path); err != nil {
		// Another owner may have published the same exact inputs. Never overwrite
		// its output; the next hit still verifies its retained bytes independently.
		if info, statErr := os.Lstat(c.path); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		return err
	}
	return nil
}

// Only dependency/build output is cached. Provider state, HOME, and ephemeral
// compilation metadata remain private, as in the existing production capsule.
func assistantPreparedPath(path string) bool {
	if path != "node_modules" && !strings.HasPrefix(path, "node_modules/") && path != ".output" && !strings.HasPrefix(path, ".output/") {
		return false
	}
	for _, excluded := range []string{".output/.eve", "node_modules/.cache", "node_modules/.nitro", "node_modules/.package-lock.json"} {
		if path == excluded || strings.HasPrefix(path, excluded+"/") {
			return false
		}
	}
	return true
}

func copyAssistantPreparedTree(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor) error {
	return copyAssistantPreparedTreeMeasured(ctx, source, destination, expected, nil)
}

func copyAssistantPreparedTreeMeasured(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor, stats *assistantCopyStats) error {
	entries := map[string]runtimeassets.Entry{}
	if expected != nil {
		for _, entry := range expected.Entries {
			entries[entry.Path] = entry
		}
	}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !assistantPreparedPath(relative) {
			if expected != nil {
				return fmt.Errorf("unexpected prepared cache path %q", relative)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		actual := runtimeassets.Entry{Path: relative}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !assistantPathWithin(destination, filepath.Join(filepath.Dir(target), link)) {
				return errors.New("prepared cache symlink escapes tree")
			}
			actual.Kind, actual.Target = runtimeassets.EntrySymlink, link
		case info.IsDir():
			if info.Mode().Perm() != 0o755 {
				return fmt.Errorf("unsupported prepared directory mode: %s", relative)
			}
			actual.Kind, actual.Mode = runtimeassets.EntryDirectory, 0o755
		case info.Mode().IsRegular():
			actual.Kind, actual.Mode = runtimeassets.EntryFile, uint32(info.Mode().Perm())
			if actual.Mode != 0o644 && actual.Mode != 0o755 {
				return fmt.Errorf("unsupported prepared file mode: %s", relative)
			}
		default:
			return fmt.Errorf("unsupported prepared cache path: %s", relative)
		}
		var data []byte
		if actual.Kind == runtimeassets.EntryFile {
			started := time.Now()
			data, err = os.ReadFile(path)
			if stats != nil {
				stats.Read += time.Since(started)
			}
			if err != nil {
				return err
			}
			started = time.Now()
			actual.Size, actual.Digest = int64(len(data)), digestBytes(data)
			if stats != nil {
				stats.Hash += time.Since(started)
				stats.Files++
				stats.Bytes += actual.Size
			}
		}
		if expected != nil {
			if want, ok := entries[relative]; !ok || want != actual {
				return fmt.Errorf("prepared cache content mismatch: %s", relative)
			}
			delete(entries, relative)
		}
		if stats != nil {
			stats.Entries++
		}
		started := time.Now()
		switch actual.Kind {
		case runtimeassets.EntryDirectory:
			err = os.Mkdir(target, 0o755)
		case runtimeassets.EntrySymlink:
			err = os.Symlink(actual.Target, target)
		default:
			err = os.WriteFile(target, data, fs.FileMode(actual.Mode))
		}
		if stats != nil {
			stats.Write += time.Since(started)
		}
		return err
	})
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("prepared cache files are missing")
	}
	return nil
}

// Eve's pinned server output embeds one JSON discovery manifest. Relocate only
// its known root fields and generated Scenery connection URL, never authored
// JavaScript, arbitrary strings, or dependency bytes.
func relocateAssistantBuildManifest(data []byte, oldRoot, newRoot, oldURL, newURL string) ([]byte, error) {
	marker := []byte("const manifest = {\n")
	start := bytes.Index(data, marker)
	if start < 0 || bytes.Count(data, marker) != 1 {
		return nil, errors.New("unsupported assistant build manifest")
	}
	start += len("const manifest = ")
	end := bytes.Index(data[start:], []byte("\n};"))
	if end < 0 {
		return nil, errors.New("unterminated assistant build manifest")
	}
	end += start + 2
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data[start:end], &manifest); err != nil {
		return nil, err
	}
	for key, pair := range map[string][2]string{"appRoot": {oldRoot, newRoot}, "agentRoot": {filepath.Join(oldRoot, "agent"), filepath.Join(newRoot, "agent")}} {
		var value string
		if json.Unmarshal(manifest[key], &value) != nil || value != pair[0] {
			return nil, fmt.Errorf("assistant build manifest %s mismatch", key)
		}
		manifest[key], _ = json.Marshal(pair[1])
	}
	var connections []map[string]json.RawMessage
	if err := json.Unmarshal(manifest["connections"], &connections); err != nil {
		return nil, err
	}
	count := 0
	for _, connection := range connections {
		var name, url string
		_ = json.Unmarshal(connection["connectionName"], &name)
		if name != "scenery" {
			continue
		}
		if json.Unmarshal(connection["url"], &url) != nil || url != oldURL {
			return nil, errors.New("assistant build manifest MCP URL mismatch")
		}
		connection["url"], _ = json.Marshal(newURL)
		count++
	}
	if count != 1 {
		return nil, errors.New("assistant build manifest Scenery connection missing or duplicated")
	}
	manifest["connections"], _ = json.Marshal(connections)
	replacement, err := json.MarshalIndent(manifest, "", "\t")
	if err != nil {
		return nil, err
	}
	result := append([]byte(nil), data[:start]...)
	result = append(result, replacement...)
	return append(result, data[end:]...), nil
}
