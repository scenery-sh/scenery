package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"scenery.sh/internal/appwalk"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

const addModuleKind = "scenery.add.module.result"

type addModuleOptions struct {
	root, name, runtime string
	json                bool
}

type addModuleResult struct {
	cliPayloadIdentity
	Module         string             `json:"module"`
	Runtime        string             `json:"runtime"`
	CreatedFiles   []string           `json:"created_files"`
	UpdatedFiles   []string           `json:"updated_files"`
	GeneratedRoots []string           `json:"generated_roots"`
	CheckOK        bool               `json:"check_ok"`
	Diagnostics    []graph.Diagnostic `json:"diagnostics"`
}

func parseAddModuleArgs(args []string) (addModuleOptions, error) {
	var opts addModuleOptions
	flags := newCLIFlagSet("add module")
	flags.StringVar(&opts.root, "app-root", "", "")
	flags.StringVar(&opts.runtime, "runtime", "", "")
	registerJSONOutput(flags, &opts.json)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if len(positionals) != 1 || !scn.IdentifierPattern.MatchString(positionals[0]) {
		return opts, usageErrorf("add module requires one .scn module identifier (lowercase letters, digits, and underscores)")
	}
	opts.name = positionals[0]
	if !slices.Contains([]string{"go", "rust", "ts", "js"}, opts.runtime) {
		return opts, usageErrorf("--runtime must be go, rust, ts, or js")
	}
	if strings.Contains(opts.name, "scenery") || opts.runtime == "go" && token.Lookup(opts.name).IsKeyword() {
		return opts, usageErrorf("module name %q cannot be used in the generated package or HTTP route", opts.name)
	}
	if reservedModuleName(opts.name, opts.runtime) {
		return opts, usageErrorf("module name %q is reserved or names a directory that source watching skips", opts.name)
	}
	return opts, nil
}

// reservedModuleName reports names that collide with the root `app` or
// standard `std` address namespace, with Go's main package, or with
// directories that rebuild planning and build caches skip (Cargo `target`,
// `dist`, `out`, `node_modules`): edits there would never trigger a rebuild.
func reservedModuleName(name, runtime string) bool {
	switch name {
	case "app", "std", "target":
		return true
	case "main":
		return runtime == "go"
	}
	return appwalk.SkipDirName(name)
}

type moduleScaffold struct {
	addModuleResult
	files    []workspacetx.File
	cargoDir string
	rustTool string
}

func runAddModule(ctx context.Context, out io.Writer, args []string) error {
	opts, err := parseAddModuleArgs(args)
	if err != nil {
		return err
	}
	root, err := findContractRoot(opts.root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	// Compile before taking the publication lock. Its snapshot is checked
	// again under the lock before the authored edits are prepared.
	compiled, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return err
	}
	if !compiled.Valid() {
		return preconditionErrorf("fix existing application diagnostics with scenery check before scaffolding a module")
	}
	if err := validateModuleDirectory(root, opts.name, compiled.Manifest.Resources); err != nil {
		return err
	}
	var scaffold moduleScaffold
	if err := workspacetx.PublishContext(ctx, root, func() ([]workspacetx.File, error) {
		unchanged, err := compiler.SnapshotUnchangedDuringChangeTransaction(compiled)
		if err != nil {
			return nil, err
		}
		if !unchanged {
			return nil, preconditionErrorf("application source changed while planning module %q; retry the command", opts.name)
		}
		scaffold, err = planModuleScaffold(root, opts, compiled.Manifest.Resources)
		return scaffold.files, err
	}); err != nil {
		return err
	}
	result := &scaffold.addModuleResult
	result.Diagnostics, err = verifyModuleScaffold(ctx, root, &scaffold)
	result.CheckOK = err == nil
	if err != nil && len(result.Diagnostics) == 0 {
		result.Diagnostics = []graph.Diagnostic{{Code: "SCN8003", Severity: "error", Message: err.Error()}}
	}
	if opts.json {
		if encodeErr := json.NewEncoder(out).Encode(newCLIEnvelope(result.CheckOK, result, result.Diagnostics)); encodeErr != nil {
			return encodeErr
		}
	} else {
		_, _ = fmt.Fprintf(out, "Added %s (%s), check passed: %t\n", opts.name, opts.runtime, result.CheckOK)
		for _, diagnostic := range result.Diagnostics {
			_, _ = fmt.Fprintf(out, "%s: %s\n", diagnostic.Code, diagnostic.Message)
		}
	}
	if err != nil {
		return &silentCLIError{err: err, code: 3}
	}
	return nil
}

