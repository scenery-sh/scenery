package storagefs

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"scenery.sh/internal/machine"
)

const (
	referenceKind       = "scenery.storage.reference"
	objectDescriptor    = `{"store":"string","tenant":"string","key":"string","size_bytes":"integer","content_type":"string","etag":"string","sha256":"string","modified_at":"date-time","metadata":{"additionalProperties":"string"}}`
	referenceDescriptor = `{"identity":"artifact","object":` + objectDescriptor + `,"version_id":"string"}`
)

type Scope struct {
	Store  string `json:"store"`
	Tenant string `json:"tenant"`
}

type Object struct {
	Store       string            `json:"store"`
	Tenant      string            `json:"tenant,omitempty"`
	Key         string            `json:"key"`
	SizeBytes   int64             `json:"size_bytes"`
	ContentType string            `json:"content_type,omitempty"`
	ETag        string            `json:"etag"`
	SHA256      string            `json:"sha256"`
	ModifiedAt  time.Time         `json:"modified_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type reference struct {
	machine.ArtifactIdentity
	Object    Object `json:"object"`
	VersionID string `json:"version_id"`
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	IfNoneMatch bool
	IfMatch     string
}

func ValidatePutOptions(opts PutOptions) error {
	if opts.IfNoneMatch && opts.IfMatch != "" {
		return fmt.Errorf("%w: write preconditions are mutually exclusive", ErrInvalid)
	}
	return validateMetadata(opts.ContentType, opts.Metadata)
}

type DeleteOptions struct{ IfMatch string }
type GetOptions struct{ Offset, Length *int64 }

type Store struct {
	namespace *Namespace
	scope     Scope
	maxBytes  int64
}

func (n *Namespace) Store(scope Scope, maxBytes int64) (*Store, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("%w: negative object size limit", ErrInvalid)
	}
	return &Store{namespace: n, scope: scope, maxBytes: maxBytes}, nil
}

func (s Scope) validate() error {
	if s.Store == "" {
		return fmt.Errorf("%w: store is required", ErrInvalid)
	}
	for _, c := range s.Store {
		valid := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !valid {
			return fmt.Errorf("%w: invalid store name", ErrInvalid)
		}
	}
	if len(s.Tenant) > 256 || !utf8.ValidString(s.Tenant) {
		return fmt.Errorf("%w: tenant must be valid UTF-8 of at most 256 bytes", ErrInvalid)
	}
	for _, c := range s.Tenant {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("%w: tenant contains control characters", ErrInvalid)
		}
	}
	return nil
}

func ValidateScope(scope Scope) error { return scope.validate() }

func ValidateKey(key string) error       { return validatePath(key, false) }
func ValidatePrefix(prefix string) error { return validatePath(prefix, true) }

func validatePath(value string, prefix bool) error {
	if value == "" && prefix {
		return nil
	}
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return fmt.Errorf("%w: key must be nonempty UTF-8 of at most 4096 bytes", ErrInvalid)
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") {
		return fmt.Errorf("%w: key must be a normalized relative logical path", ErrInvalid)
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("%w: key contains control characters", ErrInvalid)
		}
	}
	trimmed := value
	if prefix {
		trimmed = strings.TrimSuffix(trimmed, "/")
	}
	if path.Clean(trimmed) != trimmed {
		return fmt.Errorf("%w: key must be normalized", ErrInvalid)
	}
	for part := range strings.SplitSeq(trimmed, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("%w: traversal segments are forbidden", ErrInvalid)
		}
	}
	return nil
}

func validateMetadata(contentType string, metadata map[string]string) error {
	if len(contentType) > 255 || !utf8.ValidString(contentType) {
		return fmt.Errorf("%w: content type exceeds 255 UTF-8 bytes", ErrInvalid)
	}
	for _, c := range contentType {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("%w: content type contains control characters", ErrInvalid)
		}
	}
	for k, v := range metadata {
		if !utf8.ValidString(k) || !utf8.ValidString(v) {
			return fmt.Errorf("%w: metadata must be UTF-8", ErrInvalid)
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(encoded) > 16<<10 {
		return fmt.Errorf("%w: metadata exceeds 16 KiB", ErrInvalid)
	}
	return nil
}

// A committed object must fit a list page together with its largest cursor.
// Metadata is not listed; reserve room for the fresh fixed-width ETag even
// when validating an imported descriptor before its version is assigned.
func validateListDescriptor(object Object) error {
	object.Metadata = nil
	object.ETag = `"00000000000000000000000000000000"`
	data, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("%w: invalid object descriptor: %w", ErrInvalid, err)
	}
	if len(data) > MaxPageBytes-(12<<10)-256 {
		return fmt.Errorf("%w: object descriptor exceeds the bounded list-page budget", ErrInvalid)
	}
	return nil
}

func (s Scope) partition() string {
	tenantToken := "unscoped"
	if s.Tenant != "" {
		tenantToken = token("tenant", s.Tenant)
	}
	return filepath.Join(token("store", s.Store), tenantToken)
}

func (s Scope) refPath(generation, key string) string {
	hash := token("key", key)
	return filepath.Join(generationPath(generation), "refs", s.partition(), hash[:2], hash+".json")
}

func (s Scope) versionPath(generation, key, version string) string {
	hash := token("key", key)
	return filepath.Join(generationPath(generation), "versions", s.partition(), hash[:2], hash, version+".data")
}

func (r reference) validate(scope Scope, key string) error {
	o := r.Object
	if o.Store != scope.Store || o.Tenant != scope.Tenant || o.Key != key {
		return fmt.Errorf("%w: reference tuple mismatch", ErrCorrupt)
	}
	if !isHexID(r.VersionID, 16) || !isHexID(o.SHA256, 32) || len(o.ETag) != 34 || o.ETag[0] != '"' || o.ETag[33] != '"' || !isHexID(o.ETag[1:33], 16) || o.SizeBytes < 0 || o.ModifiedAt.IsZero() {
		return fmt.Errorf("%w: invalid reference metadata", ErrCorrupt)
	}
	if err := ValidateKey(key); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := validateMetadata(o.ContentType, o.Metadata); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := validateListDescriptor(o); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return nil
}
