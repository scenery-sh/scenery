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

	"scenery.sh/internal/vnext"
)

type vnextEnvelope struct {
	APIVersion             string             `json:"api_version"`
	DiagnosticCatalog      string             `json:"diagnostic_catalog"`
	OK                     bool               `json:"ok"`
	WorkspaceRevision      any                `json:"workspace_revision"`
	ContractRevision       any                `json:"contract_revision"`
	ImplementationRevision any                `json:"implementation_revision"`
	DeploymentRevision     any                `json:"deployment_revision"`
	Data                   any                `json:"data"`
	Diagnostics            []vnext.Diagnostic `json:"diagnostics"`
}

type vnextOptions struct {
	AppRoot        string
	Output         string
	View           string
	Module         string
	Check          bool
	NonInteractive bool
	Quiet          bool
}

func compileCommand(args []string) error { return runVNextCompile(os.Stdout, args) }
func schemaCommand(args []string) error  { return runVNextSchema(os.Stdout, args) }
func listCommand(args []string) error    { return runVNextList(os.Stdout, args) }
func getCommand(args []string) error     { return runVNextGet(os.Stdout, args, false) }
func explainCommand(args []string) error { return runVNextGet(os.Stdout, args, true) }
func fmtCommand(args []string) error     { return runVNextFmt(os.Stdout, args) }
func diffCommand(args []string) error    { return runVNextDiff(os.Stdout, args) }
func graphCommand(args []string) error   { return runVNextGraph(os.Stdout, args) }
func changesCommand(args []string) error { return runVNextChanges(os.Stdout, args) }
func runVNextAgentServer(stdin io.Reader, stdout io.Writer, args []string) error {
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
	session := vnext.NewAgentSession()
	for scanner.Scan() {
		var request vnext.AgentRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if encodeErr := encoder.Encode(vnext.AgentResponse{JSONRPC: "2.0", Error: &vnext.AgentError{Code: -32700, Kind: "invalid_request", Message: "invalid JSON"}}); encodeErr != nil {
				return encodeErr
			}
			continue
		}
		result, compileErr := compileVNextRoot(appRoot)
		if compileErr != nil {
			return compileErr
		}
		if err := encoder.Encode(session.Handle(result, request)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func runVNextChanges(stdout io.Writer, args []string) error {
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
	if err := validateVNextOutput(output); err != nil {
		return err
	}
	root, err := vnextRoot(appRoot)
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
		plan, err := vnext.PlanChanges(root, vnext.ChangeRequest{BaseWorkspaceRevision: baseWorkspace, BaseContractRevision: revisionFlag(baseContract), Caller: "local", Operations: operations})
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
			return json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, WorkspaceRevision: plan.BaseWorkspaceRevision, ContractRevision: plan.BaseContractRevision, ImplementationRevision: nil, DeploymentRevision: nil, Data: plan, Diagnostics: []vnext.Diagnostic{}})
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
		var plan vnext.ChangePlan
		if err := readExactVNextPlanFile(planPath, "change plan", &plan); err != nil {
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
		receipt, err := vnext.ApplyChangePlanWithOptions(root, plan, vnext.ApplyOptions{ExpectedWorkspaceRevision: expectWorkspace, ExpectedContractRevision: revisionFlag(expectContract), Caller: "local", ApprovalTokens: approvalTokens, VerifyApproval: approvalVerifier})
		if err != nil {
			return err
		}
		if output == "json" {
			return json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, WorkspaceRevision: receipt.WorkspaceRevision, ContractRevision: receipt.ContractRevision, ImplementationRevision: nil, DeploymentRevision: nil, Data: receipt, Diagnostics: []vnext.Diagnostic{}})
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, receipt.PlanID)
		}
		return err
	case "rename":
		if len(positionals) != 2 {
			return fmt.Errorf("usage: scenery changes rename ADDRESS NEW_NAME [--dry-run] [-o json]")
		}
		base, err := vnext.Compile(root)
		if err != nil {
			return err
		}
		if !base.Valid() {
			return fmt.Errorf("current contract is invalid")
		}
		plan, err := vnext.PlanChanges(root, vnext.ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: revisionFlag(base.Manifest.ContractRevision), Caller: "local", Operations: []vnext.SemanticOperation{{Op: "resource.rename", Address: positionals[0], Value: positionals[1]}}})
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
			receipt, applyErr := vnext.ApplyChangePlanWithOptions(root, plan, vnext.ApplyOptions{
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
			return json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, WorkspaceRevision: workspaceRevision, ContractRevision: contractRevision, ImplementationRevision: nil, DeploymentRevision: nil, Data: data, Diagnostics: []vnext.Diagnostic{}})
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, plan.PlanID)
		}
		return err
	default:
		return fmt.Errorf("unknown scenery changes subcommand %q", subcommand)
	}
}