func validateModuleDirectory(root, name string, resources []graph.Resource) error {
	for _, resource := range resources {
		if resource.Kind == "scenery.module" && resource.Name == name {
			return usageErrorf("module %q is already declared", name)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return preconditionErrorf("module path %q already exists", name)
		}
		return err
	}
	return nil
}

func planModuleScaffold(root string, opts addModuleOptions, resources []graph.Resource) (moduleScaffold, error) {
	result := moduleScaffold{addModuleResult: addModuleResult{
		cliPayloadIdentity: newCLIPayloadIdentity(addModuleKind), Module: opts.name, Runtime: opts.runtime,
		CreatedFiles: []string{}, UpdatedFiles: []string{}, GeneratedRoots: []string{opts.name + "/scenerycontract"},
		Diagnostics: []graph.Diagnostic{},
	}}
	if err := validateModuleDirectory(root, opts.name, resources); err != nil {
		return result, err
	}
	app, err := readScaffoldFile(root, scn.AppFilename)
	if err != nil {
		return result, err
	}
	gateway, extra := scaffoldGateway(resources, opts.name)
	var packageMetadata, constructor, method string
	switch opts.runtime {
	case "go":
		importPath, err := scaffoldGoImport(resources, opts.name)
		if err != nil {
			return result, err
		}
		packageMetadata = fmt.Sprintf("go_contract { import_path = %q }", importPath)
		constructor, method = "NewService", "Ping"
		result.addGoFiles(importPath)
	case "rust":
		var declarations string
		declarations, err = result.addRustWorkspace(root, resources)
		if err != nil {
			return result, err
		}
		extra += declarations
		crate := "module_" + opts.name
		packageMetadata = fmt.Sprintf("rust_contract { crate = %q }", crate)
		constructor, method = "new_service", "ping"
		result.addRustFiles(crate)
	case "js", "ts":
		declarations, err := result.addJSWorkspace(root, resources)
		if err != nil {
			return result, err
		}
		extra += declarations
		packageMetadata = fmt.Sprintf("js_contract { entry = %q }", "service."+opts.runtime)
		constructor, method = "newService", "ping"
		result.addJSFiles()
	}
	result.create(opts.name+"/package.scn", fmt.Sprintf(modulePackageTemplate, opts.name, packageMetadata, opts.name, opts.runtime, constructor, opts.name, method, opts.name))
	updated, err := appendScaffoldRoot(app, result.GeneratedRoots[0])
	if err != nil {
		return result, err
	}
	updated = fmt.Appendf(updated, "\n%s\nmodule %q {\n  source = %q\n  inputs = { gateway = http_gateway.%s }\n}\n", extra, opts.name, "./"+opts.name, gateway)
	result.update(scn.AppFilename, app, updated)
	slices.Sort(result.CreatedFiles)
	slices.Sort(result.UpdatedFiles)
	return result, nil
}

func readScaffoldFile(root, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	if err := scn.RejectPathSymlinks(root, path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, preconditionErrorf("expected a regular file at %q", name)
	}
	return os.ReadFile(path)
}

func (s *moduleScaffold) create(path, content string) {
	s.CreatedFiles = append(s.CreatedFiles, path)
	s.files = append(s.files, workspacetx.File{Path: path, Bytes: []byte(content), ExpectedBeforeDigest: workspacetx.AbsentDigest})
}

