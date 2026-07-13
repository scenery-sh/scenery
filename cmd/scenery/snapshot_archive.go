package main

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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

type snapshotArchive struct {
	reader   *zip.ReadCloser
	manifest snapshotManifest
	files    map[string]*zip.File
}

type snapshotCountingWriter struct {
	n int64
}

var snapshotNow = time.Now

func (w *snapshotCountingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func saveSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, opts snapshotSaveOptions) (_ snapshotSaveResult, returnErr error) {
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return snapshotSaveResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return snapshotSaveResult{}, err
	}
	manifest := snapshotManifest{
		Kind:           snapshotManifestKind,
		SchemaRevision: snapshotManifestSchemaRevision,
		CreatedAt:      snapshotNow().UTC(),
		App:            snapshotManifestApp{Name: cfg.Name, ID: cfg.AppID()},
		Files:          []snapshotManifestFile{},
	}
	result := snapshotSaveResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.save"),
		Archive:            output,
		App:                snapshotApp(cfg, appRoot),
	}

	var database postgresdb.Database
	var pgRunner snapshotPostgresRunner
	if opts.DB {
		database, err = resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
		if err != nil {
			return snapshotSaveResult{}, err
		}
		if database.Database == "" {
			return snapshotSaveResult{}, fmt.Errorf("snapshot save --db requires configured Postgres services")
		}
		pgRunner, err = snapshotPostgresRunnerFor(database)
		if err != nil {
			return snapshotSaveResult{}, err
		}
		dumpFile := path.Join("db", database.Database+".postgres.dump")
		manifest.DB = &snapshotManifestDB{Database: database.Database, Source: string(database.Source), Schemas: snapshotSchemas(database), DumpFile: dumpFile, DumpFormat: "pg_custom"}
		result.DB = &snapshotDBResult{Database: database.Database, Source: string(database.Source), Action: "saved"}
	}

	var storagePlan *storageCellPlan
	if opts.Storage {
		storagePlan, err = resolveStorageCellPlan(cfg, "")
		if err != nil {
			return snapshotSaveResult{}, err
		}
		if storagePlan == nil {
			return snapshotSaveResult{}, fmt.Errorf("snapshot save --storage requires stores configured in %s", cfg.SourcePath(appRoot))
		}
		manifest.Storage = &snapshotManifestStorage{CellID: storagePlan.StorageCellID, Stores: []snapshotManifestStore{}}
		result.Storage = &snapshotStorageResult{CellID: storagePlan.StorageCellID, CellRoot: storagePlan.CellRoot}
	}

	temporary, err := os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".tmp-*")
	if err != nil {
		return snapshotSaveResult{}, err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	archiveWriter := zip.NewWriter(temporary)

	if manifest.DB != nil {
		entry, err := createSnapshotZipEntry(archiveWriter, manifest.DB.DumpFile, zip.Store, 0o600)
		if err != nil {
			return snapshotSaveResult{}, err
		}
		file, err := writeSnapshotPayload(entry, func(writer io.Writer) error {
			return pgRunner.Dump(ctx, database, writer)
		})
		if err != nil {
			return snapshotSaveResult{}, err
		}
		file.Path = manifest.DB.DumpFile
		manifest.Files = append(manifest.Files, file)
		result.Files++
		result.Bytes += file.Bytes
	}

	if manifest.Storage != nil {
		stores := make([]string, 0, len(cfg.Storage.Stores))
		for store := range cfg.Storage.Stores {
			stores = append(stores, store)
		}
		sort.Strings(stores)
		for _, store := range stores {
			record, files, err := saveSnapshotStore(archiveWriter, storagePlan.storageStoreObjectsDir(store), store)
			if err != nil {
				return snapshotSaveResult{}, err
			}
			manifest.Storage.Stores = append(manifest.Storage.Stores, record)
			manifest.Files = append(manifest.Files, files...)
			result.Storage.Stores++
			result.Storage.Files += record.Files
			result.Storage.Bytes += record.Bytes
			result.Files += record.Files
			result.Bytes += record.Bytes
		}
	}

	manifestEntry, err := createSnapshotZipEntry(archiveWriter, "manifest.json", zip.Deflate, 0o600)
	if err != nil {
		return snapshotSaveResult{}, err
	}
	encodedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return snapshotSaveResult{}, err
	}
	if _, err := manifestEntry.Write(append(encodedManifest, '\n')); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := archiveWriter.Close(); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return snapshotSaveResult{}, err
	}
	if err := syncSnapshotDirectory(filepath.Dir(output)); err != nil {
		return snapshotSaveResult{}, err
	}
	return result, nil
}

