package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/machine"
)

const telemetryBundleKind = "scenery.telemetry.export"
const telemetryBundleFileLimit = 32 << 20
const telemetryBundleLimit = 256 << 20

type telemetryBundleFile struct {
	Path          string `json:"path"`
	ArchivePath   string `json:"archive_path"`
	SHA256        string `json:"sha256,omitempty"`
	Bytes         int64  `json:"bytes"`
	Purpose       string `json:"purpose"`
	MissingReason string `json:"missing_reason,omitempty"`
}

type telemetryBundle struct {
	machine.ArtifactIdentity
	OK          bool                  `json:"ok"`
	GeneratedAt string                `json:"generated_at"`
	Root        string                `json:"root"`
	Output      string                `json:"output"`
	Runs        []string              `json:"runs"`
	Files       []telemetryBundleFile `json:"files"`
	OmittedRuns int                   `json:"omitted_runs"`
	Unknown     []string              `json:"unknown"`
}

type telemetryBundleOptions struct {
	Root    string
	Output  string
	Runs    []string
	Include []string
	Limit   int
	JSON    bool
}

func runTelemetryBundleCommand(stdout io.Writer, args []string) error {
	opts := telemetryBundleOptions{Limit: 20}
	flags := newCLIFlagSet("telemetry export")
	flags.StringVar(&opts.Root, "app-root", "", "application or repository root")
	flags.StringVar(&opts.Output, "output", "", "new ZIP destination")
	flags.IntVar(&opts.Limit, "limit", 20, "maximum immutable runs, at most 100")
	flags.Func("run", "exact immutable run ID; repeat to select", func(value string) error { opts.Runs = append(opts.Runs, value); return nil })
	flags.Func("include", "additional selected log, trace or benchmark file; repeat to select", func(value string) error { opts.Include = append(opts.Include, value); return nil })
	output := "human"
	flags.StringVar(&output, "o", "human", "human or json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || opts.Limit < 1 || opts.Limit > 100 || (output != "human" && output != "json") {
		return usageErrorf("telemetry export accepts --app-root, --output, --run, --include, --limit 1..100 and -o human|json")
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return err
	}
	opts.Root = root
	opts.JSON = output == "json"
	if opts.Output == "" {
		opts.Output = filepath.Join(root, ".scenery", "harness", "exports", time.Now().UTC().Format("20060102T150405.000000000Z")+".zip")
	}
	result, err := writeTelemetryBundle(opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		if err := writeCLIJSON(stdout, result); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(stdout, "wrote %s (%d files, %d immutable runs; complete=%t)\n", result.Output, len(result.Files), len(result.Runs), result.OK)
	}
	if !result.OK {
		return &silentCLIError{err: fmt.Errorf("telemetry export contains missing or incomplete evidence; inspect manifest.json")}
	}
	return nil
}

