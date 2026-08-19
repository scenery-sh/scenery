// Package runtimeassets provides deterministic, verified packaging for the
// child runtimes shipped with a Scenery application.
//
// Runtime assets are deliberately represented by a small provider-neutral
// tree descriptor.  The descriptor records every path, safe permission mode,
// and content digest, while archive bytes are a transport detail.  The
// descriptor is also what an installed runtime is checked against before it
// can be reused.
package runtimeassets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/spec"
)

const (
	schemaDescriptor               = `{"schema_revision":"digest","entries":"array<entry{path,kind,mode,size,digest,target}>","digest":"sha256"}`
	AssistantAssetKind             = "scenery.assistant.runtime-assets"
	assistantAssetSchemaDescriptor = `{"kind":"scenery.assistant.runtime-assets","schema_revision":"digest","assistant_address":"address","target":"goos/goarch","runtime_revision":"revision","capability_revision":"digest","node_archive_digest":"digest","node_tree_digest":"digest","capsule_archive_digest":"digest","capsule_tree_digest":"digest","capsule_entry":".scenery/bootstrap.mjs","package_lock_digest":"digest"}`
	// DescriptorFilename is the private install manifest written beside an
	// extracted tree.  It is never included in the tree descriptor itself.
	DescriptorFilename = ".scenery-runtime-assets.json"
	// AssetDirectory is the content-addressed directory below an install root.
	AssetDirectory = "runtime-assets"
)

// SchemaRevision identifies the complete structural descriptor encoding.
var SchemaRevision = string(spec.SchemaRevision(schemaDescriptor))

// AssistantAssetSchemaRevision identifies the provider-neutral embedded
// assistant asset descriptor shared by build generation and runtime launch.
var AssistantAssetSchemaRevision = string(spec.SchemaRevision(assistantAssetSchemaDescriptor))

// EntryKind is the type of one tree entry.
type EntryKind string

const (
	EntryDirectory EntryKind = "directory"
	EntryFile      EntryKind = "file"
	EntrySymlink   EntryKind = "symlink"
)

// Entry is one canonical tree entry.  Paths always use slash separators and
// never begin with a slash or contain a dot path component.
type Entry struct {
	Path   string    `json:"path"`
	Kind   EntryKind `json:"kind"`
	Mode   uint32    `json:"mode"`
	Size   int64     `json:"size,omitempty"`
	Digest string    `json:"digest,omitempty"`
	Target string    `json:"target,omitempty"`
}

// Descriptor is the content identity of an asset tree.  Digest is the SHA-256
// digest of the canonical descriptor payload (the same payload with Digest
// omitted), not of the JSON object containing the digest itself.
type Descriptor struct {
	SchemaRevision string  `json:"schema_revision"`
	Entries        []Entry `json:"entries"`
	Digest         string  `json:"digest"`
}

// Archive contains deterministic gzip-compressed tar bytes and the tree they
// represent.  ArchiveDigest is the digest of Data; Descriptor.Digest is the
// content-addressed tree identity used for installation.
type Archive struct {
	Data          []byte     `json:"-"`
	ArchiveDigest string     `json:"archive_digest"`
	Descriptor    Descriptor `json:"descriptor"`
}

// NewArchive validates archive bytes and returns an immutable value carrying
// their digest and expected tree descriptor.  It does not extract the bytes;
// Install performs that check while writing its private staging directory.
func NewArchive(data []byte, descriptor Descriptor) (Archive, error) {
	if err := descriptor.Validate(); err != nil {
		return Archive{}, fmt.Errorf("validate descriptor: %w", err)
	}
	if len(data) == 0 {
		return Archive{}, fmt.Errorf("archive is empty")
	}
	return Archive{
		Data:          append([]byte(nil), data...),
		ArchiveDigest: digestBytes(data),
		Descriptor:    cloneDescriptor(descriptor),
	}, nil
}

