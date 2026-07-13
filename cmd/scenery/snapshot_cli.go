package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/postgresdb"
)

const (
	snapshotManifestKind           = "scenery.snapshot.manifest"
	snapshotManifestSchemaRevision = "sha256:83cc59388d47510203407af1dd68d22fcad86d95add34d9dfb4cabcd56b54792"
)

type snapshotSaveOptions struct {
	AppRoot string
	Output  string
	DB      bool
	Storage bool
	JSON    bool
}

type snapshotLoadOptions struct {
	AppRoot    string
	Input      string
	DB         bool
	Storage    bool
	Mode       string
	OnConflict string
	Yes        bool
	DryRun     bool
	JSON       bool
}

type snapshotVerifyOptions struct {
	Input string
	JSON  bool
}

type snapshotManifest struct {
	Kind           string                   `json:"kind"`
	SchemaRevision string                   `json:"schema_revision"`
	CreatedAt      time.Time                `json:"created_at"`
	App            snapshotManifestApp      `json:"app"`
	DB             *snapshotManifestDB      `json:"db,omitempty"`
	Storage        *snapshotManifestStorage `json:"storage,omitempty"`
	Files          []snapshotManifestFile   `json:"files"`
}

type snapshotManifestApp struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type snapshotManifestDB struct {
	Database   string                   `json:"database"`
	Source     string                   `json:"source"`
	Schemas    []snapshotManifestSchema `json:"schemas"`
	DumpFile   string                   `json:"dump_file"`
	DumpFormat string                   `json:"dump_format"`
}

type snapshotManifestSchema struct {
	Service string `json:"service"`
	Schema  string `json:"schema"`
}

type snapshotManifestStorage struct {
	CellID string                  `json:"cell_id"`
	Stores []snapshotManifestStore `json:"stores"`
}