// Export immutable transactions and their transitive raw references together.
// Latest files only navigate between runs and never enter this bundle.
func writeTelemetryBundle(opts telemetryBundleOptions) (telemetryBundle, error) {
	result := telemetryBundle{ArtifactIdentity: machine.NewArtifactIdentity(telemetryBundleKind, machine.ExactSchemaRevision(newCLIPayloadIdentity(telemetryBundleKind).SchemaRevision)), OK: true, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Root: opts.Root, Output: opts.Output, Runs: []string{}, Files: []telemetryBundleFile{}, Unknown: []string{"live client/server traces and external CI artifacts require an explicit --include selection; absence is not healthy collection proof"}}
	if len(opts.Runs) > opts.Limit {
		return result, fmt.Errorf("selected runs exceed export limit")
	}
	selected := map[string]bool{}
	for _, id := range opts.Runs {
		if !filepath.IsLocal(id) || filepath.Base(id) != id || strings.HasPrefix(id, ".") {
			return result, fmt.Errorf("invalid immutable run ID %q", id)
		}
		selected[id] = true
	}
	roots := []string{filepath.Join(opts.Root, ".scenery", "harness", "runs"), filepath.Join(opts.Root, ".scenery", "harness", "validation", "runs")}
	type run struct{ path, id string }
	var runs []run
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return result, err
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				runs = append(runs, run{filepath.Join(root, entry.Name()), entry.Name()})
			}
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].id > runs[j].id })
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o700); err != nil {
		return result, err
	}
	if _, err := os.Lstat(opts.Output); err == nil {
		return result, preconditionErrorf("export destination already exists: %s", opts.Output)
	} else if !os.IsNotExist(err) {
		return result, preconditionErrorf("export destination cannot be inspected: %v", err)
	}
	staging, err := os.CreateTemp(filepath.Dir(opts.Output), ".telemetry-export-*.zip")
	if err != nil {
		return result, err
	}
	defer func() { _ = staging.Close(); _ = os.Remove(staging.Name()) }()
	archive := zip.NewWriter(staging)
	seen := map[string]bool{}
	found := map[string]bool{}
	bytesWritten := int64(0)
	var add func(string, string, string, bool)
	add = func(path, name, purpose string, references bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		record := telemetryBundleFile{Path: path, ArchivePath: name, Purpose: purpose}
		missing := func(issue error) {
			record.MissingReason = issue.Error()
			result.Files = append(result.Files, record)
			result.OK = false
		}
		boundary := opts.Root
		if relative, err := filepath.Rel(boundary, path); err != nil || !filepath.IsLocal(relative) {
			// An explicitly selected file may live outside the workspace. Its
			// parent is user-selected; automatic references remain root-bound.
			boundary = filepath.Dir(path)
		}
		if err := refuseBundleSymlinks(boundary, path); err != nil {
			missing(err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			missing(err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, telemetryBundleFileLimit+1))
		closeErr := file.Close()
		if err != nil {
			missing(err)
			return
		}
		if closeErr != nil {
			missing(closeErr)
			return
		}
		if len(data) > telemetryBundleFileLimit || bytesWritten+int64(len(data)) > telemetryBundleLimit {
			missing(fmt.Errorf("evidence exceeds bounded export size"))
			return
		}
		writer, err := archive.Create(name)
		if err != nil {
			missing(err)
			return
		}
		if _, err = writer.Write(data); err != nil {
			missing(err)
			return
		}
		record.Bytes = int64(len(data))
		record.SHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		bytesWritten += record.Bytes
		result.Files = append(result.Files, record)
		if references && strings.HasSuffix(path, ".json") {
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				result.OK = false
				record.MissingReason = "invalid structured run record"
				result.Files[len(result.Files)-1] = record
				return
			}
			visitBundleReferences(value, func(ref string) {
				if filepath.IsAbs(ref) {
					relative, err := filepath.Rel(opts.Root, ref)
					if err != nil || !filepath.IsLocal(relative) {
						return
					}
					ref = filepath.ToSlash(relative)
				}
				if !strings.HasPrefix(ref, ".scenery/harness/") || !filepath.IsLocal(ref) || strings.Contains(filepath.Base(ref), "latest") || !bundleEvidenceExtension(ref) {
					return
				}
				add(filepath.Join(opts.Root, filepath.FromSlash(ref)), "workspace/"+filepath.ToSlash(ref), purpose, true)
			})
		}
	}
	count := 0
	for _, run := range runs {
		if len(selected) > 0 && !selected[run.id] {
			result.OmittedRuns++
			continue
		}
		if count >= opts.Limit {
			result.OmittedRuns++
			continue
		}
		count++
		found[run.id] = true
		relative, _ := filepath.Rel(opts.Root, run.path)
		result.Runs = append(result.Runs, filepath.ToSlash(relative))
		entries, err := os.ReadDir(run.path)
		if err != nil {
			add(run.path, "workspace/"+filepath.ToSlash(relative), "verification", false)
			continue
		}
		purpose := "verification"
		if strings.Contains(filepath.ToSlash(relative), "/validation/") {
			purpose = "validation"
		}
		expected := "self.json"
		if purpose == "validation" {
			expected = "result.json"
		}
		add(filepath.Join(run.path, expected), "workspace/"+filepath.ToSlash(filepath.Join(relative, expected)), purpose, true)
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() != expected {
				rel := filepath.Join(relative, entry.Name())
				add(filepath.Join(run.path, entry.Name()), "workspace/"+filepath.ToSlash(rel), purpose, true)
			}
		}
	}
	for id := range selected {
		if !found[id] {
			add(filepath.Join(roots[0], id, "self.json"), "missing/"+id+"/self.json", "verification", false)
		}
	}
	for index, path := range opts.Include {
		if !filepath.IsAbs(path) {
			path = filepath.Join(opts.Root, path)
		}
		add(path, fmt.Sprintf("selected/%03d-%s", index, filepath.Base(path)), "selected operational or measurement evidence", true)
	}
	if len(result.Runs) == 0 && len(opts.Include) == 0 {
		result.OK = false
		result.Unknown = append(result.Unknown, "no immutable runs selected")
	}
	manifest, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, err
	}
	writer, err := archive.Create("manifest.json")
	if err != nil {
		return result, err
	}
	if _, err = writer.Write(append(manifest, '\n')); err != nil {
		return result, err
	}
	if err := archive.Close(); err != nil {
		return result, err
	}
	if err := staging.Close(); err != nil {
		return result, err
	}
	// Publish only the completed archive, without replacing a destination that
	// appeared after the initial check. Unsupported hard links fail closed.
	if err := os.Link(staging.Name(), opts.Output); err != nil {
		if os.IsExist(err) {
			return result, preconditionErrorf("export destination already exists: %s", opts.Output)
		}
		return result, preconditionErrorf("export no-replace publication failed: %v", err)
	}
	return result, nil
}

func visitBundleReferences(value any, add func(string)) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "path" || key == "baseline_artifact" || key == "artifact" {
				if path, ok := child.(string); ok {
					add(path)
				}
			}
			visitBundleReferences(child, add)
		}
	case []any:
		for _, child := range value {
			visitBundleReferences(child, add)
		}
	}
}

func refuseBundleSymlinks(root, path string) error {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if relative, err := filepath.Rel(root, path); err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf("evidence is outside its selected root: %s", path)
	}
	for current := path; current != ""; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink evidence is not exported: %s", current)
		}
		if current == root {
			break
		}
	}
	return nil
}

func bundleEvidenceExtension(path string) bool {
	switch filepath.Ext(path) {
	case ".json", ".jsonl", ".xml", ".tap", ".log", ".txt", ".csv", ".zip":
		return true
	}
	return false
}