func saveSnapshotStore(archive *zip.Writer, root, store string) (snapshotManifestStore, []snapshotManifestFile, error) {
	record := snapshotManifestStore{Name: store}
	files := []snapshotManifestFile{}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return record, files, nil
	} else if err != nil {
		return record, nil, err
	}
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot storage store %s contains symlink %s", store, filePath)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("snapshot storage store %s contains non-regular file %s", store, filePath)
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		name := path.Join("storage", store, filepath.ToSlash(relative))
		writer, err := createSnapshotZipEntry(archive, name, zip.Deflate, info.Mode().Perm())
		if err != nil {
			return err
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		manifestFile, copyErr := writeSnapshotPayload(writer, func(target io.Writer) error {
			_, err := io.Copy(target, file)
			return err
		})
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		manifestFile.Path = name
		files = append(files, manifestFile)
		record.Files++
		record.Bytes += manifestFile.Bytes
		return nil
	})
	return record, files, err
}

func createSnapshotZipEntry(archive *zip.Writer, name string, method uint16, mode os.FileMode) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: method}
	header.SetMode(mode)
	return archive.CreateHeader(header)
}

func writeSnapshotPayload(writer io.Writer, write func(io.Writer) error) (snapshotManifestFile, error) {
	hash := sha256.New()
	count := &snapshotCountingWriter{}
	if err := write(io.MultiWriter(writer, hash, count)); err != nil {
		return snapshotManifestFile{}, err
	}
	return snapshotManifestFile{Bytes: count.n, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}

func loadSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, opts snapshotLoadOptions) (snapshotLoadResult, error) {
	input, err := filepath.Abs(opts.Input)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	archive, err := openSnapshotArchive(input)
	if err != nil {
		return snapshotLoadResult{}, err
	}
	defer archive.reader.Close()
	if archive.manifest.App.ID != cfg.AppID() {
		return snapshotLoadResult{}, fmt.Errorf("snapshot app id %q does not match current app id %q", archive.manifest.App.ID, cfg.AppID())
	}
	if opts.DB && archive.manifest.DB == nil {
		return snapshotLoadResult{}, fmt.Errorf("snapshot does not contain a database section")
	}
	if opts.Storage && archive.manifest.Storage == nil {
		return snapshotLoadResult{}, fmt.Errorf("snapshot does not contain a storage section")
	}
	if err := rejectSnapshotLiveSession(ctx, appRoot); err != nil {
		return snapshotLoadResult{}, err
	}

	result := snapshotLoadResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.load"),
		Archive:            input,
		App:                snapshotApp(cfg, appRoot),
		Mode:               opts.Mode,
		DryRun:             opts.DryRun,
	}

	var targetName, targetSource string
	if opts.DB {
		if err := validateSnapshotSchemas(cfg, archive.manifest.DB.Schemas); err != nil {
			return snapshotLoadResult{}, err
		}
		targetName, targetSource, err = configuredSnapshotDatabaseTarget(appRoot, cfg)
		if err != nil {
			return snapshotLoadResult{}, err
		}
		if targetName == "" {
			return snapshotLoadResult{}, fmt.Errorf("snapshot load --db requires configured Postgres services")
		}
		if opts.Mode == "overwrite" && targetSource == string(postgresdb.SourceExternal) {
			return snapshotLoadResult{}, fmt.Errorf("refusing to overwrite external postgres database")
		}
		result.DB = &snapshotDBResult{Database: targetName, Source: targetSource, Action: opts.Mode}
	}

	var storagePlan *storageCellPlan
	if opts.Storage {
		storagePlan, err = resolveStorageCellPlan(cfg, "")
		if err != nil {
			return snapshotLoadResult{}, err
		}
		if storagePlan == nil {
			return snapshotLoadResult{}, fmt.Errorf("snapshot load --storage requires configured stores")
		}
		if err := validateSnapshotStores(cfg, archive.manifest.Storage.Stores); err != nil {
			return snapshotLoadResult{}, err
		}
		result.Storage = snapshotStorageResultFromManifest(storagePlan, archive.manifest.Storage)
		if opts.Mode == "merge" {
			conflicts, err := snapshotStorageConflicts(archive, storagePlan)
			if err != nil {
				return snapshotLoadResult{}, err
			}
			result.Storage.Conflicts = int64(len(conflicts))
			if opts.OnConflict == "fail" && len(conflicts) > 0 {
				return snapshotLoadResult{}, fmt.Errorf("snapshot storage merge found %d conflicts; use --on-conflict skip|overwrite", len(conflicts))
			}
		}
	}
	if opts.DryRun {
		return result, nil
	}

	if opts.DB {
		database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
		if err != nil {
			return snapshotLoadResult{}, err
		}
		if opts.Mode == "merge" {
			if err := requireSnapshotSchemas(ctx, database, archive.manifest.DB.Schemas); err != nil {
				return snapshotLoadResult{}, err
			}
		}
		if err := restoreSnapshotDatabase(ctx, archive, database, opts.Mode); err != nil {
			return snapshotLoadResult{}, err
		}
	}
	if opts.Storage {
		if err := applySnapshotStorage(archive, storagePlan, opts, result.Storage); err != nil {
			if opts.DB {
				return snapshotLoadResult{}, fmt.Errorf("database load completed but storage load failed; rerun with --storage only: %w", err)
			}
			return snapshotLoadResult{}, err
		}
	}
	return result, nil
}

