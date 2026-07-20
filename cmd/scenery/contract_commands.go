package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/contractagent"
	"scenery.sh/internal/evolution"
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

func compileCommand(args []string) error { return runContractCompile(os.Stdout, args) }
func schemaCommand(args []string) error  { return runContractSchema(os.Stdout, args) }
func listCommand(args []string) error    { return runContractList(os.Stdout, args) }
func getCommand(args []string) error     { return runContractGet(os.Stdout, args, false) }
func explainCommand(args []string) error { return runContractGet(os.Stdout, args, true) }
func fmtCommand(args []string) error     { return runContractFmt(os.Stdout, args) }
func diffCommand(args []string) error    { return runContractDiff(os.Stdout, args) }
func graphCommand(args []string) error   { return runContractGraph(os.Stdout, args) }
func changesCommand(args []string) error { return runContractChanges(os.Stdout, args) }
func runContractAgentServer(stdin io.Reader, stdout io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return fmt.Errorf("usage: scenery agent serve --stdio [--app-root <path>]")
	}
	var appRoot string
	var stdio bool
	flags := newCLIFlagSet("agent serve")
	flags.BoolVar(&stdio, "stdio", false, "")
	flags.StringVar(&appRoot, "app-root", "", "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if !stdio || len(positionals) != 0 {
		return fmt.Errorf("usage: scenery agent serve --stdio [--app-root <path>]")
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64*1024), 2_000_000)
	encoder := json.NewEncoder(stdout)
	session := contractagent.NewAgentSession()
	for scanner.Scan() {
		var request contractagent.AgentRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if encodeErr := encoder.Encode(contractagent.AgentResponse{JSONRPC: "2.0", Error: &contractagent.AgentError{Code: -32700, Kind: "invalid_request", Message: "invalid JSON"}}); encodeErr != nil {
				return encodeErr
			}
			continue
		}
		result, compileErr := compileContractRoot(appRoot)
		if compileErr != nil {
			return compileErr
		}
		if err := encoder.Encode(session.Handle(result, request)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func runContractChanges(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery changes plan|apply")
	}
	subcommand := args[0]
	var appRoot, output, changesPath, planPath, outPath string
	var baseWorkspace, baseContract, expectWorkspace, expectContract string
	var approvalTokenPaths []string
	var dryRun, nonInteractive, quiet bool
	flags := newCLIFlagSet("changes " + subcommand)
	flags.StringVar(&appRoot, "app-root", "", "")
	flags.StringVar(&output, "o", "human", "")
	flags.StringVar(&changesPath, "changes", "", "")
	flags.StringVar(&planPath, "plan", "", "")
	flags.StringVar(&outPath, "out", "", "")
	flags.StringVar(&baseWorkspace, "base-workspace-revision", "", "")
	flags.StringVar(&baseContract, "base-contract-revision", "", "")
	flags.StringVar(&expectWorkspace, "expect-workspace-revision", "", "")
	flags.StringVar(&expectContract, "expect-contract-revision", "", "")
	flags.BoolVar(&dryRun, "dry-run", false, "")
	flags.BoolVar(&nonInteractive, "non-interactive", false, "")
	flags.BoolVar(&quiet, "quiet", false, "")
	flags.Func("approval-token", "", func(value string) error { approvalTokenPaths = append(approvalTokenPaths, value); return nil })
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if err := validateContractOutput(output); err != nil {
		return err
	}
	root, err := findContractRoot(appRoot)
	if err != nil {
		return err
	}
	switch subcommand {
	case "plan":
		if changesPath == "" || outPath == "" || len(positionals) > 0 {
			return fmt.Errorf("usage: scenery changes plan --changes FILE --base-workspace-revision REV --base-contract-revision REV --out PLAN")
		}
		operations, err := readSemanticOperations(changesPath)
		if err != nil {
			return err
		}
		plan, err := evolution.PlanChanges(root, evolution.ChangeRequest{BaseWorkspaceRevision: baseWorkspace, BaseContractRevision: revisionFlag(baseContract), Caller: "local", Operations: operations})
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(plan, "", "  ")
		b = append(b, '\n')
		temporary := outPath + ".tmp"
		if err := os.WriteFile(temporary, b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(temporary, outPath); err != nil {
			return err
		}
		if output == "json" {
			envelope := newCLIEnvelope(true, plan, nil)
			envelope.WorkspaceRevision = plan.BaseWorkspaceRevision
			envelope.ContractRevision = plan.BaseContractRevision
			return json.NewEncoder(stdout).Encode(envelope)
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, plan.PlanID)
		}
		return err
	case "apply":
		if planPath == "" && len(positionals) == 1 {
			planPath = positionals[0]
		} else if planPath == "" || len(positionals) > 0 {
			return fmt.Errorf("usage: scenery changes apply PLAN --expect-workspace-revision REV --expect-contract-revision REV")
		}
		var plan evolution.ChangePlan
		if err := readExactPlanFile(planPath, "change plan", &plan); err != nil {
			return err
		}
		approvalTokens, err := readApprovalTokens(approvalTokenPaths)
		if err != nil {
			return err
		}
		approvalVerifier, err := approvalVerifierForTokens(root, approvalTokens)
		if err != nil {
			return err
		}
		receipt, err := evolution.ApplyChangePlanWithOptions(root, plan, evolution.ApplyOptions{ExpectedWorkspaceRevision: expectWorkspace, ExpectedContractRevision: revisionFlag(expectContract), Caller: "local", ApprovalTokens: approvalTokens, VerifyApproval: approvalVerifier})
		if err != nil {
			return err
		}
		if output == "json" {
			envelope := newCLIEnvelope(true, receipt, nil)
			envelope.WorkspaceRevision = receipt.WorkspaceRevision
			envelope.ContractRevision = receipt.ContractRevision
			return json.NewEncoder(stdout).Encode(envelope)
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, receipt.PlanID)
		}
		return err
	case "rename":
		if len(positionals) != 2 {
			return fmt.Errorf("usage: scenery changes rename ADDRESS NEW_NAME [--dry-run] [-o json]")
		}
		base, err := compiler.Compile(root)
		if err != nil {
			return err
		}
		if !base.Valid() {
			return fmt.Errorf("current contract is invalid")
		}
		plan, err := evolution.PlanChanges(root, evolution.ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: revisionFlag(base.Manifest.ContractRevision), Caller: "local", Operations: []evolution.SemanticOperation{{Op: "resource.rename", Address: positionals[0], Value: positionals[1]}}})
		if err != nil {
			return err
		}
		data := any(plan)
		workspaceRevision, contractRevision := base.WorkspaceRevision, base.Manifest.ContractRevision
		if !dryRun {
			approvalTokens, tokenErr := readApprovalTokens(approvalTokenPaths)
			if tokenErr != nil {
				return tokenErr
			}
			approvalVerifier, verifierErr := approvalVerifierForTokens(root, approvalTokens)
			if verifierErr != nil {
				return verifierErr
			}
			receipt, applyErr := evolution.ApplyChangePlanWithOptions(root, plan, evolution.ApplyOptions{
				ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: revisionFlag(base.Manifest.ContractRevision),
				Caller: "local", ApprovalTokens: approvalTokens, VerifyApproval: approvalVerifier,
			})
			if applyErr != nil {
				return applyErr
			}
			data = receipt
			workspaceRevision, contractRevision = receipt.WorkspaceRevision, receipt.ContractRevision
		}
		if output == "json" {
			envelope := newCLIEnvelope(true, data, nil)
			envelope.WorkspaceRevision = workspaceRevision
			envelope.ContractRevision = contractRevision
			return json.NewEncoder(stdout).Encode(envelope)
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, plan.PlanID)
		}
		return err
	default:
		return fmt.Errorf("unknown scenery changes subcommand %q", subcommand)
	}
}