type snapshotManifestStore struct {
	Name  string `json:"name"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

type snapshotManifestFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type snapshotAppResult struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Root string `json:"root"`
}

type snapshotDBResult struct {
	Database string `json:"database"`
	Source   string `json:"source"`
	Action   string `json:"action,omitempty"`
}

type snapshotStorageResult struct {
	CellID      string `json:"cell_id"`
	CellRoot    string `json:"cell_root"`
	Stores      int    `json:"stores"`
	Files       int64  `json:"files"`
	Bytes       int64  `json:"bytes"`
	Conflicts   int64  `json:"conflicts,omitempty"`
	Skipped     int64  `json:"skipped,omitempty"`
	Overwritten int64  `json:"overwritten,omitempty"`
}

type snapshotSaveResult struct {
	cliPayloadIdentity
	Archive string                 `json:"archive"`
	App     snapshotAppResult      `json:"app"`
	DB      *snapshotDBResult      `json:"db,omitempty"`
	Storage *snapshotStorageResult `json:"storage,omitempty"`
	Files   int64                  `json:"files"`
	Bytes   int64                  `json:"bytes"`
}

type snapshotLoadResult struct {
	cliPayloadIdentity
	Archive string                 `json:"archive"`
	App     snapshotAppResult      `json:"app"`
	Mode    string                 `json:"mode"`
	DryRun  bool                   `json:"dry_run"`
	DB      *snapshotDBResult      `json:"db,omitempty"`
	Storage *snapshotStorageResult `json:"storage,omitempty"`
}

type snapshotVerifyResult struct {
	cliPayloadIdentity
	Archive   string              `json:"archive"`
	App       snapshotManifestApp `json:"app"`
	CreatedAt time.Time           `json:"created_at"`
	Files     int64               `json:"files"`
	Bytes     int64               `json:"bytes"`
	DB        bool                `json:"db"`
	Storage   bool                `json:"storage"`
}

func snapshotCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery snapshot save|verify|load [flags]")
	}
	switch args[0] {
	case "save":
		return runSnapshotSave(context.Background(), os.Stdout, args[1:])
	case "load":
		return runSnapshotLoad(context.Background(), os.Stdout, args[1:])
	case "verify":
		return runSnapshotVerify(os.Stdout, args[1:])
	default:
		return fmt.Errorf("unknown snapshot command %q", args[0])
	}
}

func parseSnapshotVerifyArgs(args []string) (snapshotVerifyOptions, error) {
	var opts snapshotVerifyOptions
	flags := newCLIFlagSet("snapshot verify")
	flags.StringVar(&opts.Input, "input", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return snapshotVerifyOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return snapshotVerifyOptions{}, err
	}
	if strings.TrimSpace(opts.Input) == "" {
		return snapshotVerifyOptions{}, fmt.Errorf("snapshot verify requires --input <file.zip>")
	}
	return opts, nil
}

func parseSnapshotSaveArgs(args []string) (snapshotSaveOptions, error) {
	var opts snapshotSaveOptions
	flags := newCLIFlagSet("snapshot save")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "output", "", "")
	flags.BoolVar(&opts.DB, "db", false, "")
	flags.BoolVar(&opts.Storage, "storage", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return snapshotSaveOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return snapshotSaveOptions{}, err
	}
	if !opts.DB && !opts.Storage {
		return snapshotSaveOptions{}, fmt.Errorf("snapshot save requires --db and/or --storage")
	}
	if strings.TrimSpace(opts.Output) == "" {
		return snapshotSaveOptions{}, fmt.Errorf("snapshot save requires --output <file.zip>")
	}
	if !strings.EqualFold(filepath.Ext(opts.Output), ".zip") {
		return snapshotSaveOptions{}, fmt.Errorf("snapshot output must end in .zip")
	}
	return opts, nil
}

func parseSnapshotLoadArgs(args []string) (snapshotLoadOptions, error) {
	var opts snapshotLoadOptions
	flags := newCLIFlagSet("snapshot load")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Input, "input", "", "")
	flags.BoolVar(&opts.DB, "db", false, "")
	flags.BoolVar(&opts.Storage, "storage", false, "")
	flags.StringVar(&opts.Mode, "mode", "", "")
	flags.StringVar(&opts.OnConflict, "on-conflict", "", "")
	flags.BoolVar(&opts.Yes, "yes", false, "")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return snapshotLoadOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return snapshotLoadOptions{}, err
	}
	if !opts.DB && !opts.Storage {
		return snapshotLoadOptions{}, fmt.Errorf("snapshot load requires --db and/or --storage")
	}
	if strings.TrimSpace(opts.Input) == "" {
		return snapshotLoadOptions{}, fmt.Errorf("snapshot load requires --input <file.zip>")
	}
	if opts.Mode != "overwrite" && opts.Mode != "merge" {
		return snapshotLoadOptions{}, fmt.Errorf("snapshot load requires --mode overwrite|merge")
	}
	if opts.Mode == "overwrite" && !opts.Yes {
		return snapshotLoadOptions{}, fmt.Errorf("snapshot overwrite requires --yes")
	}
	onConflictSet := cliFlagSet(flags, "on-conflict")
	if onConflictSet && (opts.Mode != "merge" || !opts.Storage) {
		return snapshotLoadOptions{}, fmt.Errorf("--on-conflict is valid only with --mode merge --storage")
	}
	if opts.Mode == "merge" && opts.Storage && opts.OnConflict == "" {
		opts.OnConflict = "fail"
	}
	if opts.OnConflict != "" && opts.OnConflict != "fail" && opts.OnConflict != "skip" && opts.OnConflict != "overwrite" {
		return snapshotLoadOptions{}, fmt.Errorf("--on-conflict must be fail, skip, or overwrite")
	}
	if opts.OnConflict == "overwrite" && !opts.Yes {
		return snapshotLoadOptions{}, fmt.Errorf("snapshot merge --on-conflict overwrite requires --yes")
	}
	return opts, nil
}

func runSnapshotSave(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseSnapshotSaveArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := saveSnapshot(ctx, appRoot, cfg, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "saved snapshot %s (%d files, %d bytes)\n", result.Archive, result.Files, result.Bytes)
	return nil
}

func runSnapshotLoad(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseSnapshotLoadArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := loadSnapshot(ctx, appRoot, cfg, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	if result.DryRun {
		fmt.Fprintf(stdout, "snapshot load preflight passed for %s\n", result.Archive)
	} else {
		fmt.Fprintf(stdout, "loaded snapshot %s\n", result.Archive)
	}
	return nil
}

func runSnapshotVerify(stdout io.Writer, args []string) error {
	opts, err := parseSnapshotVerifyArgs(args)
	if err != nil {
		return err
	}
	result, err := verifySnapshot(opts.Input)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "verified snapshot %s (%d files, %d bytes)\n", result.Archive, result.Files, result.Bytes)
	return nil
}

func snapshotApp(cfg appcfg.Config, root string) snapshotAppResult {
	return snapshotAppResult{Name: cfg.Name, ID: cfg.AppID(), Root: root}
}

func snapshotSchemas(database postgresdb.Database) []snapshotManifestSchema {
	out := make([]snapshotManifestSchema, 0, len(database.Schemas))
	for _, schema := range database.Schemas {
		out = append(out, snapshotManifestSchema{Service: schema.Name, Schema: schema.Schema})
	}
	return out
}
