package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/build"
)

type frameworkOptions struct {
	Command string
	AppRoot string
	Source  string
	JSON    bool
}

type frameworkResult struct {
	cliPayloadIdentity
	OK               bool   `json:"ok"`
	AppRoot          string `json:"app_root"`
	Mode             string `json:"mode"`
	FrameworkVersion string `json:"framework_version"`
	SourceRoot       string `json:"source_root"`
	SourceDigest     string `json:"source_digest"`
	SourceRevision   string `json:"source_revision,omitempty"`
	Executable       string `json:"executable"`
	ExecutableDigest string `json:"executable_digest"`
	SelectionPath    string `json:"selection_path"`
	ModuleChanged    bool   `json:"module_changed"`
	NextCommand      string `json:"next_command"`
}

func frameworkCommand(args []string) error {
	return runFrameworkCommand(context.Background(), os.Stdout, args)
}

func parseFrameworkArgs(args []string) (frameworkOptions, error) {
	opts := frameworkOptions{}
	flags := newCLIFlagSet("framework")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Source, "source", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if len(positionals) != 1 || (positionals[0] != "use" && positionals[0] != "inspect") {
		return opts, fmt.Errorf("usage: scenery framework use|inspect [--source <checkout>] [--app-root <path>] [-o json]")
	}
	opts.Command = positionals[0]
	if opts.Command == "inspect" && opts.Source != "" {
		return opts, fmt.Errorf("--source belongs only to scenery framework use")
	}
	return opts, nil
}

func runFrameworkCommand(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseFrameworkArgs(args)
	if err != nil {
		return err
	}
	root, _, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	var selection build.FrameworkSelection
	changed := false
	if opts.Command == "use" {
		selection, changed, err = useAppFramework(ctx, root, opts.Source)
	} else {
		selection, err = build.ReadFrameworkSelection(root)
	}
	if err == nil {
		err = build.VerifyFrameworkSelection(ctx, selection)
	}
	if err != nil {
		return &codedCLIError{err: err, code: 3}
	}
	mode := "pinned"
	if selection.Version == "dev" {
		mode = "source"
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	result := frameworkResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.framework"), OK: true, AppRoot: root, Mode: mode,
		FrameworkVersion: selection.Version, SourceRoot: selection.Source.Root, SourceDigest: selection.Source.Digest,
		SourceRevision: selection.SourceRevision, Executable: selection.Executable, ExecutableDigest: selection.ExecutableDigest,
		SelectionPath: build.FrameworkSelectionPath(root), ModuleChanged: changed,
		NextCommand: quote(selection.Executable) + " up --app-root " + quote(root),
	}
	if opts.JSON {
		return writeCLIJSON(stdout, result)
	}
	_, err = fmt.Fprintf(stdout, "framework %s %s\nsource: %s\nexecutable: %s\nnext: %s\n", mode, result.SourceDigest, result.SourceRoot, result.Executable, result.NextCommand)
	return err
}

func useAppFramework(ctx context.Context, root, sourceOverride string) (build.FrameworkSelection, bool, error) {
	var selection build.FrameworkSelection
	modulePath := filepath.Join(root, "go.mod")
	moduleInfo, err := os.Lstat(modulePath)
	if err != nil {
		return selection, false, err
	}
	if !moduleInfo.Mode().IsRegular() {
		return selection, false, fmt.Errorf("application go.mod must be a regular non-symlink file")
	}
	before, err := os.ReadFile(modulePath)
	if err != nil {
		return selection, false, err
	}
	module, err := modfile.Parse("go.mod", before, nil)
	if err != nil {
		return selection, false, err
	}
	source, version := sourceOverride, "dev"
	local := sourceOverride != ""
	if sourceOverride == "" {
		source, version, err = build.ResolveFrameworkModule(ctx, root, true)
		if err != nil {
			return selection, false, err
		}
		for _, replacement := range module.Replace {
			if replacement.Old.Path == "scenery.sh" && replacement.New.Version == "" && (replacement.Old.Version == "" || replacement.Old.Version == version) {
				local = true
			}
		}
	}
	revision := ""
	if local {
		version = "dev"
		command := exec.CommandContext(ctx, "git", "-C", source, "rev-parse", "HEAD")
		if output, err := command.Output(); err == nil {
			revision = strings.TrimSpace(string(output))
		}
	}
	selection, err = build.PrepareFramework(ctx, root, source, version, revision)
	if err != nil {
		return selection, false, err
	}
	after := before
	if local {
		after, err = selectFrameworkSnapshotModule(before, root, selection.Source.Root)
		if err != nil {
			return selection, false, err
		}
	}
	current, err := os.ReadFile(modulePath)
	if err != nil || !bytes.Equal(current, before) {
		return selection, false, fmt.Errorf("application go.mod changed while preparing the framework; retry without overwriting that edit")
	}
	// A torn selection is fail-closed: runtime verification compares the receipt,
	// authored module and actual source. No runtime is started by this command.
	if err := build.WriteFrameworkSelection(selection); err != nil {
		return selection, false, err
	}
	changed := !bytes.Equal(before, after)
	if changed {
		if err := atomicfile.Write(modulePath, after, moduleInfo.Mode().Perm(), atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
			return selection, false, err
		}
	}
	return selection, changed, nil
}

func selectFrameworkSnapshotModule(data []byte, appRoot, snapshotRoot string) ([]byte, error) {
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(appRoot, snapshotRoot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("framework snapshot must be inside the selected app root")
	}
	hasRequirement := false
	for _, requirement := range module.Require {
		if requirement.Mod.Path == "scenery.sh" {
			hasRequirement = true
		}
	}
	if !hasRequirement {
		if err := module.AddRequire("scenery.sh", "v0.0.0"); err != nil {
			return nil, err
		}
	}
	for _, replacement := range append([]*modfile.Replace(nil), module.Replace...) {
		if replacement.Old.Path == "scenery.sh" {
			if err := module.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
				return nil, err
			}
		}
	}
	if err := module.AddReplace("scenery.sh", "", "./"+filepath.ToSlash(relative), ""); err != nil {
		return nil, err
	}
	return module.Format()
}