// Validate checks descriptor structure, safe modes, path ordering, and the
// canonical descriptor digest.
func (d Descriptor) Validate() error {
	if d.SchemaRevision != SchemaRevision {
		return fmt.Errorf("unsupported descriptor schema revision %q", d.SchemaRevision)
	}
	previous := ""
	seen := make(map[string]struct{}, len(d.Entries))
	kinds := make(map[string]EntryKind, len(d.Entries))
	for i, entry := range d.Entries {
		if err := validateArchivePath(entry.Path); err != nil {
			return fmt.Errorf("entries[%d] path: %w", i, err)
		}
		if i > 0 && entry.Path <= previous {
			return fmt.Errorf("entries are not in lexical order at %q", entry.Path)
		}
		previous = entry.Path
		if _, ok := seen[entry.Path]; ok {
			return fmt.Errorf("duplicate entry %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		kinds[entry.Path] = entry.Kind
		switch entry.Kind {
		case EntryDirectory:
			if entry.Mode != uint32(0o755) || entry.Size != 0 || entry.Digest != "" || entry.Target != "" {
				return fmt.Errorf("directory %q has invalid metadata", entry.Path)
			}
		case EntryFile:
			if entry.Mode != uint32(0o644) && entry.Mode != uint32(0o755) {
				return fmt.Errorf("file %q has unsafe mode %04o", entry.Path, entry.Mode)
			}
			if entry.Size < 0 || !validDigest(entry.Digest) || entry.Target != "" {
				return fmt.Errorf("file %q has invalid metadata", entry.Path)
			}
		case EntrySymlink:
			if entry.Mode != 0 || entry.Size != 0 || entry.Digest != "" {
				return fmt.Errorf("symlink %q has invalid metadata", entry.Path)
			}
			if err := validateSymlinkTarget(entry.Path, entry.Target); err != nil {
				return fmt.Errorf("symlink %q target: %w", entry.Path, err)
			}
		default:
			return fmt.Errorf("entry %q has unsupported kind %q", entry.Path, entry.Kind)
		}
	}
	for _, entry := range d.Entries {
		parts := strings.Split(entry.Path, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if kinds[parent] != EntryDirectory {
				return fmt.Errorf("entry %q has missing directory parent %q", entry.Path, parent)
			}
		}
	}
	if d.Digest != descriptorDigest(d) {
		return fmt.Errorf("descriptor digest mismatch: got %q want %q", d.Digest, descriptorDigest(d))
	}
	return nil
}

// CanonicalBytes returns the stable JSON payload used for Descriptor.Digest.
func (d Descriptor) CanonicalBytes() ([]byte, error) {
	if d.SchemaRevision == "" {
		d.SchemaRevision = SchemaRevision
	}
	entries := append([]Entry(nil), d.Entries...)
	payload := descriptorPayload{SchemaRevision: d.SchemaRevision, Entries: entries}
	return json.Marshal(payload)
}

// MarshalJSON emits a canonical descriptor after validating it.  This makes
// descriptors suitable for embedding and for the install manifest.
func (d Descriptor) MarshalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	type descriptorJSON Descriptor
	return json.Marshal(descriptorJSON(d))
}

// DescribeTree computes a canonical descriptor for root without following
// symlinks.  Only regular files, directories, and safe in-root symlinks are
// accepted.  File modes are intentionally limited to 0644 and 0755; this
// avoids packaging writable or special-mode runtime content.
func DescribeTree(root string) (Descriptor, error) {
	return describeTree(root, false)
}

func describeTree(root string, allowManifest bool) (Descriptor, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Descriptor{}, fmt.Errorf("resolve tree root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return Descriptor{}, fmt.Errorf("stat tree root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Descriptor{}, fmt.Errorf("tree root is not a directory")
	}
	entries := make([]Entry, 0)
	err = filepath.WalkDir(absolute, func(name string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == absolute {
			return nil
		}
		rel, err := filepath.Rel(absolute, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == DescriptorFilename {
			if allowManifest {
				return nil
			}
			return fmt.Errorf("reserved path %q", rel)
		}
		if err := validateArchivePath(rel); err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			if err := validateSymlinkTarget(rel, target); err != nil {
				return err
			}
			entries = append(entries, Entry{Path: rel, Kind: EntrySymlink, Target: target})
		case info.IsDir():
			if info.Mode().Perm() != 0o755 {
				return fmt.Errorf("directory %q has unsafe mode %04o", rel, info.Mode().Perm())
			}
			entries = append(entries, Entry{Path: rel, Kind: EntryDirectory, Mode: 0o755})
		case info.Mode().IsRegular():
			mode := info.Mode().Perm()
			if mode != 0o644 && mode != 0o755 {
				return fmt.Errorf("file %q has unsafe mode %04o", rel, mode)
			}
			digest, size, err := digestFile(name)
			if err != nil {
				return fmt.Errorf("digest file %q: %w", rel, err)
			}
			entries = append(entries, Entry{Path: rel, Kind: EntryFile, Mode: uint32(mode), Size: size, Digest: digest})
		default:
			return fmt.Errorf("path %q has unsupported type %s", rel, info.Mode().String())
		}
		return nil
	})
	if err != nil {
		return Descriptor{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	d := Descriptor{SchemaRevision: SchemaRevision, Entries: entries}
	d.Digest = descriptorDigest(d)
	return d, nil
}

type descriptorPayload struct {
	SchemaRevision string  `json:"schema_revision"`
	Entries        []Entry `json:"entries"`
}

func descriptorDigest(d Descriptor) string {
	payload := descriptorPayload{SchemaRevision: d.SchemaRevision, Entries: d.Entries}
	b, _ := json.Marshal(payload)
	return digestBytes(b)
}

func digestFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("file changed to non-regular type")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func cloneDescriptor(d Descriptor) Descriptor {
	d.Entries = append([]Entry(nil), d.Entries...)
	return d
}

func validateArchivePath(name string) error {
	if name == "" || name == "." || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("invalid empty or NUL path")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("absolute path is not allowed")
	}
	if len(name) >= 2 && name[1] == ':' {
		return fmt.Errorf("volume path is not allowed")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("backslash path separator is not allowed")
	}
	parts := strings.SplitSeq(name, "/")
	for part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path contains unsafe component")
		}
	}
	if path.Clean(name) != name {
		return fmt.Errorf("path is not canonical")
	}
	return nil
}

func validateSymlinkTarget(link, target string) error {
	if target == "" {
		return fmt.Errorf("empty target")
	}
	if strings.ContainsRune(target, '\x00') || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "\\") || (len(target) >= 2 && target[1] == ':') {
		return fmt.Errorf("absolute target is not allowed")
	}
	if strings.Contains(target, "\\") || path.Clean(target) != target || target == "." {
		return fmt.Errorf("target is not canonical")
	}
	resolved := path.Join(path.Dir(link), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("target escapes tree")
	}
	return nil
}