func readSemanticOperations(path string) ([]evolution.SemanticOperation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var operations []evolution.SemanticOperation
	if err := json.Unmarshal(b, &operations); err == nil {
		return operations, nil
	}
	var request evolution.ChangeRequest
	if err := json.Unmarshal(b, &request); err != nil {
		return nil, err
	}
	return request.Operations, nil
}

func revisionFlag(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") || strings.EqualFold(value, "null") {
		return nil
	}
	return &value
}

func runContractGraph(stdout io.Writer, args []string) error {
	var appRoot, output, direction, view string
	var depth, maxResources int
	var nonInteractive, quiet bool
	flags := newCLIFlagSet("graph")
	flags.StringVar(&appRoot, "app-root", "", "")
	flags.StringVar(&output, "o", "human", "")
	flags.StringVar(&direction, "direction", "both", "")
	flags.StringVar(&view, "view", "effective", "")
	flags.IntVar(&depth, "depth", 1, "")
	flags.IntVar(&maxResources, "max-resources", 100, "")
	flags.BoolVar(&nonInteractive, "non-interactive", false, "")
	flags.BoolVar(&quiet, "quiet", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return err
	}
	if err := validateContractOutput(output); err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery graph ADDRESS [--direction dependencies|dependents|both]")
	}
	result, err := compileContractRoot(appRoot)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return writeContractResult(stdout, output, quiet, result, nil)
	}
	manifest, err := result.ManifestForView(view)
	if err != nil {
		return err
	}
	resourceGraph, err := graph.Graph(manifest, positionals[0], graph.GraphOptions{Direction: direction, Depth: depth, MaxResources: maxResources})
	if err != nil {
		return err
	}
	return writeContractResult(stdout, output, quiet, result, resourceGraph)
}