func openSnapshotArchive(filePath string) (*snapshotArchive, error) {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	archive := &snapshotArchive{reader: reader, files: map[string]*zip.File{}}
	fail := func(err error) (*snapshotArchive, error) {
		_ = reader.Close()
		return nil, err
	}
	for _, file := range reader.File {
		if err := validateSnapshotArchivePath(file.Name); err != nil {
			return fail(err)
		}
		if _, exists := archive.files[file.Name]; exists {
			return fail(fmt.Errorf("snapshot contains duplicate entry %q", file.Name))
		}
		archive.files[file.Name] = file
	}
	manifestFile := archive.files["manifest.json"]
	if manifestFile == nil {
		return fail(fmt.Errorf("snapshot is missing manifest.json"))
	}
	if manifestFile.UncompressedSize64 > 1<<20 {
		return fail(fmt.Errorf("snapshot manifest exceeds 1 MiB"))
	}
	body, err := manifestFile.Open()
	if err != nil {
		return fail(err)
	}
	decoder := json.NewDecoder(io.LimitReader(body, 1<<20))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&archive.manifest)
	closeErr := body.Close()
	if err != nil {
		return fail(fmt.Errorf("decode snapshot manifest: %w", err))
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if archive.manifest.Kind != snapshotManifestKind || archive.manifest.SchemaRevision != snapshotManifestSchemaRevision {
		return fail(fmt.Errorf("unsupported snapshot manifest identity %q at %q", archive.manifest.Kind, archive.manifest.SchemaRevision))
	}
	if err := validateSnapshotManifestFiles(archive); err != nil {
		return fail(err)
	}
	return archive, nil
}

func validateSnapshotArchivePath(name string) error {
	if name == "" || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name || strings.HasSuffix(name, "/") {
		return fmt.Errorf("invalid snapshot archive path %q", name)
	}
	return nil
}

