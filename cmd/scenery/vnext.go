package main

import (
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
	WorkspaceRevision      string             `json:"workspace_revision,omitempty"`
	ContractRevision       string             `json:"contract_revision,omitempty"`
	ImplementationRevision any                `json:"implementation_revision"`
	DeploymentRevision     any                `json:"deployment_revision"`
	Data                   any                `json:"data"`
	Diagnostics            []vnext.Diagnostic `json:"diagnostics"`
}

type vnextOptions struct {
	AppRoot string
	Output  string
	View    string
	Module  string
	Check   bool
}

func compileCommand(args []string) error { return runVNextCompile(os.Stdout, args) }
func schemaCommand(args []string) error  { return runVNextSchema(os.Stdout, args) }
func listCommand(args []string) error    { return runVNextList(os.Stdout, args) }
func getCommand(args []string) error     { return runVNextGet(os.Stdout, args, false) }
func explainCommand(args []string) error { return runVNextGet(os.Stdout, args, true) }
func fmtCommand(args []string) error     { return runVNextFmt(os.Stdout, args) }
func migrateCommand(args []string) error { return runVNextMigrate(os.Stdout, args) }

func runVNextGenerate(stdout io.Writer, args []string) error {
	opts := vnextOptions{Output: "human"}
	var target string
	flags := newCLIFlagSet("generate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "o", opts.Output, "")
	flags.StringVar(&target, "target", "", "")
	flags.BoolVar(&opts.Check, "check", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
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
		goResult, goErr := vnext.GenerateGoContracts(root, opts.Check)
		tsResult, tsErr := vnext.GenerateTypeScriptClients(root, "", opts.Check)
		result.Changed = append(goResult.Changed, tsResult.Changed...)
		result.Checked = append(goResult.Checked, tsResult.Checked...)
		sort.Strings(result.Changed)
		sort.Strings(result.Checked)
		if goErr != nil {
			generateErr = goErr
		} else {
			generateErr = tsErr
		}
	} else {
		result, generateErr = vnext.GenerateGoContracts(root, opts.Check)
	}
	if opts.Output == "json" {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"api_version": "scenery.generate.vnext.v1", "ok": generateErr == nil, "target": target, "data": result})
	}
	if generateErr != nil {
		return generateErr
	}
	if opts.Output == "human" {
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
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return vnextOptions{}, nil, err
	}
	if opts.Output != "human" && opts.Output != "json" {
		return vnextOptions{}, nil, fmt.Errorf("unsupported output %q", opts.Output)
	}
	return opts, positionals, nil
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
	result, err := compileVNextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	data := map[string]any{"contract_status": map[bool]string{true: "valid", false: "invalid"}[result.Valid()], "implementation_status": "not_requested", "manifest": result.Manifest}
	return writeVNextResult(stdout, opts.Output, result, data)
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
	return writeVNextResult(stdout, opts.Output, result, map[string]any{"contract_status": map[bool]string{true: "valid", false: "invalid"}[result.Valid()], "implementation_status": "not_requested", "manifest": result.Manifest})
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
	var resources []vnext.Resource
	if result.Manifest != nil {
		for _, resource := range result.Manifest.Resources {
			if resourceKindMatches(resource, positionals[0]) && (opts.Module == "" || resource.Module == opts.Module) {
				resources = append(resources, resource)
			}
		}
	}
	return writeVNextResult(stdout, opts.Output, result, map[string]any{"view": opts.View, "resources": resources})
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
	var found *vnext.Resource
	if result.Manifest != nil {
		for i := range result.Manifest.Resources {
			if result.Manifest.Resources[i].Address == positionals[0] {
				found = &result.Manifest.Resources[i]
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
	return writeVNextResult(stdout, opts.Output, result, data)
}

func runVNextSchema(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("schema", args)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: scenery schema KIND")
	}
	schema, ok := vnext.CoreSchema(positionals[0])
	if !ok {
		return fmt.Errorf("schema %q not found", positionals[0])
	}
	if opts.Output == "json" {
		return json.NewEncoder(stdout).Encode(vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true, ImplementationRevision: nil, DeploymentRevision: nil, Data: schema, Diagnostics: []vnext.Diagnostic{}})
	}
	b, _ := json.MarshalIndent(schema, "", "  ")
	_, err = fmt.Fprintln(stdout, string(b))
	return err
}