func runContractDiff(stdout io.Writer, args []string) error {
	var output, view, renameReceiptsPath string
	var semantic, exitCode, nonInteractive, quiet bool
	flags := newCLIFlagSet("diff")
	flags.BoolVar(&semantic, "semantic", false, "")
	flags.BoolVar(&exitCode, "exit-code", false, "")
	flags.StringVar(&output, "o", "human", "")
	flags.StringVar(&view, "view", "expanded", "")
	flags.StringVar(&renameReceiptsPath, "rename-receipts", "", "")
	flags.BoolVar(&nonInteractive, "non-interactive", false, "")
	flags.BoolVar(&quiet, "quiet", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return err
	}
	if err := validateContractOutput(output); err != nil {
		return err
	}
	if !semantic || len(positionals) != 2 {
		return fmt.Errorf("usage: scenery diff --semantic BASE TARGET [--rename-receipts change-plan-or-receipt.json] [-o human|json]")
	}
	base, err := evolution.LoadManifestReference(positionals[0])
	if err != nil {
		return fmt.Errorf("load base: %w", err)
	}
	target, err := evolution.LoadManifestReference(positionals[1])
	if err != nil {
		return fmt.Errorf("load target: %w", err)
	}
	var renames []evolution.RenameReceipt
	var rebinds []evolution.RevisionRebind
	if renameReceiptsPath != "" {
		renames, rebinds, err = evolution.LoadRenameEvidence(renameReceiptsPath)
		if err != nil {
			// Preserve the existing bare-array receipt format.
			renames, err = evolution.LoadRenameReceipts(renameReceiptsPath)
		}
		if err != nil {
			return fmt.Errorf("load rename evidence: %w", err)
		}
	}
	for _, reference := range []string{positionals[1], positionals[0]} {
		if info, statErr := os.Stat(reference); statErr == nil && info.IsDir() {
			persisted, persistedRebinds, loadErr := evolution.LoadAppliedRenameEvidence(reference)
			if loadErr != nil {
				return fmt.Errorf("load applied rename receipts: %w", loadErr)
			}
			renames = append(renames, persisted...)
			rebinds = append(rebinds, persistedRebinds...)
			break
		}
	}
	diff := evolution.CompareManifests(base, target, evolution.CompareOptions{View: view, Renames: renames, Rebinds: rebinds})
	if output == "json" {
		envelope := newCLIEnvelope(true, diff, nil)
		envelope.ContractRevision = target.ContractRevision
		if err := json.NewEncoder(stdout).Encode(envelope); err != nil {
			return err
		}
	} else if output == "human" && !quiet {
		_, err = fmt.Fprintf(stdout, "%d compatible, %d breaking, %d migration required, %d unknown\n", diff.Summary.Compatible, diff.Summary.Breaking, diff.Summary.MigrationRequired, diff.Summary.Unknown)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported output %q", output)
	}
	if exitCode && len(diff.Changes) > 0 {
		return &silentCLIError{err: fmt.Errorf("semantic differences found")}
	}
	return nil
}

