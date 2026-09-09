package snapshotarchive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/storagefs"
)

type Reader struct {
	file     *os.File
	Manifest Manifest
	SHA256   string
	entries  map[string]*zip.File
}

// Open holds one descriptor through checksum validation and application. A
// pathname replacement after this point cannot redirect the restored archive.
func Open(ctx context.Context, name, expected string) (*Reader, error) {
	if expected != "" && !ValidSHA256("sha256:"+expected) {
		return nil, fmt.Errorf("expected SHA-256 must be 64 lowercase hexadecimal characters")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Reader, error) { _ = f.Close(); return nil, err }
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("snapshot input must be a regular file"))
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx: ctx, reader: io.NewSectionReader(f, 0, info.Size())}); err != nil {
		return fail(err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if expected != "" && digest != expected {
		return fail(fmt.Errorf("snapshot archive SHA-256 mismatch: expected %s, got %s", expected, digest))
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return fail(err)
	}
	r := &Reader{file: f, SHA256: digest, entries: make(map[string]*zip.File, len(z.File))}
	for _, entry := range z.File {
		if err := ValidatePath(entry.Name); err != nil {
			return fail(err)
		}
		if !entry.Mode().IsRegular() {
			return fail(fmt.Errorf("snapshot contains non-regular entry %q", entry.Name))
		}
		if _, exists := r.entries[entry.Name]; exists {
			return fail(fmt.Errorf("snapshot contains duplicate entry %q", entry.Name))
		}
		r.entries[entry.Name] = entry
	}
	root := r.entries["manifest.json"]
	if root == nil || root.UncompressedSize64 > MaxManifestBytes {
		return fail(fmt.Errorf("snapshot root manifest missing or exceeds 1 MiB"))
	}
	body, err := root.Open()
	if err != nil {
		return fail(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(body, MaxManifestBytes+1))
	if err := errors.Join(readErr, body.Close()); err != nil {
		return fail(err)
	}
	if len(data) > MaxManifestBytes {
		return fail(fmt.Errorf("snapshot root manifest exceeds 1 MiB"))
	}
	if err := decodeStrict(data, &r.Manifest); err != nil {
		return fail(err)
	}
	if err := r.validate(ctx); err != nil {
		return fail(err)
	}
	return r, nil
}

func (r *Reader) Close() error { return r.file.Close() }

// CopyTo retains the exact opened, checksum-verified input, never its pathname.
func (r *Reader) CopyTo(ctx context.Context, writer io.Writer) error {
	info, err := r.file.Stat()
	if err != nil {
		return err
	}
	return copyVerified(ctx, writer, io.NewSectionReader(r.file, 0, info.Size()), info.Size(), r.SHA256)
}
func (r *Reader) Entry(name string) (io.ReadCloser, error) {
	entry := r.entries[name]
	if entry == nil {
		return nil, fmt.Errorf("snapshot entry %q is missing", name)
	}
	return entry.Open()
}

func (r *Reader) validate(ctx context.Context) error {
	m := r.Manifest
	if m.Kind != Kind || m.SchemaRevision != SchemaRevision {
		return fmt.Errorf("unsupported snapshot manifest; export legacy archives explicitly")
	}
	if m.App.ID == "" || m.App.Name == "" || m.CreatedAt.IsZero() || (m.DB == nil && m.Storage == nil) {
		return fmt.Errorf("incomplete snapshot manifest")
	}
	declared := map[string]bool{"manifest.json": true}
	if m.DB != nil {
		if m.DB.DumpFormat != "pg_custom" || m.DB.DumpFile != "db/database.postgres.dump" || m.DB.Database == "" || (m.DB.Source != "managed" && m.DB.Source != "external") || len(m.Files) != 1 || m.Files[0].Path != m.DB.DumpFile {
			return fmt.Errorf("invalid snapshot database declaration")
		}
		if err := r.verifyFile(ctx, m.Files[0]); err != nil {
			return err
		}
		declared[m.DB.DumpFile] = true
	} else if len(m.Files) != 0 {
		return fmt.Errorf("snapshot declares files without a database")
	}
	stores := map[string]bool{}
	if m.Storage != nil {
		if m.Storage.Stores == nil {
			return fmt.Errorf("snapshot storage stores are required")
		}
		for _, store := range m.Storage.Stores {
			if err := validateStoreName(store.Name); err != nil {
				return err
			}
			if stores[store.Name] || store.Files < 0 || store.Bytes < 0 || store.Manifest.Path != StoreManifestPath(store.Name) {
				return fmt.Errorf("invalid or duplicate snapshot store %q", store.Name)
			}
			stores[store.Name] = true
			if err := r.verifyFile(ctx, store.Manifest); err != nil {
				return err
			}
			declared[store.Manifest.Path] = true
			var count, bytes int64
			if err := r.VisitStore(ctx, store.Name, func(object Object, body io.Reader) error {
				name := PayloadPath(object)
				if declared[name] {
					return fmt.Errorf("snapshot contains duplicate logical tuple")
				}
				declared[name] = true
				if err := copyVerified(ctx, io.Discard, body, object.SizeBytes, object.SHA256); err != nil {
					return err
				}
				if bytes > int64(^uint64(0)>>1)-object.SizeBytes || count == int64(^uint64(0)>>1) {
					return fmt.Errorf("snapshot counters overflow")
				}
				count++
				bytes += object.SizeBytes
				return nil
			}); err != nil {
				return err
			}
			if count != store.Files || bytes != store.Bytes {
				return fmt.Errorf("snapshot store %q totals mismatch", store.Name)
			}
		}
	}
	for name := range r.entries {
		if !declared[name] {
			return fmt.Errorf("snapshot contains undeclared entry %q", name)
		}
	}
	return nil
}

func (r *Reader) verifyFile(ctx context.Context, file File) error {
	entry := r.entries[file.Path]
	if file.Bytes < 0 || !ValidSHA256(file.SHA256) || entry == nil || entry.UncompressedSize64 != uint64(file.Bytes) {
		return fmt.Errorf("snapshot file metadata mismatch for %q", file.Path)
	}
	body, err := entry.Open()
	if err != nil {
		return err
	}
	err = copyVerified(ctx, io.Discard, body, file.Bytes, strings.TrimPrefix(file.SHA256, "sha256:"))
	return errors.Join(err, body.Close())
}

// VisitStore retains one bounded logical record and one opened payload. ZIP's
// central directory is the archive index; there is no second object inventory.
func (r *Reader) VisitStore(ctx context.Context, name string, visit func(Object, io.Reader) error) error {
	body, err := r.Entry(StoreManifestPath(name))
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	scanner := bufio.NewScanner(contextReader{ctx: ctx, reader: body})
	scanner.Buffer(make([]byte, 4096), MaxObjectRecordBytes)
	for scanner.Scan() {
		var object Object
		if err := decodeStrict(scanner.Bytes(), &object); err != nil {
			return err
		}
		if err := object.Validate(); err != nil {
			return err
		}
		if object.Store != name {
			return fmt.Errorf("snapshot object store mismatch")
		}
		entry := r.entries[PayloadPath(object)]
		if entry == nil || entry.UncompressedSize64 != uint64(object.SizeBytes) {
			return fmt.Errorf("snapshot logical payload missing or has wrong size")
		}
		payload, err := entry.Open()
		if err != nil {
			return err
		}
		err = visit(object, contextReader{ctx: ctx, reader: payload})
		if err := errors.Join(err, payload.Close()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func decodeStrict(data []byte, target any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return fmt.Errorf("decode snapshot record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode snapshot record: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("snapshot record contains trailing data")
	}
	return nil
}
func validateStoreName(name string) error {
	return storagefs.ValidateScope(storagefs.Scope{Store: name})
}
