// Package snapshotarchive owns the portable logical snapshot format. It knows
// nothing about database provisioning, retained worktree ownership or recovery.
package snapshotarchive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"scenery.sh/internal/storagefs"
)

const Kind = "scenery.snapshot.manifest"

// Bound to the complete checked schema, including the streamed object shape.
const SchemaRevision = "sha256:6885a140ff0b5b20f4639d1837609b28bb28735e803c7ce80561c49401654e87"

const MaxManifestBytes = 1 << 20
const MaxObjectRecordBytes = 64 << 10

type Manifest struct {
	Kind           string    `json:"kind"`
	SchemaRevision string    `json:"schema_revision"`
	CreatedAt      time.Time `json:"created_at"`
	App            App       `json:"app"`
	DB             *Database `json:"db,omitempty"`
	Storage        *Storage  `json:"storage,omitempty"`
	Files          []File    `json:"files"`
}
type App struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}
type Database struct {
	Database   string   `json:"database"`
	Source     string   `json:"source"`
	Schemas    []Schema `json:"schemas"`
	DumpFile   string   `json:"dump_file"`
	DumpFormat string   `json:"dump_format"`
}
type Schema struct {
	Service string `json:"service"`
	Schema  string `json:"schema"`
}
type Storage struct {
	Provenance *Provenance `json:"provenance,omitempty"`
	Stores     []Store     `json:"stores"`
}
type Provenance struct {
	AppRoot     string `json:"app_root"`
	WorktreeKey string `json:"worktree_key"`
}
type Store struct {
	Name     string `json:"name"`
	Files    int64  `json:"files"`
	Bytes    int64  `json:"bytes"`
	Manifest File   `json:"manifest"`
}
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Object omits source ETags and physical paths deliberately. The tuple and
// content metadata survive import; concurrency identity does not.
type Object struct {
	Store       string            `json:"store"`
	Tenant      string            `json:"tenant,omitempty"`
	Key         string            `json:"key"`
	SizeBytes   int64             `json:"size_bytes"`
	ContentType string            `json:"content_type,omitempty"`
	SHA256      string            `json:"sha256"`
	ModifiedAt  time.Time         `json:"modified_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func Logical(object storagefs.Object) Object {
	return Object{Store: object.Store, Tenant: object.Tenant, Key: object.Key, SizeBytes: object.SizeBytes, ContentType: object.ContentType, SHA256: object.SHA256, ModifiedAt: object.ModifiedAt, Metadata: object.Metadata}
}
func (o Object) StorageObject() storagefs.Object {
	return storagefs.Object{Store: o.Store, Tenant: o.Tenant, Key: o.Key, SizeBytes: o.SizeBytes, ContentType: o.ContentType, SHA256: o.SHA256, ModifiedAt: o.ModifiedAt, Metadata: o.Metadata}
}
func (o Object) Validate() error { return storagefs.ValidateLogicalObject(o.StorageObject()) }

func tupleToken(values ...string) string {
	h := sha256.New()
	for _, value := range values {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func PayloadPath(o Object) string {
	return "storage/payloads/" + tupleToken(o.Store, o.Tenant, o.Key) + ".data"
}
func StoreManifestPath(name string) string { return "storage/manifests/" + tupleToken(name) + ".jsonl" }

func ValidatePath(name string) error {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name || strings.HasSuffix(name, "/") || strings.ContainsRune(name, 0) {
		return fmt.Errorf("invalid snapshot archive path %q", name)
	}
	return nil
}
func ValidSHA256(value string) bool {
	value, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'f') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