func runVNextFmt(stdout io.Writer, args []string) error {
	opts, positionals, err := parseVNextOptions("fmt", args)
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("path-scoped formatting is not implemented yet")
	}
	root, err := vnextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	result, formatErr := vnext.Format(root, opts.Check)
	if opts.Output == "json" {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"api_version": "scenery.cli.v1", "ok": formatErr == nil, "data": result})
	}
	if formatErr != nil {
		return formatErr
	}
	if opts.Output == "human" {
		if len(result.Changed) == 0 {
			_, _ = fmt.Fprintln(stdout, "scenery: format ok")
		} else {
			sort.Strings(result.Changed)
			_, _ = fmt.Fprintln(stdout, strings.Join(result.Changed, "\n"))
		}
	}
	return nil
}

func runVNextMigrate(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing migrate command")
	}
	subject := args[0]
	opts := vnextOptions{Output: "human"}
	var dryRun, native, generate, shadow bool
	flags := newCLIFlagSet("migrate " + subject)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Output, "o", opts.Output, "")
	flags.BoolVar(&dryRun, "dry-run", false, "")
	flags.BoolVar(&native, "native", false, "")
	flags.BoolVar(&generate, "generate", false, "")
	flags.BoolVar(&shadow, "shadow", false, "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	result, err := compileVNextRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	status := vnext.BuildMigrationStatus(result)
	data := any(status)
	switch subject {
	case "status":
		if len(positionals) > 0 {
			return fmt.Errorf("unexpected argument %q", positionals[0])
		}
	case "verify":
		if len(positionals) != 1 {
			return fmt.Errorf("usage: scenery migrate verify SERVICE")
		}
		service, serviceErr := status.Service(positionals[0])
		err = serviceErr
		if err != nil {
			return err
		}
		remainingLegacyAdapters := countLegacyAdapters(result, service.Name)
		contractReady := result.Valid() && service.Active == "native"
		data = map[string]any{
			"api_version":               "scenery.migrate.verify.v1",
			"service":                   service,
			"contract_valid":            result.Valid(),
			"contract_ready":            contractReady,
			"retirement_ready":          contractReady && remainingLegacyAdapters == 0,
			"remaining_legacy_adapters": remainingLegacyAdapters,
			"ready":                     contractReady,
		}
	case "compare":
		if len(positionals) != 1 {
			return fmt.Errorf("usage: scenery migrate compare SERVICE")
		}
		service, serviceErr := status.Service(positionals[0])
		if serviceErr != nil {
			return serviceErr
		}
		data = map[string]any{"api_version": "scenery.migrate.compare.v1", "service": service.Name, "state": service.State, "active": service.Active, "equal": result.Valid(), "differences": []any{}, "contract_revision": status.ContractRevision}
	case "service":
		if len(positionals) != 1 || (!generate && !shadow) {
			return fmt.Errorf("usage: scenery migrate service SERVICE --generate|--shadow [--dry-run]")
		}
		service, serviceErr := status.Service(positionals[0])
		if serviceErr != nil {
			return serviceErr
		}
		data = map[string]any{"api_version": "scenery.migrate.service-plan.v1", "service": service, "generate": generate, "shadow": shadow, "dry_run": dryRun, "writes": []string{}}
	case "activate":
		if len(positionals) != 1 || !native {
			return fmt.Errorf("usage: scenery migrate activate SERVICE --native [--dry-run]")
		}
		service, serviceErr := status.Service(positionals[0])
		if serviceErr != nil {
			return serviceErr
		}
		if service.Active != "native" {
			return fmt.Errorf("service %s is not ready for idempotent native activation; author a validated shadow candidate first", service.Name)
		}
		data = map[string]any{"api_version": "scenery.migrate.activation-receipt.v1", "service": service.Name, "active": "native", "dry_run": dryRun, "workspace_revision": status.WorkspaceRevision, "contract_revision": status.ContractRevision, "changed": false, "rollback_safe": service.State == "shadow"}
	case "finish":
		if len(positionals) > 0 {
			return fmt.Errorf("unexpected argument %q", positionals[0])
		}
		legacyCount := 0
		for _, service := range status.Services {
			if service.Active == "legacy" || service.State == "shadow" {
				legacyCount++
			}
		}
		data = map[string]any{"api_version": "scenery.migrate.finish.v1", "ready": legacyCount == 0, "remaining_services": legacyCount}
		if legacyCount > 0 && opts.Output == "human" {
			return fmt.Errorf("migration cannot finish: %d legacy or shadow services remain", legacyCount)
		}
	case "init":
		if len(positionals) > 0 {
			return fmt.Errorf("unexpected argument %q", positionals[0])
		}
		data = map[string]any{"api_version": "scenery.migrate.init.v1", "initialized": result.Migration != nil, "changed": false, "message": "workspace already has explicit native and migration source"}
	default:
		return fmt.Errorf("migrate %s is not implemented yet", subject)
	}
	if opts.Output == "json" {
		return json.NewEncoder(stdout).Encode(data)
	}
	_, err = fmt.Fprintf(stdout, "mode=%s ready=%t services=%d\n", status.Mode, status.Ready, len(status.Services))
	return err
}

