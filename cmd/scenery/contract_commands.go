package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/contractagent"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

type contractOptions struct {
	AppRoot        string
	Output         string
	View           string
	Module         string
	Check          bool
	NonInteractive bool
	Quiet          bool
}

func runContractGenerate(stdout io.Writer, args []string) error {
	opts := contractOptions{Output: "human"}
	var target string
	flags := newCLIFlagSet("generate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "o", opts.Output, "")
	flags.StringVar(&target, "target", "", "")
	flags.BoolVar(&opts.Check, "check", false, "")
	flags.BoolVar(&opts.NonInteractive, "non-interactive", false, "")
	flags.BoolVar(&opts.Quiet, "quiet", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return err
	}
	if err := validateContractOutput(opts.Output); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("invalid_request: unexpected argument %q", positionals[0])
	}
	if target != "" && target != "contracts" && !strings.HasPrefix(target, "typescript_client.") {
		return fmt.Errorf("invalid_request: unknown generation target %q; use contracts or typescript_client.<name>", target)
	}
	root, err := findContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	var result generate.GenerateResult
	var generateErr error
	if strings.HasPrefix(target, "typescript_client.") {
		result, generateErr = generate.GenerateTypeScriptClients(root, target, opts.Check)
	} else if target == "" {
		result, generateErr = generate.GenerateAll(root, opts.Check)
	} else {
		result, generateErr = generate.GenerateGoContracts(root, opts.Check)
	}
	compilation := result.Compilation
	coverage := generate.ClientCoverageFor(compilation, target)
	if target == "contracts" {
		coverage = []generate.ClientCoverage{}
	}
	var generateDiagnostic graph.Diagnostic
	if generateErr != nil {
		code := "SCN6207"
		if strings.HasPrefix(generateErr.Error(), "revision_conflict:") {
			code = "SCN8002"
		}
		if opts.Check && len(result.Changed) > 0 {
			code = "SCN6204"
		}
		generateDiagnostic = graph.Diagnostic{Code: code, Severity: "error", Message: generateErr.Error()}
	}
	if opts.Output == "json" {
		envelope := newCLIEnvelope(generateErr == nil, map[string]any{"target": target, "generation": result, "clients": coverage}, nil)
		if compilation != nil {
			envelope.WorkspaceRevision = compilation.WorkspaceRevision
			if compilation.Manifest != nil {
				envelope.ContractRevision = compilation.Manifest.ContractRevision
			}
			if len(compilation.ImplementationRevisions) > 0 {
				envelope.ImplementationRevision = compilation.ImplementationRevisions
			}
			envelope.Diagnostics = append(envelope.Diagnostics, compilation.Diagnostics...)
		}
		if generateErr != nil {
			envelope.Diagnostics = append(envelope.Diagnostics, generateDiagnostic)
		}
		_ = json.NewEncoder(stdout).Encode(envelope)
	}
	if generateErr != nil {
		if opts.Check && len(result.Changed) > 0 {
			stale := findingsDiagnosticError(generateErr, generateDiagnostic)
			if opts.Output == "json" {
				return &silentCLIError{err: stale, code: 1}
			}
			return stale
		}
		if opts.Output == "json" {
			// Telemetry records the diagnostic this command rendered.
			code := cliExitCode(generateErr)
			return &silentCLIError{err: &cliDiagnosticError{err: generateErr, code: code, diagnostic: generateDiagnostic}, code: code}
		}
		return generateErr
	}
	if opts.Output == "human" && !opts.Quiet {
		for _, client := range coverage {
			_, _ = fmt.Fprintf(stdout, "scenery: %s: %d HTTP bindings\n", client.Target, client.Bindings)
			if client.Message != "" {
				_, _ = fmt.Fprintln(stdout, "scenery: warning: "+client.Message)
			}
		}
		if len(result.Changed) == 0 {
			_, _ = fmt.Fprintln(stdout, "scenery: generated contracts are current")
		} else {
			_, _ = fmt.Fprintln(stdout, strings.Join(result.Changed, "\n"))
		}
	}
	return nil
}

func parseContractOptions(name string, args []string) (contractOptions, []string, error) {
	opts := contractOptions{Output: "human", View: "expanded"}
	flags := newCLIFlagSet(name)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "o", opts.Output, "")
	flags.StringVar(&opts.View, "view", opts.View, "")
	flags.StringVar(&opts.Module, "module", "", "")
	flags.BoolVar(&opts.Check, "check", false, "")
	flags.BoolVar(&opts.NonInteractive, "non-interactive", false, "")
	flags.BoolVar(&opts.Quiet, "quiet", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return contractOptions{}, nil, err
	}
	if err := validateContractOutput(opts.Output); err != nil {
		return contractOptions{}, nil, err
	}
	if opts.View != "source" && opts.View != "effective" && opts.View != "expanded" {
		return contractOptions{}, nil, fmt.Errorf("invalid_request: unsupported graph view %q", opts.View)
	}
	return opts, positionals, nil
}

func validateContractOutput(output string) error {
	if output != "human" && output != "json" {
		return fmt.Errorf("invalid_request: unsupported output %q; use human or json", output)
	}
	return nil
}