func (s *moduleScaffold) update(path string, before, after []byte) {
	if bytes.Equal(before, after) {
		return
	}
	s.UpdatedFiles = append(s.UpdatedFiles, path)
	s.files = append(s.files, workspacetx.File{Path: path, Bytes: after, ExpectedBeforeDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(before))})
}

func appendScaffoldRoot(source []byte, root string) ([]byte, error) {
	file, diagnostics := hclsyntax.ParseConfig(source, scn.AppFilename, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, preconditionErrorf("cannot edit invalid app.scn: %s", diagnostics.Error())
	}
	for _, block := range file.Body.(*hclsyntax.Body).Blocks {
		if block.Type != "workspace" {
			continue
		}
		attr := block.Body.Attributes["managed_generated_roots"]
		if attr == nil {
			offset := block.OpenBraceRange.End.Byte
			return insertScaffoldText(source, offset, fmt.Sprintf("\n  managed_generated_roots = [%q]\n", root)), nil
		}
		tuple, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
		if !ok {
			return nil, preconditionErrorf("managed_generated_roots must be a list literal")
		}
		if len(tuple.Exprs) == 0 {
			return insertScaffoldText(source, tuple.OpenRange.End.Byte, fmt.Sprintf("%q", root)), nil
		}
		offset := tuple.Exprs[len(tuple.Exprs)-1].Range().End.Byte
		// Insert ahead of trailing trivia, including any comma and comments.
		return insertScaffoldText(source, offset, fmt.Sprintf(", %q", root)), nil
	}
	return fmt.Appendf(bytes.Clone(source), "\nworkspace {\n  managed_generated_roots = [%q]\n}\n", root), nil
}

func insertScaffoldText(source []byte, offset int, text string) []byte {
	result := make([]byte, 0, len(source)+len(text))
	result = append(result, source[:offset]...)
	result = append(result, text...)
	return append(result, source[offset:]...)
}

func scaffoldGateway(resources []graph.Resource, name string) (string, string) {
	for _, resource := range resources {
		if resource.Kind == "scenery.http-gateway" && resource.Module == "app" && resource.Spec["exposure"] == "internet" {
			return resource.Name, ""
		}
	}
	gateway := "module_" + name
	return gateway, fmt.Sprintf(`http_gateway %q {
  exposure = "internet"
  base_path = "/api"
  cors = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded = std.forwarded_headers.reject
}
`, gateway)
}

func scaffoldGoImport(resources []graph.Resource, name string) (string, error) {
	for _, resource := range resources {
		if resource.Kind == "scenery.go-module" && cp.StringValue(resource.Spec["root"]) == "." {
			return strings.TrimSuffix(cp.StringValue(resource.Spec["import_path"]), "/") + "/" + name, nil
		}
	}
	return "", preconditionErrorf("adding a Go module requires a root go_module declaration")
}

func verifyModuleScaffold(ctx context.Context, root string, scaffold *moduleScaffold) ([]graph.Diagnostic, error) {
	compilation, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return nil, err
	}
	if !compilation.Valid() {
		return compilation.Diagnostics, fmt.Errorf("module declarations did not compile")
	}
	if _, err := generate.GenerateSnapshotWithContext(ctx, compilation, "", false); err != nil {
		return compilation.Diagnostics, err
	}
	if err := scaffold.updateCargoLock(ctx, root); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	err = runContractCheck(&output, []string{"--app-root", root, "-o", "json"})
	var envelope struct{ Diagnostics []graph.Diagnostic }
	if output.Len() > 0 {
		if decodeErr := json.Unmarshal(output.Bytes(), &envelope); decodeErr != nil {
			return nil, errors.Join(err, decodeErr)
		}
	}
	if envelope.Diagnostics == nil {
		envelope.Diagnostics = []graph.Diagnostic{}
	}
	return envelope.Diagnostics, err
}
