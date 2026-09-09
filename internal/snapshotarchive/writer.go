package snapshotarchive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type Writer struct {
	zip      *zip.Writer
	manifest Manifest
	stores   map[string]bool
}

func NewWriter(w io.Writer, manifest Manifest) *Writer {
	manifest.Kind = Kind
	manifest.SchemaRevision = SchemaRevision
	manifest.Files = []File{}
	if manifest.Storage != nil {
		copy := *manifest.Storage
		copy.Stores = []Store{}
		manifest.Storage = &copy
	}
	return &Writer{zip: zip.NewWriter(w), manifest: manifest, stores: make(map[string]bool)}
}

func (w *Writer) AddDatabase(ctx context.Context, write func(io.Writer) error) error {
	if w.manifest.DB == nil || len(w.manifest.Files) != 0 || w.manifest.DB.DumpFile != "db/database.postgres.dump" || w.manifest.DB.DumpFormat != "pg_custom" {
		return fmt.Errorf("invalid snapshot database section")
	}
	entry, err := w.entry(w.manifest.DB.DumpFile, zip.Store)
	if err != nil {
		return err
	}
	h := sha256.New()
	count := &countWriter{}
	if err := write(&contextWriter{ctx: ctx, writer: io.MultiWriter(entry, h, count)}); err != nil {
		return err
	}
	w.manifest.Files = append(w.manifest.Files, File{Path: w.manifest.DB.DumpFile, Bytes: count.n, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))})
	return nil
}

// AddStore spools only its JSONL metadata to a private temporary file. Payloads
// stream directly into ZIP, so root metadata and retained object memory remain
// bounded independently of the number or size of objects.
func (w *Writer) AddStore(ctx context.Context, scratch, name string, enumerate func(func(Object, io.Reader) error) error) (_ Store, returnErr error) {
	if w.manifest.Storage == nil || w.stores[name] {
		return Store{}, fmt.Errorf("duplicate or undeclared snapshot store %q", name)
	}
	if err := validateStoreName(name); err != nil {
		return Store{}, err
	}
	w.stores[name] = true
	spool, err := os.CreateTemp(scratch, ".scenery-snapshot-manifest-*")
	if err != nil {
		return Store{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, spool.Close(), os.Remove(spool.Name())) }()
	encoder := json.NewEncoder(spool)
	record := Store{Name: name}
	err = enumerate(func(object Object, body io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if object.Store != name {
			return fmt.Errorf("snapshot store tuple mismatch")
		}
		if err := object.Validate(); err != nil {
			return err
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return err
		}
		if len(encoded)+1 > MaxObjectRecordBytes {
			return fmt.Errorf("snapshot object metadata exceeds record bound")
		}
		entry, err := w.entry(PayloadPath(object), zip.Deflate)
		if err != nil {
			return err
		}
		if err := copyVerified(ctx, entry, body, object.SizeBytes, object.SHA256); err != nil {
			return err
		}
		if err := encoder.Encode(object); err != nil {
			return err
		}
		if record.Bytes > int64(^uint64(0)>>1)-object.SizeBytes || record.Files == int64(^uint64(0)>>1) {
			return fmt.Errorf("snapshot counters overflow")
		}
		record.Files++
		record.Bytes += object.SizeBytes
		return nil
	})
	if err != nil {
		return Store{}, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return Store{}, err
	}
	entry, err := w.entry(StoreManifestPath(name), zip.Deflate)
	if err != nil {
		return Store{}, err
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(entry, h), contextReader{ctx: ctx, reader: spool})
	if err != nil {
		return Store{}, err
	}
	record.Manifest = File{Path: StoreManifestPath(name), Bytes: size, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))}
	w.manifest.Storage.Stores = append(w.manifest.Storage.Stores, record)
	return record, nil
}

func (w *Writer) Close() error {
	encoded, err := json.Marshal(w.manifest)
	if err != nil {
		return err
	}
	if len(encoded) > MaxManifestBytes {
		return fmt.Errorf("snapshot root manifest exceeds 1 MiB")
	}
	entry, err := w.entry("manifest.json", zip.Deflate)
	if err != nil {
		return err
	}
	if _, err := entry.Write(encoded); err != nil {
		return err
	}
	return w.zip.Close()
}

func (w *Writer) entry(name string, method uint16) (io.Writer, error) {
	if err := ValidatePath(name); err != nil {
		return nil, err
	}
	header := &zip.FileHeader{Name: name, Method: method}
	header.SetMode(0o600)
	return w.zip.CreateHeader(header)
}

func copyVerified(ctx context.Context, writer io.Writer, reader io.Reader, size int64, digest string) error {
	if reader == nil || size < 0 {
		return fmt.Errorf("invalid snapshot payload")
	}
	h := sha256.New()
	count, err := io.Copy(io.MultiWriter(writer, h), io.LimitReader(contextReader{ctx: ctx, reader: reader}, size))
	if err != nil {
		return err
	}
	if count != size {
		return io.ErrUnexpectedEOF
	}
	var extra [1]byte
	n, err := io.ReadFull(contextReader{ctx: ctx, reader: reader}, extra[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		return fmt.Errorf("snapshot payload length mismatch")
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("snapshot payload checksum mismatch")
	}
	return ctx.Err()
}

type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	if w.n > int64(^uint64(0)>>1)-int64(len(p)) {
		return 0, fmt.Errorf("snapshot size overflow")
	}
	w.n += int64(len(p))
	return len(p), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}
