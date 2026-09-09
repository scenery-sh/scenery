package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/snapshotarchive"
)

const metadataPrefix = "__scenery/metadata/"
const tenantPrefix = "__scenery/tenants/"

type legacyFile struct {
	io.ReadCloser
	Size     int64
	Modified time.Time
}
type legacySource interface {
	Manifest() snapshotarchive.Manifest
	Stores() []string
	Walk(context.Context, string, func(string) error) error
	Open(string, string) (*legacyFile, error)
	CopyDatabase(context.Context, io.Writer) error
	Close() error
}
type sidecar struct {
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func strictJSON(data []byte, value any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing legacy JSON data")
	}
	return nil
}

func logicalObject(store, physical string, scoped bool) (snapshotarchive.Object, error) {
	object := snapshotarchive.Object{Store: store, Key: physical}
	if scoped {
		if !strings.HasPrefix(physical, tenantPrefix) {
			return object, fmt.Errorf("tenant store %q has an unscoped or ambiguous legacy key %q", store, physical)
		}
		encoded, key, ok := strings.Cut(strings.TrimPrefix(physical, tenantPrefix), "/")
		tenant, err := base64.RawURLEncoding.DecodeString(encoded)
		if !ok || err != nil || len(tenant) == 0 || base64.RawURLEncoding.EncodeToString(tenant) != encoded {
			return object, fmt.Errorf("invalid legacy tenant prefix")
		}
		object.Tenant, object.Key = string(tenant), key
	} else if strings.HasPrefix(physical, "__scenery/") {
		return object, fmt.Errorf("ambiguous reserved key %q in store %q; declare a known --tenant-store or recover the original logical data", physical, store)
	}
	for _, part := range strings.Split(object.Key, "/") {
		if strings.HasPrefix(part, ".scenery-put-") || strings.HasPrefix(part, ".scenery-meta-") || part == "__scenery" {
			return object, fmt.Errorf("ambiguous reserved legacy key %q", physical)
		}
	}
	return object, nil
}

func exportStore(ctx context.Context, source legacySource, store string, scoped bool, visit func(snapshotarchive.Object, io.Reader) error) error {
	return source.Walk(ctx, store, func(physical string) error {
		if strings.HasPrefix(physical, metadataPrefix) {
			key, ok := strings.CutSuffix(strings.TrimPrefix(physical, metadataPrefix), ".json")
			if !ok || key == "" {
				return fmt.Errorf("malformed sidecar path %q", physical)
			}
			if _, err := logicalObject(store, key, scoped); err != nil {
				return err
			}
			payload, err := source.Open(store, key)
			if err != nil {
				return fmt.Errorf("orphaned legacy sidecar %q: %w", physical, err)
			}
			return payload.Close()
		}
		object, err := logicalObject(store, physical, scoped)
		if err != nil {
			return err
		}
		meta, err := readSidecar(source, store, physical)
		if err != nil {
			return err
		}
		payload, err := source.Open(store, physical)
		if err != nil {
			return err
		}
		defer func() { _ = payload.Close() }()
		if payload.Modified.IsZero() {
			return fmt.Errorf("legacy payload %q has no recoverable modification time; export the original directory instead", physical)
		}
		object.SizeBytes, object.ModifiedAt = payload.Size, payload.Modified.UTC()
		object.ContentType, object.Metadata = meta.ContentType, meta.Metadata
		hash := sha256.New()
		n, err := copyContext(ctx, hash, payload, payload.Size)
		if err != nil {
			return err
		}
		if n != payload.Size {
			return fmt.Errorf("legacy payload length changed")
		}
		object.SHA256 = hex.EncodeToString(hash.Sum(nil))
		if err := object.Validate(); err != nil {
			return err
		}
		if err := payload.Close(); err != nil {
			return err
		}
		body, err := source.Open(store, physical)
		if err != nil {
			return err
		}
		// The output writer verifies size and digest again while streaming. A
		// quiescence violation cannot publish a different valid-looking payload.
		err = visit(object, body)
		return errors.Join(err, body.Close())
	})
}

func readSidecar(source legacySource, store, physical string) (sidecar, error) {
	f, err := source.Open(store, metadataPrefix+physical+".json")
	if err != nil {
		return sidecar{}, fmt.Errorf("missing metadata for legacy payload %q; refusing to invent lost metadata: %w", physical, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, snapshotarchive.MaxObjectRecordBytes+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return sidecar{}, err
	}
	if len(data) > snapshotarchive.MaxObjectRecordBytes {
		return sidecar{}, fmt.Errorf("oversized legacy sidecar")
	}
	var meta sidecar
	if err := strictJSON(data, &meta); err != nil {
		return sidecar{}, fmt.Errorf("malformed legacy sidecar: %w", err)
	}
	return meta, nil
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func copyContext(ctx context.Context, writer io.Writer, reader io.Reader, size int64) (int64, error) {
	if size < 0 {
		return 0, fmt.Errorf("invalid legacy length")
	}
	n, err := io.Copy(writer, io.LimitReader(cancelReader{ctx, reader}, size))
	if err != nil {
		return n, err
	}
	var extra [1]byte
	if count, err := reader.Read(extra[:]); count != 0 || !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("legacy payload length differs")
	}
	return n, ctx.Err()
}