func findContractRoot(value string) (string, error) {
	start := value
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if pathExistsLocal(filepath.Join(abs, scn.AppFilename)) {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", usageErrorf("no %s found from %s; run inside a Scenery app or pass --app-root <path>", scn.AppFilename, start)
		}
		abs = parent
	}
}

func runContractCheck(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("check", args)
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return usageErrorf("unexpected argument %q", positionals[0])
	}
	root, err := findContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := checkCompiledContract(root)
	if err != nil {
		return err
	}
	// check is a validation gate: it reports statuses, revisions, and a
	// compact manifest summary. The full graph stays on `scenery compile`
	// and `list`.
	data := map[string]any{"contract_status": result.ContractStatus, "implementation_status": result.ImplementationStatus, "manifest_summary": contractManifestSummary(result.Manifest), "http_surface_revision": result.HTTPSurfaceRevisions, "openapi_revision": result.OpenAPIRevisions}
	if result.PartialGraph != nil {
		data["partial_graph"] = result.PartialGraph
	}
	return writeSourceResult(stdout, opts.Output, opts.Quiet, result, data)
}

func contractManifestSummary(manifest *graph.Manifest) map[string]any {
	if manifest == nil {
		return nil
	}
	byKind := map[string]int{}
	for _, resource := range manifest.Resources {
		byKind[resource.Kind]++
	}
	return map[string]any{
		"application":       manifest.Application,
		"resources":         len(manifest.Resources),
		"resources_by_kind": byKind,
	}
}

func runContractCompile(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("compile", args)
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return usageErrorf("unexpected argument %q", positionals[0])
	}
	result, err := compileContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	manifest := result.Manifest
	if result.Valid() {
		manifest, err = result.ManifestForView(opts.View)
		if err != nil {
			return err
		}
	}
	data := map[string]any{"contract_status": result.ContractStatus, "implementation_status": result.ImplementationStatus, "view": opts.View, "manifest": manifest}
	if result.PartialGraph != nil {
		data["partial_graph"] = result.PartialGraph
	}
	return writeSourceResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runContractList(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("list", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return usageErrorf("usage: scenery list <kind>")
	}
	result, err := compileContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	manifest, err := result.ManifestForView(opts.View)
	if err != nil {
		return err
	}
	var resources []graph.Resource
	if manifest != nil {
		for _, resource := range manifest.Resources {
			if resourceKindMatches(resource, positionals[0]) && (opts.Module == "" || resource.Module == opts.Module) {
				resources = append(resources, resource)
			}
		}
	}
	return writeSourceResult(stdout, opts.Output, opts.Quiet, result, map[string]any{"view": opts.View, "resources": resources})
}

func runContractSchema(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("schema", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return usageErrorf("usage: scenery schema <kind>")
	}
	schema, ok := contractagent.AgentSchema(positionals[0])
	if !ok {
		qualified := "scenery." + positionals[0]
		if _, found := contractagent.AgentSchema(qualified); found {
			return fmt.Errorf("invalid_request: schema %q not found; use scenery schema %s -o json", positionals[0], qualified)
		}
		return fmt.Errorf("schema %q not found", positionals[0])
	}
	if opts.Output == "json" {
		return json.NewEncoder(stdout).Encode(newCLIEnvelope(true, schema, nil))
	}
	b, _ := json.MarshalIndent(schema, "", "  ")
	if opts.Quiet {
		return nil
	}
	_, err = fmt.Fprintln(stdout, string(b))
	return err
}

func runContractFmt(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("fmt", args)
	if err != nil {
		return err
	}
	root, err := findContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	result, formatErr := scn.FormatPaths(root, positionals, opts.Check)
	var formatDiagnostic graph.Diagnostic
	if formatErr != nil {
		formatDiagnostic = graph.Diagnostic{Code: "SCN1019", Severity: "error", Message: formatErr.Error()}
	}
	if opts.Output == "json" {
		diagnostics := []graph.Diagnostic{}
		if formatErr != nil {
			diagnostics = append(diagnostics, formatDiagnostic)
		}
		_ = json.NewEncoder(stdout).Encode(newCLIEnvelope(formatErr == nil, result, diagnostics))
	}
	if formatErr != nil {
		if opts.Check && len(result.Changed) > 0 {
			drift := findingsDiagnosticError(formatErr, formatDiagnostic)
			if opts.Output == "json" {
				return &silentCLIError{err: drift, code: 1}
			}
			// A person needs the files and the reason, not only exit 1.
			if !opts.Quiet {
				sort.Strings(result.Changed)
				_, _ = fmt.Fprintln(stdout, strings.Join(result.Changed, "\n"))
			}
			return drift
		}
		if opts.Output == "json" {
			return &silentCLIError{err: formatErr, code: 2}
		}
		return &codedCLIError{err: formatErr, code: 2}
	}
	if opts.Output == "human" && !opts.Quiet {
		if len(result.Changed) == 0 {
			_, _ = fmt.Fprintln(stdout, "scenery: format ok")
		} else {
			sort.Strings(result.Changed)
			_, _ = fmt.Fprintln(stdout, strings.Join(result.Changed, "\n"))
		}
	}
	return nil
}