func readSemanticOperations(path string) ([]vnext.SemanticOperation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var operations []vnext.SemanticOperation
	if err := json.Unmarshal(b, &operations); err == nil {
		return operations, nil
	}
	var request vnext.ChangeRequest
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

func runVNextGraph(stdout io.Writer, args []string) error {
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
	if err := validateVNextOutput(output); err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery graph ADDRESS [--direction dependencies|dependents|both]")
	}
	result, err := compileVNextRoot(appRoot)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return writeVNextResult(stdout, output, quiet, result, nil)
	}
	manifest, err := result.ManifestForView(view)
	if err != nil {
		return err
	}
	graph, err := vnext.Graph(manifest, positionals[0], vnext.GraphOptions{Direction: direction, Depth: depth, MaxResources: maxResources})
	if err != nil {
		return err
	}
	return writeVNextResult(stdout, output, quiet, result, graph)
}

func runVNextDiff(stdout io.Writer, args []string) error {
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
	if err := validateVNextOutput(output); err != nil {
		return err
	}
	if !semantic || len(positionals) != 2 {
		return fmt.Errorf("usage: scenery diff --semantic BASE TARGET [--rename-receipts change-plan-or-receipt.json] [-o human|json]")
	}
	base, err := vnext.LoadManifestReference(positionals[0])
	if err != nil {
		return fmt.Errorf("load base: %w", err)
	}
	target, err := vnext.LoadManifestReference(positionals[1])
	if err != nil {
		return fmt.Errorf("load target: %w", err)
	}
	var renames []vnext.RenameReceipt
	if renameReceiptsPath != "" {
		renames, err = vnext.LoadRenameReceipts(renameReceiptsPath)
		if err != nil {
			return fmt.Errorf("load rename receipts: %w", err)
		}
	}
	for _, reference := range []string{positionals[1], positionals[0]} {
		if info, statErr := os.Stat(reference); statErr == nil && info.IsDir() {
			persisted, loadErr := vnext.LoadAppliedRenameReceipts(reference, base, target)
			if loadErr != nil {
				return fmt.Errorf("load applied rename receipts: %w", loadErr)
			}
			renames = append(renames, persisted...)
			break
		}
	}
	diff := vnext.CompareManifests(base, target, vnext.CompareOptions{View: view, Renames: renames})
	if output == "json" {
		envelope := vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, ContractRevision: target.ContractRevision, ImplementationRevision: nil, DeploymentRevision: nil, Data: diff, Diagnostics: []vnext.Diagnostic{}}
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

func runVNextGenerate(stdout io.Writer, args []string) error {
	opts := vnextOptions{Output: "human"}
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
	if err := validateVNextOutput(opts.Output); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	if target != "" && target != "go" && target != "contracts" && !strings.HasPrefix(target, "typescript_client.") {
		return fmt.Errorf("unknown vNext generation target %q", target)
	}
	root, err := vnextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	var result vnext.GenerateResult
	var generateErr error
	if strings.HasPrefix(target, "typescript_client.") {
		result, generateErr = vnext.GenerateTypeScriptClients(root, target, opts.Check)
	} else if target == "" {
		result, generateErr = vnext.GenerateAll(root, opts.Check)
	} else {
		result, generateErr = vnext.GenerateGoContracts(root, opts.Check)
	}
	if opts.Output == "json" {
		compilation, _ := vnext.Compile(root)
		envelope := vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: generateErr == nil, ContractRevision: nil, ImplementationRevision: nil, DeploymentRevision: nil, Data: map[string]any{"target": target, "generation": result}, Diagnostics: []vnext.Diagnostic{}}
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
			envelope.Diagnostics = append(envelope.Diagnostics, vnext.Diagnostic{Code: "SCN6207", Severity: "error", Message: generateErr.Error()})
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

func parseVNextOptions(name string, args []string) (vnextOptions, []string, error) {
	opts := vnextOptions{Output: "human", View: "expanded"}
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
		return vnextOptions{}, nil, err
	}
	if err := validateVNextOutput(opts.Output); err != nil {
		return vnextOptions{}, nil, err
	}
	if opts.View != "source" && opts.View != "effective" && opts.View != "expanded" {
		return vnextOptions{}, nil, fmt.Errorf("invalid_request: unsupported graph view %q", opts.View)
	}
	return opts, positionals, nil
}

func validateVNextOutput(output string) error {
	if output != "human" && output != "json" {
		return fmt.Errorf("unsupported output %q", output)
	}
	return nil
}

func vnextRoot(value string) (string, error) {
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

func runVNextCheck(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("check", args)
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	root, err := vnextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	result, err := vnext.Check(root)
	if err != nil {
		return err
	}
	data := map[string]any{"contract_status": result.ContractStatus, "implementation_status": result.ImplementationStatus, "manifest": result.Manifest, "http_surface_revision": result.HTTPSurfaceRevisions, "openapi_revision": result.OpenAPIRevisions}
	if result.PartialGraph != nil {
		data["partial_graph"] = result.PartialGraph
	}
	return writeVNextResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runVNextCompile(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("compile", args)
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("unexpected argument %q", positionals[0])
	}
	result, err := compileVNextRoot(opts.AppRoot)
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
	return writeVNextResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runVNextList(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("list", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery list KIND")
	}
	result, err := compileVNextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	manifest, err := result.ManifestForView(opts.View)
	if err != nil {
		return err
	}
	var resources []vnext.Resource
	if manifest != nil {
		for _, resource := range manifest.Resources {
			if resourceKindMatches(resource, positionals[0]) && (opts.Module == "" || resource.Module == opts.Module) {
				resources = append(resources, resource)
			}
		}
	}
	return writeVNextResult(stdout, opts.Output, opts.Quiet, result, map[string]any{"view": opts.View, "resources": resources})
}

func runVNextGet(stdout io.Writer, args []string, explain bool) error {
	name := "get"
	if explain {
		name = "explain"
	}
	opts, positionals, err := parseVNextOptions(name, args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery %s ADDRESS", name)
	}
	result, err := compileVNextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	manifest, err := result.ManifestForView(opts.View)
	if err != nil {
		return err
	}
	var found *vnext.Resource
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
	return writeVNextResult(stdout, opts.Output, opts.Quiet, result, data)
}

func runVNextSchema(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("schema", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery schema KIND")
	}
	schema, ok := vnext.AgentSchema(positionals[0])
	if !ok {
		return fmt.Errorf("schema %q not found", positionals[0])
	}
	if opts.Output == "json" {
		return json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, ImplementationRevision: nil, DeploymentRevision: nil, Data: schema, Diagnostics: []vnext.Diagnostic{}})
	}
	b, _ := json.MarshalIndent(schema, "", "  ")
	if opts.Quiet {
		return nil
	}
	_, err = fmt.Fprintln(stdout, string(b))
	return err
}

func runVNextFmt(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("fmt", args)
	if err != nil {
		return err
	}
	root, err := vnextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	result, formatErr := vnext.FormatPaths(root, positionals, opts.Check)
	if opts.Output == "json" {
		diagnostics := []vnext.Diagnostic{}
		if formatErr != nil {
			diagnostics = append(diagnostics, vnext.Diagnostic{Code: "SCN1019", Severity: "error", Message: formatErr.Error()})
		}
		_ = json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: formatErr == nil, ContractRevision: nil, ImplementationRevision: nil, DeploymentRevision: nil, Data: result, Diagnostics: diagnostics})
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