func validateSnapshotManifestFiles(archive *snapshotArchive) error {
	declared := map[string]snapshotManifestFile{}
	storeStats := map[string]snapshotManifestStore{}
	if archive.manifest.Storage != nil {
		for _, store := range archive.manifest.Storage.Stores {
			if !validSnapshotStoreName(store.Name) {
				return fmt.Errorf("snapshot manifest contains invalid store name %q", store.Name)
			}
			if _, exists := storeStats[store.Name]; exists {
				return fmt.Errorf("snapshot manifest contains duplicate store %q", store.Name)
			}
			storeStats[store.Name] = snapshotManifestStore{Name: store.Name}
		}
	}
	for _, file := range archive.manifest.Files {
		if _, exists := declared[file.Path]; exists {
			return fmt.Errorf("snapshot manifest contains duplicate file %q", file.Path)
		}
		if err := validateSnapshotManifestFilePath(archive.manifest, file.Path); err != nil {
			return err
		}
		if file.Bytes < 0 || !validSnapshotSHA256(file.SHA256) {
			return fmt.Errorf("snapshot manifest has invalid metadata for %q", file.Path)
		}
		entry := archive.files[file.Path]
		if entry == nil {
			return fmt.Errorf("snapshot payload %q is missing", file.Path)
		}
		if entry.UncompressedSize64 > uint64(^uint64(0)>>1) || int64(entry.UncompressedSize64) != file.Bytes {
			return fmt.Errorf("snapshot payload %q size mismatch", file.Path)
		}
		declared[file.Path] = file
		if store, _, ok := snapshotStoragePath(file.Path); ok {
			stats := storeStats[store]
			stats.Files++
			stats.Bytes += file.Bytes
			storeStats[store] = stats
		}
	}
	for name := range archive.files {
		if name != "manifest.json" {
			if _, ok := declared[name]; !ok {
				return fmt.Errorf("snapshot contains undeclared payload %q", name)
			}
		}
	}
	if archive.manifest.DB != nil {
		if archive.manifest.DB.DumpFormat != "pg_custom" {
			return fmt.Errorf("unsupported snapshot database dump format %q", archive.manifest.DB.DumpFormat)
		}
		if _, ok := declared[archive.manifest.DB.DumpFile]; !ok {
			return fmt.Errorf("snapshot database dump %q is not declared", archive.manifest.DB.DumpFile)
		}
	}
	if archive.manifest.Storage != nil {
		for _, want := range archive.manifest.Storage.Stores {
			got := storeStats[want.Name]
			if got.Files != want.Files || got.Bytes != want.Bytes {
				return fmt.Errorf("snapshot store %q counts do not match its payload", want.Name)
			}
		}
	}
	for _, file := range archive.manifest.Files {
		if err := verifySnapshotZipFile(archive.files[file.Path], file); err != nil {
			return err
		}
	}
	return nil
}

func validSnapshotStoreName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\") && path.Clean(name) == name
}

func validateSnapshotManifestFilePath(manifest snapshotManifest, filePath string) error {
	if err := validateSnapshotArchivePath(filePath); err != nil {
		return err
	}
	if manifest.DB != nil && filePath == manifest.DB.DumpFile && strings.HasPrefix(filePath, "db/") {
		return nil
	}
	store, relative, ok := snapshotStoragePath(filePath)
	if !ok || relative == "" || manifest.Storage == nil {
		return fmt.Errorf("snapshot payload path %q is not declared by a section", filePath)
	}
	for _, declared := range manifest.Storage.Stores {
		if store == declared.Name {
			return nil
		}
	}
	return fmt.Errorf("snapshot payload path %q names undeclared store %q", filePath, store)
}

func snapshotStoragePath(filePath string) (string, string, bool) {
	rest, ok := strings.CutPrefix(filePath, "storage/")
	if !ok {
		return "", "", false
	}
	store, relative, ok := strings.Cut(rest, "/")
	return store, relative, ok && store != "" && relative != ""
}

func validSnapshotSHA256(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func verifySnapshotZipFile(file *zip.File, manifest snapshotManifestFile) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, reader)
	closeErr := reader.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	got := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if written != manifest.Bytes || got != manifest.SHA256 {
		return fmt.Errorf("snapshot checksum mismatch for %q", manifest.Path)
	}
	return nil
}

func validateSnapshotSchemas(cfg appcfg.Config, schemas []snapshotManifestSchema) error {
	configured := map[string]string{}
	for _, schema := range cfg.DatabaseServices() {
		configured[schema.Name] = schema.Schema
	}
	for _, schema := range schemas {
		if configured[schema.Service] != schema.Schema {
			return fmt.Errorf("snapshot database schema %s=%s is not configured in the current app", schema.Service, schema.Schema)
		}
	}
	return nil
}