func countLegacyAdapters(result *vnext.Result, module string) int {
	count := 0
	if result == nil || result.Manifest == nil {
		return 0
	}
	for _, resource := range result.Manifest.Resources {
		if resource.Module != module || resource.Kind != "scenery.operation/v1" {
			continue
		}
		handler, _ := resource.Spec["handler"].(map[string]any)
		if adapter, _ := handler["adapter"].(string); adapter == "legacy_go_v0" {
			count++
		}
	}
	return count
}

func compileVNextRoot(value string) (*vnext.Result, error) {
	root, err := vnextRoot(value)
	if err != nil {
		return nil, err
	}
	return vnext.Compile(root)
}

func writeVNextResult(stdout io.Writer, output string, result *vnext.Result, data any) error {
	if output == "json" {
		env := vnextEnvelope{APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: result.Valid(), WorkspaceRevision: result.WorkspaceRevision, ImplementationRevision: nil, DeploymentRevision: nil, Data: data, Diagnostics: result.Diagnostics}
		if result.Manifest != nil {
			env.ContractRevision = result.Manifest.ContractRevision
		}
		if err := json.NewEncoder(stdout).Encode(env); err != nil {
			return err
		}
		if !env.OK {
			return &silentCLIError{err: fmt.Errorf("vNext compilation failed")}
		}
		return nil
	}
	if !result.Valid() {
		for _, diag := range result.Diagnostics {
			if diag.Severity == "error" {
				_, _ = fmt.Fprintf(stdout, "%s: %s\n", diag.Code, diag.Message)
			}
		}
		return fmt.Errorf("vNext compilation failed")
	}
	_, err := fmt.Fprintln(stdout, "scenery: vNext contract ok", result.Manifest.ContractRevision)
	return err
}

func resourceKindMatches(resource vnext.Resource, value string) bool {
	return resource.Kind == value || strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery.") == strings.ReplaceAll(value, "_", "-")
}
func pathExistsLocal(path string) bool { _, err := os.Stat(path); return err == nil }

func hasCLIArg(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func isVNextGenerate(args []string) bool {
	root := "."
	for i, arg := range args {
		if arg == "client" || arg == "sqlc" || arg == "data" {
			return false
		}
		if arg == "--app-root" && i+1 < len(args) {
			root = args[i+1]
		}
		if strings.HasPrefix(arg, "--app-root=") {
			root = strings.TrimPrefix(arg, "--app-root=")
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return pathExistsLocal(filepath.Join(abs, "scenery.scn"))
}