func runContractGenerate(stdout io.Writer, args []string) error {
	opts := contractOptions{Output: "human"}
	var target string
	var materialize bool
	var pruneMaterializedGo bool
	var mergeEditorWorkspace bool
	flags := newCLIFlagSet("generate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "o", opts.Output, "")
	flags.StringVar(&target, "target", "", "")
	flags.BoolVar(&opts.Check, "check", false, "")
	flags.BoolVar(&materialize, "materialize", false, "")
	flags.BoolVar(&pruneMaterializedGo, "prune-materialized-go", false, "")
	flags.BoolVar(&mergeEditorWorkspace, "merge-editor-workspace", false, "")
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
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	if target != "" && target != "go" && target != "contracts" && !strings.HasPrefix(target, "typescript_client.") {
		return fmt.Errorf("unknown generation target %q", target)
	}
	root, err := findContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	var result generate.GenerateResult
	var generateErr error
	if pruneMaterializedGo {
		if target != "" || materialize {
			return fmt.Errorf("--prune-materialized-go cannot be combined with --target or --materialize")
		}
		result, generateErr = generate.PruneMaterializedGo(root, opts.Check)
	} else if strings.HasPrefix(target, "typescript_client.") {
		result, generateErr = generate.GenerateTypeScriptClients(root, target, opts.Check)
	} else if target == "" {
		if materialize {
			return fmt.Errorf("--materialize requires --target contracts")
		}
		result, generateErr = generate.GenerateAll(root, opts.Check)
	} else {
		if !materialize {
			return fmt.Errorf("--target %s requires --materialize", target)
		}
		result, generateErr = generate.GenerateGoContracts(root, opts.Check)
	}
	var compilation *compiler.Result
	if generateErr == nil {
		compilation, generateErr = compiler.Compile(root)
		if generateErr == nil && compilation.Valid() {
			if mergeEditorWorkspace {
				generateErr = generate.SyncEditorWorkspaceMerge(compilation)
			} else {
				generateErr = generate.SyncEditorWorkspace(compilation)
			}
		}
	}
	if opts.Output == "json" {
		if compilation == nil {
			compilation, _ = compiler.Compile(root)
		}
		envelope := newCLIEnvelope(generateErr == nil, map[string]any{"target": target, "generation": result}, nil)
		if compilation != nil {
			envelope.WorkspaceRevision = compilation.WorkspaceRevision
			if compilation.Manifest != nil {
				envelope.ContractRevision = compilation.Manifest.ContractRevision
			}
			if len(compilation.ImplementationRevisions) > 0 {
				envelope.ImplementationRevision = compilation.ImplementationRevisions
			}
			if len(compilation.DeploymentRevisions) > 0 {
				envelope.DeploymentRevision = compilation.DeploymentRevisions
			}
			envelope.Diagnostics = append(envelope.Diagnostics, compilation.Diagnostics...)
		}
		if generateErr != nil {
			envelope.Diagnostics = append(envelope.Diagnostics, graph.Diagnostic{Code: "SCN6207", Severity: "error", Message: generateErr.Error()})
		}
		_ = json.NewEncoder(stdout).Encode(envelope)
	}
	if generateErr != nil {
		if opts.Check && len(result.Changed) > 0 {
			if opts.Output == "json" {
				return &silentCLIError{err: generateErr, code: 1}
			}
			return &codedCLIError{err: generateErr, code: 1}
		}
		if opts.Output == "json" {
			return &silentCLIError{err: generateErr, code: cliExitCode(generateErr)}
		}
		return generateErr
	}
	if opts.Output == "human" && !opts.Quiet {
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
		return fmt.Errorf("unsupported output %q", output)
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
		if pathExistsLocal(filepath.Join(abs, "scenery.scn")) {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no scenery.scn found from %s", start)
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
		return fmt.Errorf("unexpected argument %q", positionals[0])
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
	// compact manifest summary. The full graph stays on `scenery compile`,
	// `list`, `get`, and `explain`.
	data := map[string]any{"contract_status": result.ContractStatus, "implementation_status": result.ImplementationStatus, "manifest_summary": contractManifestSummary(result.Manifest), "http_surface_revision": result.HTTPSurfaceRevisions, "openapi_revision": result.OpenAPIRevisions}
	if result.PartialGraph != nil {
		data["partial_graph"] = result.PartialGraph
	}
	return writeContractResult(stdout, opts.Output, opts.Quiet, result, data)
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
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	result, err := compileContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	if result.Valid() {
		if err := generate.SyncEditorWorkspace(result); err != nil {
			return err
		}
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
	return writeContractResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runContractList(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("list", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery list KIND")
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
	return writeContractResult(stdout, opts.Output, opts.Quiet, result, map[string]any{"view": opts.View, "resources": resources})
}

func runContractGet(stdout io.Writer, args []string, explain bool) error {
	name := "get"
	if explain {
		name = "explain"
	}
	opts, positionals, err := parseContractOptions(name, args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery %s ADDRESS", name)
	}
	result, err := compileContractRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	manifest, err := result.ManifestForView(opts.View)
	if err != nil {
		return err
	}
	var found *graph.Resource
	if manifest != nil {
		for i := range manifest.Resources {
			if manifest.Resources[i].Address == positionals[0] {
				found = &manifest.Resources[i]
				break
			}
		}
	}
	if found == nil {
		return fmt.Errorf("resource %q not found", positionals[0])
	}
	data := map[string]any{"view": opts.View, "resource": found}
	if explain {
		data["provenance"] = found.Origin
	}
	return writeContractResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runContractSchema(stdout io.Writer, args []string) error {
	opts, positionals, err := parseContractOptions("schema", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery schema KIND")
	}
	schema, ok := contractagent.AgentSchema(positionals[0])
	if !ok {
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
	if opts.Output == "json" {
		diagnostics := []graph.Diagnostic{}
		if formatErr != nil {
			diagnostics = append(diagnostics, graph.Diagnostic{Code: "SCN1019", Severity: "error", Message: formatErr.Error()})
		}
		_ = json.NewEncoder(stdout).Encode(newCLIEnvelope(formatErr == nil, result, diagnostics))
	}
	if formatErr != nil {
		if opts.Check && len(result.Changed) > 0 {
			return &silentCLIError{err: formatErr, code: 1}
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