func validateSnapshotStores(cfg appcfg.Config, stores []snapshotManifestStore) error {
	for _, store := range stores {
		if _, ok := cfg.Storage.Stores[store.Name]; !ok {
			return fmt.Errorf("snapshot storage store %q is not configured in the current app", store.Name)
		}
	}
	return nil
}

func configuredSnapshotDatabaseTarget(appRoot string, cfg appcfg.Config) (string, string, error) {
	if len(cfg.DatabaseServices()) == 0 {
		return "", "", nil
	}
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return "", "", err
	}
	if value, _ := lookupEnvValue(env, appDatabaseURLEnv); strings.TrimSpace(value) != "" {
		if _, err := postgresdb.ParseURL(value); err != nil {
			return "", "", err
		}
		return postgresdb.DatabaseNameFromURL(value), string(postgresdb.SourceExternal), nil
	}
	return postgresname.DatabaseNameFor(cfg.AppID(), appRoot), string(postgresdb.SourceManaged), nil
}

func rejectSnapshotLiveSession(ctx context.Context, appRoot string) error {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.SocketPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	client := localagent.NewClient(paths.SocketPath)
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return fmt.Errorf("cannot verify snapshot load safety against the local agent: %w", err)
	}
	for _, session := range sessions {
		if sessionOwnerLive(session) {
			return fmt.Errorf("snapshot load requires the live dev runtime to stop first; run `scenery down --app-root %s`", appRoot)
		}
	}
	return nil
}

func requireSnapshotSchemas(ctx context.Context, database postgresdb.Database, schemas []snapshotManifestSchema) error {
	db, err := openPostgresDatabase(ctx, database.URL)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, schema := range schemas {
		var exists bool
		if err := db.QueryRowContext(ctx, `select exists(select 1 from pg_namespace where nspname = $1)`, schema.Schema).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("snapshot merge requires existing schema %q; run `scenery db setup` first", schema.Schema)
		}
	}
	return nil
}

func restoreSnapshotDatabase(ctx context.Context, archive *snapshotArchive, database postgresdb.Database, mode string) error {
	if mode == "overwrite" {
		admin, err := managedPostgresAdmin(ctx)
		if err != nil {
			return err
		}
		if err := postgresdb.DropDatabase(ctx, admin, database.Database); err != nil {
			_ = admin.Close()
			return err
		}
		if err := postgresdb.EnsureDatabase(ctx, admin, database.Database); err != nil {
			_ = admin.Close()
			return err
		}
		if err := admin.Close(); err != nil {
			return err
		}
	}
	runner, err := snapshotPostgresRunnerFor(database)
	if err != nil {
		return err
	}
	dump, err := archive.files[archive.manifest.DB.DumpFile].Open()
	if err != nil {
		return err
	}
	defer dump.Close()
	flags := []string{"--exit-on-error"}
	if mode == "merge" {
		flags = append(flags, "--data-only", "--single-transaction")
	}
	if err := runner.Restore(ctx, database, flags, dump); err != nil {
		return fmt.Errorf("restore snapshot database failed; rerun the same load to recover: %w", err)
	}
	return nil
}

func snapshotStorageResultFromManifest(plan *storageCellPlan, storage *snapshotManifestStorage) *snapshotStorageResult {
	result := &snapshotStorageResult{CellID: plan.StorageCellID, CellRoot: plan.CellRoot, Stores: len(storage.Stores)}
	for _, store := range storage.Stores {
		result.Files += store.Files
		result.Bytes += store.Bytes
	}
	return result
}

