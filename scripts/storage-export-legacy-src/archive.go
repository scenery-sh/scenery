package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"scenery.sh/internal/snapshotarchive"
)

const legacyRevision = "sha256:83cc59388d47510203407af1dd68d22fcad86d95add34d9dfb4cabcd56b54792"

type legacyManifest struct {
	Kind           string                    `json:"kind"`
	SchemaRevision string                    `json:"schema_revision"`
	CreatedAt      time.Time                 `json:"created_at"`
	App            snapshotarchive.App       `json:"app"`
	DB             *snapshotarchive.Database `json:"db,omitempty"`
	Storage        *struct {
		CellID string `json:"cell_id"`
		Stores []struct {
			Name  string `json:"name"`
			Files int64  `json:"files"`
			Bytes int64  `json:"bytes"`
		} `json:"stores"`
	} `json:"storage,omitempty"`
	Files []snapshotarchive.File `json:"files"`
}
type archiveSource struct {
	zip      *zip.ReadCloser
	entries  map[string]*zip.File
	manifest snapshotarchive.Manifest
	stores   []string
	database string
}

func openLegacyArchive(ctx context.Context, name string) (*archiveSource, error) {
	z, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	s := &archiveSource{zip: z, entries: map[string]*zip.File{}}
	fail := func(err error) (*archiveSource, error) { _ = z.Close(); return nil, err }
	for _, file := range z.File {
		if err := snapshotarchive.ValidatePath(file.Name); err != nil {
			return fail(err)
		}
		if !file.Mode().IsRegular() || s.entries[file.Name] != nil {
			return fail(fmt.Errorf("duplicate or unsafe legacy ZIP entry %q", file.Name))
		}
		s.entries[file.Name] = file
	}
	root := s.entries["manifest.json"]
	if root == nil || root.UncompressedSize64 > snapshotarchive.MaxManifestBytes {
		return fail(fmt.Errorf("missing or oversized legacy manifest"))
	}
	f, err := root.Open()
	if err != nil {
		return fail(err)
	}
	data, err := io.ReadAll(io.LimitReader(f, snapshotarchive.MaxManifestBytes+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return fail(err)
	}
	var old legacyManifest
	if err := strictJSON(data, &old); err != nil {
		return fail(err)
	}
	if old.Kind != snapshotarchive.Kind || old.SchemaRevision != legacyRevision || old.App.ID == "" || old.App.Name == "" || old.CreatedAt.IsZero() || (old.DB == nil && old.Storage == nil) {
		return fail(fmt.Errorf("unsupported legacy snapshot manifest"))
	}
	s.manifest = snapshotarchive.Manifest{App: old.App, DB: old.DB}
	declared := map[string]bool{"manifest.json": true}
	stores := map[string]bool{}
	if old.Storage != nil {
		s.manifest.Storage = &snapshotarchive.Storage{}
		for _, store := range old.Storage.Stores {
			if store.Name == "" || strings.ContainsAny(store.Name, "/\\") || stores[store.Name] || store.Files < 0 || store.Bytes < 0 {
				return fail(fmt.Errorf("invalid legacy store"))
			}
			stores[store.Name] = true
			s.stores = append(s.stores, store.Name)
		}
	}
	if old.DB != nil {
		if old.DB.DumpFormat != "pg_custom" || !strings.HasPrefix(old.DB.DumpFile, "db/") {
			return fail(fmt.Errorf("unsupported legacy database"))
		}
		s.database = old.DB.DumpFile
		copy := *old.DB
		copy.DumpFile = "db/database.postgres.dump"
		s.manifest.DB = &copy
	}
	totals := map[string][2]int64{}
	for _, file := range old.Files {
		entry := s.entries[file.Path]
		if declared[file.Path] || entry == nil || file.Bytes < 0 || uint64(file.Bytes) != entry.UncompressedSize64 || !snapshotarchive.ValidSHA256(file.SHA256) {
			return fail(fmt.Errorf("invalid legacy file declaration"))
		}
		declared[file.Path] = true
		if file.Path != s.database {
			store, _, ok := strings.Cut(strings.TrimPrefix(file.Path, "storage/"), "/")
			if !strings.HasPrefix(file.Path, "storage/") || !ok || !stores[store] {
				return fail(fmt.Errorf("undeclared legacy file %q", file.Path))
			}
			total := totals[store]
			if total[1] > int64(^uint64(0)>>1)-file.Bytes {
				return fail(fmt.Errorf("legacy inventory overflows"))
			}
			totals[store] = [2]int64{total[0] + 1, total[1] + file.Bytes}
		}
		body, err := entry.Open()
		if err != nil {
			return fail(err)
		}
		hash := sha256.New()
		count, err := copyContext(ctx, hash, body, file.Bytes)
		if err := errors.Join(err, body.Close()); err != nil {
			return fail(err)
		}
		if count != file.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
			return fail(fmt.Errorf("legacy file checksum differs"))
		}
	}
	for name := range s.entries {
		if !declared[name] {
			return fail(fmt.Errorf("undeclared legacy ZIP entry %q", name))
		}
	}
	if s.database != "" && !declared[s.database] {
		return fail(fmt.Errorf("legacy database dump is missing"))
	}
	if old.Storage != nil {
		for _, store := range old.Storage.Stores {
			if totals[store.Name] != [2]int64{store.Files, store.Bytes} {
				return fail(fmt.Errorf("legacy store totals differ"))
			}
		}
	}
	return s, nil
}

func (s *archiveSource) Manifest() snapshotarchive.Manifest { return s.manifest }
func (s *archiveSource) Stores() []string                   { return s.stores }
func (s *archiveSource) Close() error                       { return s.zip.Close() }
func (s *archiveSource) Open(store, key string) (*legacyFile, error) {
	file := s.entries["storage/"+store+"/"+key]
	if file == nil {
		return nil, fmt.Errorf("legacy entry missing")
	}
	body, err := file.Open()
	if err != nil {
		return nil, err
	}
	modified := file.Modified
	if file.ModifiedDate == 0 && file.ModifiedTime == 0 {
		modified = time.Time{}
	}
	return &legacyFile{ReadCloser: body, Size: int64(file.UncompressedSize64), Modified: modified}, nil
}
func (s *archiveSource) Walk(ctx context.Context, store string, visit func(string) error) error {
	prefix := "storage/" + store + "/"
	for _, file := range s.zip.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(file.Name, prefix) {
			if err := visit(strings.TrimPrefix(file.Name, prefix)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *archiveSource) CopyDatabase(ctx context.Context, writer io.Writer) error {
	entry := s.entries[s.database]
	if entry == nil {
		return fmt.Errorf("legacy database missing")
	}
	body, err := entry.Open()
	if err != nil {
		return err
	}
	_, err = copyContext(ctx, writer, body, int64(entry.UncompressedSize64))
	return errors.Join(err, body.Close())
}
