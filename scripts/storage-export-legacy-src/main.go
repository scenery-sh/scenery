// storage-export-legacy is an explicit read-only converter, never a runtime
// backend or automatic migration path. It has no namespace allocation access.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/snapshotarchive"
)

type storeFlags map[string]bool

func (s storeFlags) String() string { return "" }
func (s storeFlags) Set(value string) error {
	if value == "" || strings.ContainsAny(value, "/\\") || s[value] {
		return fmt.Errorf("invalid or repeated tenant store %q", value)
	}
	s[value] = true
	return nil
}

type options struct {
	source, output, appID, appName string
	quiesced, dryRun               bool
	tenantStores                   storeFlags
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "legacy export:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	opts := options{tenantStores: storeFlags{}}
	var input string
	flags := flag.NewFlagSet("storage-export-legacy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.source, "source", "", "legacy cell directory")
	flags.StringVar(&input, "input", "", "legacy ZIP archive")
	flags.StringVar(&opts.output, "output", "", "new logical ZIP archive (must not exist)")
	flags.StringVar(&opts.appID, "app-id", "", "exact application ID")
	flags.StringVar(&opts.appName, "app-name", "", "application display name")
	flags.BoolVar(&opts.quiesced, "confirm-source-quiesced", false, "confirm every legacy writer is stopped")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "validate source without publishing an archive")
	flags.Var(opts.tenantStores, "tenant-store", "tenant-scoped store; repeat for each known store")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (opts.source == "") == (input == "") || (!opts.dryRun && opts.output == "") || (opts.dryRun && opts.output != "") {
		return fmt.Errorf("select exactly one of --source <cell> or --input <old.zip>, and either --dry-run or --output <new.zip>; directory publication requires --confirm-source-quiesced")
	}
	if input != "" {
		info, err := os.Lstat(input)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("--input requires a regular archive")
		}
		opts.source = input
	} else if info, err := os.Lstat(opts.source); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("--source requires a cell directory")
	}
	return export(ctx, opts, stdout)
}

func export(ctx context.Context, opts options, stdout io.Writer) (returnErr error) {
	sourcePath, err := filepath.Abs(opts.source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("legacy source must not be a symlink")
	}
	sourcePath, err = filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return err
	}
	var source legacySource
	if info.IsDir() {
		if !opts.quiesced && !opts.dryRun {
			return fmt.Errorf("directory export requires --confirm-source-quiesced after stopping ALL legacy writers; no process will be stopped automatically")
		}
		if opts.appID == "" {
			return fmt.Errorf("directory export requires --app-id")
		}
		if opts.appName == "" {
			opts.appName = opts.appID
		}
		source, err = openDirectory(sourcePath)
	} else if info.Mode().IsRegular() {
		source, err = openLegacyArchive(ctx, sourcePath)
	} else {
		return fmt.Errorf("legacy source must be a regular archive or cell directory")
	}
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, source.Close()) }()
	manifest := source.Manifest()
	if info.IsDir() {
		manifest.App = snapshotarchive.App{ID: opts.appID, Name: opts.appName}
	}
	if opts.appID != "" && opts.appID != manifest.App.ID {
		return fmt.Errorf("archive app ID differs from --app-id")
	}
	if opts.appName != "" && opts.appName != manifest.App.Name {
		return fmt.Errorf("archive app name differs from --app-name")
	}
	manifest.CreatedAt = time.Now().UTC()
	for store := range opts.tenantStores {
		found := false
		for _, name := range source.Stores() {
			if name == store {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("tenant store %q is not present in source", store)
		}
	}
	if opts.dryRun {
		var objects int
		for _, store := range source.Stores() {
			if err := exportStore(ctx, source, store, opts.tenantStores[store], func(_ snapshotarchive.Object, body io.Reader) error {
				objects++
				_, err := io.Copy(io.Discard, body)
				return err
			}); err != nil {
				return err
			}
		}
		if manifest.DB != nil {
			if err := source.CopyDatabase(ctx, io.Discard); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(stdout, "Validated %d objects; no output written. Dry-run is not a quiescence or snapshot consistency proof.\n", objects)
		return err
	}
	output, err := filepath.Abs(opts.output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		return err
	}
	output = filepath.Join(parent, filepath.Base(output))
	if output == sourcePath || (info.IsDir() && within(sourcePath, output)) {
		return fmt.Errorf("output must be outside the read-only source")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("output must not exist: %s", output)
	}
	tmp, err := os.CreateTemp(parent, ".storage-export-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		if err := os.Remove(tmp.Name()); !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	writer := snapshotarchive.NewWriter(tmp, manifest)
	if manifest.DB != nil {
		if err := writer.AddDatabase(ctx, func(w io.Writer) error { return source.CopyDatabase(ctx, w) }); err != nil {
			return err
		}
	}
	for _, store := range source.Stores() {
		if _, err := writer.AddStore(ctx, parent, store, func(visit func(snapshotarchive.Object, io.Reader) error) error {
			return exportStore(ctx, source, store, opts.tenantStores[store], visit)
		}); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	verified, err := snapshotarchive.Open(ctx, tmp.Name(), "")
	if err != nil {
		return err
	}
	digest := verified.SHA256
	if err := verified.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("output appeared during export; refusing replacement")
	}
	if err := os.Link(tmp.Name(), output); err != nil {
		return err
	}
	dir, err := os.Open(parent)
	if err != nil {
		return err
	}
	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		return fmt.Errorf("archive published but directory durability is uncertain: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "Exported %s\nSHA-256: %s\nSource was read only; retain it and verify the target before resuming writers.\n", output, digest)
	return err
}

func within(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