func snapshotStorageConflicts(archive *snapshotArchive, plan *storageCellPlan) ([]string, error) {
	var conflicts []string
	for _, file := range archive.manifest.Files {
		store, relative, ok := snapshotStoragePath(file.Path)
		if !ok {
			continue
		}
		target := filepath.Join(plan.storageStoreObjectsDir(store), filepath.FromSlash(relative))
		if _, err := os.Stat(target); err == nil {
			conflicts = append(conflicts, file.Path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func applySnapshotStorage(archive *snapshotArchive, plan *storageCellPlan, opts snapshotLoadOptions, result *snapshotStorageResult) error {
	if err := os.MkdirAll(plan.ObjectsDir, 0o755); err != nil {
		return err
	}
	if opts.Mode == "merge" {
		return mergeSnapshotStorage(archive, plan, opts.OnConflict, result)
	}
	for _, store := range archive.manifest.Storage.Stores {
		if err := recoverSnapshotStoreSwap(plan, store.Name); err != nil {
			return err
		}
	}
	for _, store := range archive.manifest.Storage.Stores {
		if err := stageSnapshotStore(archive, plan, store.Name); err != nil {
			return err
		}
	}
	for _, store := range archive.manifest.Storage.Stores {
		if err := swapSnapshotStore(plan, store.Name); err != nil {
			return err
		}
	}
	return nil
}

func snapshotStoreSwapPaths(plan *storageCellPlan, store string) (target, stage, trash string) {
	target = plan.storageStoreObjectsDir(store)
	stage = filepath.Join(plan.ObjectsDir, "."+store+".snapshot-stage")
	trash = filepath.Join(plan.ObjectsDir, "."+store+".snapshot-trash")
	return
}

func recoverSnapshotStoreSwap(plan *storageCellPlan, store string) error {
	target, stage, trash := snapshotStoreSwapPaths(plan, store)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if _, err := os.Stat(trash); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return os.RemoveAll(trash)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(trash, target)
}

func stageSnapshotStore(archive *snapshotArchive, plan *storageCellPlan, store string) error {
	_, stage, _ := snapshotStoreSwapPaths(plan, store)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	for _, file := range archive.manifest.Files {
		entryStore, relative, ok := snapshotStoragePath(file.Path)
		if !ok || entryStore != store {
			continue
		}
		if err := writeSnapshotZipFile(archive.files[file.Path], filepath.Join(stage, filepath.FromSlash(relative)), false); err != nil {
			return err
		}
	}
	return syncSnapshotDirectory(stage)
}

func swapSnapshotStore(plan *storageCellPlan, store string) error {
	target, stage, trash := snapshotStoreSwapPaths(plan, store)
	if err := os.RemoveAll(trash); err != nil {
		return err
	}
	targetExists := false
	if _, err := os.Stat(target); err == nil {
		targetExists = true
		if err := os.Rename(target, trash); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		if targetExists {
			_ = os.Rename(trash, target)
		}
		return err
	}
	if err := syncSnapshotDirectory(plan.ObjectsDir); err != nil {
		return err
	}
	return os.RemoveAll(trash)
}

func mergeSnapshotStorage(archive *snapshotArchive, plan *storageCellPlan, conflictPolicy string, result *snapshotStorageResult) error {
	for _, file := range archive.manifest.Files {
		store, relative, ok := snapshotStoragePath(file.Path)
		if !ok {
			continue
		}
		target := filepath.Join(plan.storageStoreObjectsDir(store), filepath.FromSlash(relative))
		_, statErr := os.Stat(target)
		exists := statErr == nil
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if exists && conflictPolicy == "skip" {
			result.Skipped++
			continue
		}
		if exists && conflictPolicy == "overwrite" {
			result.Overwritten++
		}
		if err := writeSnapshotZipFile(archive.files[file.Path], target, true); err != nil {
			return err
		}
	}
	return nil
}

func writeSnapshotZipFile(file *zip.File, target string, atomic bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	writePath := target
	var output *os.File
	if atomic {
		output, err = os.CreateTemp(filepath.Dir(target), ".snapshot-load-*")
		if err == nil {
			writePath = output.Name()
		}
	} else {
		output, err = os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode().Perm())
	}
	if err != nil {
		return err
	}
	removeTemporary := atomic
	defer func() {
		_ = output.Close()
		if removeTemporary {
			_ = os.Remove(writePath)
		}
	}()
	if _, err := io.Copy(output, reader); err != nil {
		return err
	}
	if err := output.Chmod(file.Mode().Perm()); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if atomic {
		if err := os.Rename(writePath, target); err != nil {
			return err
		}
		removeTemporary = false
		return syncSnapshotDirectory(filepath.Dir(target))
	}
	return nil
}

func syncSnapshotDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
