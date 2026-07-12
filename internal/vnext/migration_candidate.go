package vnext

import (
	"encoding/json"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

type MigrationCandidateRequest struct {
	Service               string `json:"service"`
	Caller                string `json:"caller"`
	BaseWorkspaceRevision string `json:"base_workspace_revision"`
	BaseContractRevision  string `json:"base_contract_revision"`
}

type MigrationCandidatePlan struct {
	APIVersion                 string       `json:"api_version"`
	PlanID                     string       `json:"plan_id"`
	Service                    string       `json:"service"`
	Caller                     string       `json:"caller"`
	BaseWorkspaceRevision      string       `json:"base_workspace_revision"`
	BaseContractRevision       string       `json:"base_contract_revision"`
	PredictedWorkspaceRevision string       `json:"predicted_workspace_revision"`
	PredictedContractRevision  string       `json:"predicted_contract_revision"`
	LegacyCandidateDigest      string       `json:"legacy_candidate_digest"`
	NativeCandidateDigest      string       `json:"native_candidate_digest"`
	Diagnostics                []Diagnostic `json:"diagnostics"`
	Edits                      []SourceEdit `json:"source_edits"`
	ExpiresAt                  time.Time    `json:"expires_at"`
}

type MigrationCandidateApplyOptions struct {
	ExpectedWorkspaceRevision string
	ExpectedContractRevision  string
	Caller                    string
}

type MigrationCandidateReceipt struct {
	APIVersion            string       `json:"api_version"`
	PlanID                string       `json:"plan_id"`
	Service               string       `json:"service"`
	Active                string       `json:"active"`
	WorkspaceRevision     string       `json:"workspace_revision"`
	ContractRevision      string       `json:"contract_revision"`
	NativeCandidateDigest string       `json:"native_candidate_digest"`
	Diagnostics           []Diagnostic `json:"diagnostics"`
}

func PlanMigrationCandidate(root string, request MigrationCandidateRequest) (MigrationCandidatePlan, error) {
	base, err := compileContractGraph(root, false)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	if !base.Valid() || base.Manifest == nil || base.Migration == nil {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: candidate generation requires a valid mixed-mode graph")
	}
	if request.BaseWorkspaceRevision != base.WorkspaceRevision || request.BaseContractRevision != base.Manifest.ContractRevision {
		return MigrationCandidatePlan{}, fmt.Errorf("revision_conflict: migration candidate base revisions changed")
	}
	service, err := BuildMigrationStatus(base).Service(request.Service)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	if service.Active != "legacy" {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: candidate generation requires legacy ownership")
	}
	if len(base.Migration.NativeCandidates[service.Name]) > 0 {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: migration service %s already has a native candidate", service.Name)
	}
	packageSource, requiresAuthentication, diagnostics, err := renderMigrationCandidatePackage(base.Root, service, base.Manifest.Profiles)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	temp, err := cloneWorkspace(base.Root)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	defer os.RemoveAll(temp)
	packagePath := filepath.Join(temp, filepath.FromSlash(strings.TrimPrefix(service.Package, "./")), "scenery.package.scn")
	if pathExists(packagePath) {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: %s already exists", filepath.ToSlash(packagePath))
	}
	if err := atomicWrite(packagePath, packageSource); err != nil {
		return MigrationCandidatePlan{}, err
	}
	if err := appendMigrationCandidateModule(temp, service, requiresAuthentication); err != nil {
		return MigrationCandidatePlan{}, err
	}
	if err := mutateMigrationOwnership(temp, service.Name, "shadow"); err != nil {
		return MigrationCandidatePlan{}, err
	}
	if _, err := Format(temp, false); err != nil {
		return MigrationCandidatePlan{}, err
	}
	predicted, err := Compile(temp)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	if !predicted.Valid() || predicted.Manifest == nil {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: generated native candidate is invalid: %s", firstError(predicted.Diagnostics))
	}
	predictedService, err := BuildMigrationStatus(predicted).Service(service.Name)
	if err != nil || predictedService.NativeCandidateDigest == "" {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: generated native candidate was not linked")
	}
	if !predictedService.NativeCandidateValid {
		return MigrationCandidatePlan{}, fmt.Errorf("failed_precondition: generated native candidate is invalid: %s", firstError(predictedService.CandidateDiagnostics["native"]))
	}
	edits, err := changedWorkspaceFiles(base.Root, temp)
	if err != nil {
		return MigrationCandidatePlan{}, err
	}
	caller := strings.TrimSpace(request.Caller)
	if caller == "" {
		caller = "local"
	}
	plan := MigrationCandidatePlan{
		APIVersion:                 "scenery.migrate.candidate-plan.v1",
		Service:                    service.Name,
		Caller:                     caller,
		BaseWorkspaceRevision:      base.WorkspaceRevision,
		BaseContractRevision:       base.Manifest.ContractRevision,
		PredictedWorkspaceRevision: predicted.WorkspaceRevision,
		PredictedContractRevision:  predicted.Manifest.ContractRevision,
		LegacyCandidateDigest:      service.LegacyCandidateDigest,
		NativeCandidateDigest:      predictedService.NativeCandidateDigest,
		Diagnostics:                diagnostics,
		Edits:                      edits,
		ExpiresAt:                  time.Now().UTC().Add(15 * time.Minute),
	}
	plan.PlanID = migrationCandidatePlanID(plan)
	if err := retainIssuedPlan(root, issuedMigrationCandidatePlan, plan.PlanID, plan); err != nil {
		return MigrationCandidatePlan{}, err
	}
	return plan, nil
}

func ApplyMigrationCandidate(root string, plan MigrationCandidatePlan, options MigrationCandidateApplyOptions) (MigrationCandidateReceipt, error) {
	if err := requireIssuedPlan(root, issuedMigrationCandidatePlan, plan.PlanID, plan); err != nil {
		return MigrationCandidateReceipt{}, err
	}
	if time.Now().UTC().After(plan.ExpiresAt) {
		return MigrationCandidateReceipt{}, fmt.Errorf("failed_precondition: migration candidate plan expired")
	}
	if plan.PlanID == "" || migrationCandidatePlanID(plan) != plan.PlanID {
		return MigrationCandidateReceipt{}, fmt.Errorf("failed_precondition: migration candidate plan identity mismatch")
	}
	if options.Caller != plan.Caller || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision || options.ExpectedContractRevision != plan.BaseContractRevision {
		return MigrationCandidateReceipt{}, fmt.Errorf("revision_conflict: migration candidate plan binding changed")
	}
	if pathExists(migrationCandidateReceiptPath(root, plan.PlanID)) {
		return MigrationCandidateReceipt{}, fmt.Errorf("failed_precondition: migration candidate plan was already applied")
	}
	current, err := compileContractGraph(root, false)
	if err != nil || !current.Valid() || current.Manifest == nil || current.WorkspaceRevision != plan.BaseWorkspaceRevision || current.Manifest.ContractRevision != plan.BaseContractRevision {
		return MigrationCandidateReceipt{}, fmt.Errorf("revision_conflict: migration candidate source changed")
	}
	service, err := BuildMigrationStatus(current).Service(plan.Service)
	if err != nil || service.LegacyCandidateDigest != plan.LegacyCandidateDigest || len(current.Migration.NativeCandidates[plan.Service]) > 0 {
		return MigrationCandidateReceipt{}, fmt.Errorf("revision_conflict: migration candidate ownership changed")
	}
	staged, err := cloneWorkspace(root)
	if err != nil {
		return MigrationCandidateReceipt{}, err
	}
	defer os.RemoveAll(staged)
	if err := applyPlannedEdits(staged, plan.Edits, true); err != nil {
		return MigrationCandidateReceipt{}, err
	}
	checked, checkedFiles, err := validateStagedWorkspace(staged, false)
	if err != nil || !checked.Valid() || checked.Manifest == nil || checked.WorkspaceRevision != plan.PredictedWorkspaceRevision || checked.Manifest.ContractRevision != plan.PredictedContractRevision {
		return MigrationCandidateReceipt{}, fmt.Errorf("failed_precondition: staged migration candidate no longer validates")
	}
	checkedService, err := BuildMigrationStatus(checked).Service(plan.Service)
	if err != nil || checkedService.NativeCandidateDigest != plan.NativeCandidateDigest {
		return MigrationCandidateReceipt{}, fmt.Errorf("revision_conflict: staged native candidate changed")
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.Edits, migrationCandidateReceiptPath(root, plan.PlanID))
	if err != nil {
		return MigrationCandidateReceipt{}, err
	}
	actual, err := revalidateCommittedResult(root, checked, checkedFiles)
	if err != nil || !actual.Valid() || actual.Manifest == nil || actual.WorkspaceRevision != plan.PredictedWorkspaceRevision || actual.Manifest.ContractRevision != plan.PredictedContractRevision {
		rollback()
		return MigrationCandidateReceipt{}, fmt.Errorf("internal: applied migration candidate revisions differ from plan")
	}
	actualService, err := BuildMigrationStatus(actual).Service(plan.Service)
	if err != nil || actualService.NativeCandidateDigest != plan.NativeCandidateDigest {
		rollback()
		return MigrationCandidateReceipt{}, fmt.Errorf("internal: applied native candidate differs from plan")
	}
	receipt := MigrationCandidateReceipt{
		APIVersion:            "scenery.migrate.candidate-receipt.v1",
		PlanID:                plan.PlanID,
		Service:               plan.Service,
		Active:                actualService.Active,
		WorkspaceRevision:     actual.WorkspaceRevision,
		ContractRevision:      actual.Manifest.ContractRevision,
		NativeCandidateDigest: actualService.NativeCandidateDigest,
		Diagnostics:           append([]Diagnostic(nil), plan.Diagnostics...),
	}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if err := atomicWriteSynced(migrationCandidateReceiptPath(root, plan.PlanID), append(encoded, '\n'), 0o644); err != nil {
		rollback()
		return MigrationCandidateReceipt{}, err
	}
	finalize()
	return receipt, nil
}

type migrationCandidateField struct {
	Name, WireName, Type, Source, SourceName string
}

type migrationCandidateOperation struct {
	Name, Method, Path, LegacyPath, Access string
	File, Receiver                         string
	Methods, Tags                          []string
	Input, Output                          []migrationCandidateField
	HasOutput, HasPayload                  bool
	Raw                                    bool
}

func renderMigrationCandidatePackage(root string, migrationService MigrationService, profiles []string) ([]byte, bool, []Diagnostic, error) {
	_, config, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return nil, false, nil, err
	}
	appModel, err := parse.AppPackages(root, config.Name, []string{migrationService.Package})
	if err != nil {
		return nil, false, nil, fmt.Errorf("discover legacy candidate: %w", err)
	}
	var legacyService *model.Service
	for _, service := range appModel.Services {
		if service.Name == migrationService.Name {
			legacyService = service
			break
		}
	}
	if legacyService == nil {
		return nil, false, nil, fmt.Errorf("failed_precondition: legacy service %s was not discovered", migrationService.Name)
	}
	goModBytes, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, false, nil, err
	}
	goMod, err := modfile.Parse("go.mod", goModBytes, nil)
	if err != nil || goMod.Module == nil {
		return nil, false, nil, fmt.Errorf("failed_precondition: go.mod requires a module path")
	}
	packageRelative := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(migrationService.Package)), "./")
	importPath := strings.TrimSuffix(goMod.Module.Mod.Path, "/") + "/" + packageRelative
	pathTailsEnabled := containsExactString(profiles, HTTPPathTailProfile) && containsExactString(profiles, RuntimeHTTPPathTailProfile)
	operations, diagnostics := migrationCandidateOperations(legacyService, pathTailsEnabled)
	requiresAuthentication := false
	for _, operation := range operations {
		requiresAuthentication = requiresAuthentication || operation.Access == "auth"
	}

	var source strings.Builder
	fmt.Fprintf(&source, "package %q {\n  version         = \"0.1.0\"\n  scenery_version = \">= 2.0.0, < 3.0.0\"\n\n  go_contract {\n    import_path = %q\n  }\n}\n\n", migrationService.Name, importPath)
	source.WriteString("input \"gateway\" {\n  type = resource_ref(\"http_gateway\")\n}\n\n")
	if requiresAuthentication {
		source.WriteString("input \"authentication\" {\n  type = resource_ref(\"authentication\")\n}\n\n")
	}
	fmt.Fprintf(&source, "service %q {\n  runtime = \"go\"\n\n  implementation {\n    adapter = \"legacy_go_v0\"\n    root    = %q\n  }\n}\n", migrationService.Name, packageRelative)
	for _, operation := range operations {
		renderMigrationCandidateOperation(&source, migrationService.Name, operation)
	}
	return []byte(source.String()), requiresAuthentication, diagnostics, nil
}

func migrationCandidateOperations(service *model.Service, pathTailsEnabled bool) ([]migrationCandidateOperation, []Diagnostic) {
	operations := make([]migrationCandidateOperation, 0, len(service.Endpoints))
	var diagnostics []Diagnostic
	seenNames := map[string]int{}
	for _, endpoint := range service.Endpoints {
		name := snakeName(endpoint.Name)
		seenNames[name]++
		if seenNames[name] > 1 {
			name += fmt.Sprintf("_%d", seenNames[name])
		}
		tailName, terminalTail := legacyTerminalPathTail(endpoint.Path)
		operationPath := legacyPathToNative(endpoint.Path)
		if terminalTail && pathTailsEnabled {
			operationPath = legacyCandidatePathToNative(endpoint.Path)
		}
		operation := migrationCandidateOperation{
			Name: name, Method: endpoint.Name, Path: operationPath, LegacyPath: endpoint.Path, Access: string(endpoint.Access),
			File: legacyModelFile(endpoint.Package, endpoint.File), Methods: append([]string(nil), endpoint.Methods...), Tags: append([]string(nil), endpoint.Tags...), HasPayload: endpoint.Payload != nil, Raw: endpoint.Raw,
		}
		if endpoint.Receiver != nil {
			operation.Receiver = endpoint.Receiver.TypeName
		}
		if endpoint.Raw || strings.Contains(endpoint.Path, "*") && (!terminalTail || !pathTailsEnabled) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5401", Severity: "warning", Message: "raw or unsupported wildcard legacy endpoint requires an explicit native rewrite", Address: resourceAddress(service.Name, "operation", name), Details: map[string]any{"migration_disposition": "rewrite_required"}})
			continue
		}
		unsupportedTail := false
		for index, parameter := range endpoint.PathParams {
			field := endpoint.Params[index+1]
			typeExpression, complete := migrationCandidateType(field.Type)
			if !complete {
				diagnostics = append(diagnostics, candidateTypeDiagnostic(service.Name, name, parameter.Name, field.TypeExpr))
			}
			name := snakeName(parameter.Name)
			source := "path"
			if terminalTail && parameter.Name == tailName {
				source = "path_tail"
				if typeExpression != "string" && typeExpression != "relative_path" && typeExpression != "optional(relative_path)" {
					unsupportedTail = true
				}
			}
			operation.Input = append(operation.Input, migrationCandidateField{Name: name, WireName: name, Type: typeExpression, Source: source, SourceName: name})
		}
		if unsupportedTail {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5401", Severity: "warning", Message: "legacy terminal wildcard target requires an explicitly supported path-tail type", Address: resourceAddress(service.Name, "operation", name), Details: map[string]any{"migration_disposition": "rewrite_required"}})
			continue
		}
		if terminalTail {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SCN5405", Severity: "warning", Message: "legacy terminal wildcard lowering requires behavioral comparison for slash and decoding parity",
				Address: resourceAddress(service.Name, "operation", name),
				Details: map[string]any{"migration_disposition": "advisory", "legacy_path": endpoint.Path, "native_path": operation.Path, "required_profile": HTTPPathTailProfile},
			})
		}
		if endpoint.Payload != nil {
			fields, fieldDiagnostics := migrationCandidatePayloadFields(service.Name, name, endpoint.Payload.Type)
			operation.Input = append(operation.Input, fields...)
			diagnostics = append(diagnostics, fieldDiagnostics...)
		}
		if endpoint.Response != nil {
			operation.HasOutput = true
			fields, fieldDiagnostics := migrationCandidateResultFields(service.Name, name, endpoint.Response.Type)
			operation.Output = append(operation.Output, fields...)
			diagnostics = append(diagnostics, fieldDiagnostics...)
		}
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
	return operations, diagnostics
}

func migrationCandidatePayloadFields(service, operation string, value types.Type) ([]migrationCandidateField, []Diagnostic) {
	base := value
	for {
		pointer, ok := base.(*types.Pointer)
		if !ok {
			break
		}
		base = pointer.Elem()
	}
	base = types.Unalias(base)
	if named, ok := base.(*types.Named); ok {
		base = named.Underlying()
	}
	structure, ok := base.(*types.Struct)
	if !ok {
		typeExpression, complete := migrationCandidateType(value)
		field := migrationCandidateField{Name: "payload", WireName: "payload", Type: typeExpression, Source: "body", SourceName: "payload"}
		if complete {
			return []migrationCandidateField{field}, nil
		}
		return []migrationCandidateField{field}, []Diagnostic{candidateTypeDiagnostic(service, operation, "payload", value.String())}
	}
	return migrationCandidateStructFields(service, operation, structure, true)
}

func migrationCandidateResultFields(service, operation string, value types.Type) ([]migrationCandidateField, []Diagnostic) {
	base := value
	for {
		pointer, ok := base.(*types.Pointer)
		if !ok {
			break
		}
		base = pointer.Elem()
	}
	base = types.Unalias(base)
	if named, ok := base.(*types.Named); ok {
		base = named.Underlying()
	}
	if structure, ok := base.(*types.Struct); ok {
		return migrationCandidateStructFields(service, operation, structure, false)
	}
	typeExpression, complete := migrationCandidateType(value)
	field := migrationCandidateField{Name: "value", WireName: "value", Type: typeExpression, Source: "body", SourceName: "value"}
	if complete {
		return []migrationCandidateField{field}, nil
	}
	return []migrationCandidateField{field}, []Diagnostic{candidateTypeDiagnostic(service, operation, "value", value.String())}
}

func migrationCandidateStructFields(service, operation string, structure *types.Struct, input bool) ([]migrationCandidateField, []Diagnostic) {
	var fields []migrationCandidateField
	var diagnostics []Diagnostic
	seen := map[string]int{}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() {
			continue
		}
		tag := reflect.StructTag(structure.Tag(index))
		if tag.Get("scenery") == "httpstatus" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5402", Severity: "warning", Message: "legacy implementation-selected HTTP status requires an explicit native response choice", Address: resourceAddress(service, "operation", operation), Path: "/result/status", Details: map[string]any{"migration_disposition": "manual_choice"}})
			continue
		}
		source, sourceName := "body", candidateTagName(tag.Get("json"))
		if input {
			for _, candidate := range []struct{ key, source string }{{"query", "query"}, {"qs", "query"}, {"header", "header"}, {"cookie", "cookie"}} {
				if value := candidateTagName(tag.Get(candidate.key)); value != "" {
					source, sourceName = candidate.source, value
					break
				}
			}
		}
		if sourceName == "-" {
			continue
		}
		if sourceName == "" {
			sourceName = field.Name()
		}
		name := snakeName(field.Name())
		seen[name]++
		if seen[name] > 1 {
			name += fmt.Sprintf("_%d", seen[name])
		}
		typeExpression, complete := migrationCandidateType(field.Type())
		if !complete {
			diagnostics = append(diagnostics, candidateTypeDiagnostic(service, operation, name, field.Type().String()))
		}
		if source == "header" {
			lower := strings.ToLower(sourceName)
			if lower != sourceName {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN5403", Severity: "warning", Message: "legacy header spelling is normalized in the native candidate", Address: resourceAddress(service, "operation", operation), Path: "/input/" + name, Details: map[string]any{"legacy": sourceName, "native": lower}})
			}
			sourceName = lower
		}
		fields = append(fields, migrationCandidateField{Name: name, WireName: sourceName, Type: typeExpression, Source: source, SourceName: sourceName})
	}
	return fields, diagnostics
}

func migrationCandidateType(value types.Type) (string, bool) {
	if value == nil {
		return "json", false
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		inner, complete := migrationCandidateType(pointer.Elem())
		return "optional(" + inner + ")", complete
	}
	if named, ok := value.(*types.Named); ok {
		if object := named.Obj(); object != nil && object.Pkg() != nil {
			qualified := object.Pkg().Path() + "." + object.Name()
			switch qualified {
			case "time.Time":
				return "datetime", true
			case "github.com/google/uuid.UUID":
				return "uuid", true
			case "net/url.URL":
				return "url", true
			}
		}
		return migrationCandidateType(named.Underlying())
	}
	switch typed := value.(type) {
	case *types.Basic:
		switch typed.Kind() {
		case types.Bool:
			return "bool", true
		case types.String:
			return "string", true
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
			return "int64", true
		case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
			return "uint64", true
		case types.Float32, types.Float64:
			return "float64", true
		default:
			return "json", false
		}
	case *types.Slice:
		if basic, ok := types.Unalias(typed.Elem()).(*types.Basic); ok && basic.Kind() == types.Byte {
			return "bytes", true
		}
		inner, complete := migrationCandidateType(typed.Elem())
		return "list(" + inner + ")", complete
	case *types.Array:
		inner, complete := migrationCandidateType(typed.Elem())
		return "list(" + inner + ")", complete
	case *types.Map:
		key, keyComplete := migrationCandidateType(typed.Key())
		inner, complete := migrationCandidateType(typed.Elem())
		if key != "string" {
			return "json", false
		}
		return "map(" + inner + ")", keyComplete && complete
	case *types.Struct, *types.Interface:
		return "json", false
	default:
		return "json", false
	}
}

func renderMigrationCandidateOperation(source *strings.Builder, service string, operation migrationCandidateOperation) {
	fmt.Fprintf(source, "\nrecord %q {\n", operation.Name+"_input")
	for _, field := range operation.Input {
		renderMigrationCandidateField(source, field)
	}
	source.WriteString("}\n")
	fmt.Fprintf(source, "\nrecord %q {\n", operation.Name+"_result")
	for _, field := range operation.Output {
		renderMigrationCandidateField(source, field)
	}
	source.WriteString("}\n")
	fmt.Fprintf(source, "\noperation %q {\n  service = service.%s\n  input   = record.%s_input\n\n  handler {\n    method  = %q\n    adapter = \"legacy_go_v0\"\n  }\n\n  result \"success\" {\n    type = record.%s_result\n  }\n\n  error \"legacy_error\" {\n    type = std.type.problem\n  }\n}\n", operation.Name, service, operation.Name, operation.Method, operation.Name)
	fmt.Fprintf(source, "\nexecution %q {\n  operation = operation.%s\n  mode      = \"direct\"\n  timeout   = \"30s\"\n}\n", operation.Name+"_direct", operation.Name)
	if operation.Access == "private" {
		renderMigrationCandidateInternalBinding(source, operation)
		return
	}
	for index, method := range operation.Methods {
		name := operation.Name + "_http"
		if len(operation.Methods) > 1 {
			name += fmt.Sprintf("_%d", index+1)
		}
		renderMigrationCandidateHTTPBinding(source, operation, name, method)
	}
}

func renderMigrationCandidateField(source *strings.Builder, field migrationCandidateField) {
	fmt.Fprintf(source, "\n  field %q {\n    type = %s\n", field.Name, field.Type)
	if field.WireName != "" && field.WireName != field.Name {
		fmt.Fprintf(source, "    wire_name = %q\n", field.WireName)
	}
	source.WriteString("  }\n")
}

func renderMigrationCandidateInternalBinding(source *strings.Builder, operation migrationCandidateOperation) {
	fmt.Fprintf(source, "\nbinding %q {\n  operation = operation.%s\n  execution = execution.%s_direct\n  protocol  = \"internal\"\n  delivery  = \"call\"\n\n  exposure       = \"local\"\n  authentication = std.authentication.inherit\n  authorization  = std.authorization.legacy_v0\n  pipeline       = std.pipeline.legacy_v0\n\n  internal {\n    visibility = \"package\"\n    principal  = \"inherit\"\n  }\n}\n", operation.Name+"_internal", operation.Name, operation.Name)
}

func renderMigrationCandidateHTTPBinding(source *strings.Builder, operation migrationCandidateOperation, bindingName, method string) {
	authentication, authorization := "std.authentication.none", "std.authorization.public"
	if operation.Access == "auth" {
		authentication, authorization = "var.authentication", "std.authorization.legacy_v0"
	}
	fmt.Fprintf(source, "\nbinding %q {\n  gateway   = var.gateway\n  operation = operation.%s\n  execution = execution.%s_direct\n  protocol  = \"http\"\n  delivery  = \"call\"\n\n  authentication = %s\n  authorization  = %s\n  pipeline       = std.pipeline.legacy_v0\n\n  http {\n    method        = %q\n    path          = %q\n    codec_profile = std.codec.http_json_v1\n", bindingName, operation.Name, operation.Name, authentication, authorization, strings.ToUpper(method), operation.Path)
	var bodyFields []string
	for _, field := range operation.Input {
		switch field.Source {
		case "path":
			fmt.Fprintf(source, "\n    path_parameter %q {\n      to = operation.%s.input.%s\n    }\n", field.SourceName, operation.Name, field.Name)
		case "path_tail":
			fmt.Fprintf(source, "\n    path_tail %q {\n      to = operation.%s.input.%s\n    }\n", field.SourceName, operation.Name, field.Name)
		case "query":
			fmt.Fprintf(source, "\n    query_parameter %q {\n      to = operation.%s.input.%s\n    }\n", field.SourceName, operation.Name, field.Name)
		case "header":
			fmt.Fprintf(source, "\n    header %q {\n      to = operation.%s.input.%s\n    }\n", field.SourceName, operation.Name, field.Name)
		case "cookie":
			fmt.Fprintf(source, "\n    cookie %q {\n      to = operation.%s.input.%s\n    }\n", field.SourceName, operation.Name, field.Name)
		default:
			bodyFields = append(bodyFields, field.Name)
		}
	}
	if len(bodyFields) > 0 {
		fmt.Fprintf(source, "\n    body {\n      codec = \"json\"\n      to    = operation.%s.input\n", operation.Name)
		if len(bodyFields) != len(operation.Input) {
			references := make([]string, 0, len(bodyFields))
			for _, field := range bodyFields {
				references = append(references, "operation."+operation.Name+".input."+field)
			}
			fmt.Fprintf(source, "      include = [%s]\n", strings.Join(references, ", "))
		}
		source.WriteString("    }\n")
	}
	fmt.Fprintf(source, "\n    response \"success\" {\n      when   = result.success\n      status = %d\n", map[bool]int{true: 200, false: 204}[operation.HasOutput])
	if operation.HasOutput {
		source.WriteString("\n      body {\n        codec = \"json\"\n        from  = result.success\n      }\n")
	}
	source.WriteString("    }\n\n    response \"legacy_error\" {\n      when   = error.legacy_error\n      status = 500\n\n      body {\n        codec = \"problem_json\"\n        from  = error.legacy_error\n      }\n    }\n  }\n}\n")
}

func legacyTerminalPathTail(route string) (string, bool) {
	segments := strings.Split(strings.TrimPrefix(route, "/"), "/")
	if len(segments) == 0 || !strings.HasPrefix(segments[len(segments)-1], "*") || len(segments[len(segments)-1]) == 1 {
		return "", false
	}
	for _, segment := range segments[:len(segments)-1] {
		if strings.HasPrefix(segment, "*") {
			return "", false
		}
	}
	return strings.TrimPrefix(segments[len(segments)-1], "*"), true
}

func legacyCandidatePathToNative(route string) string {
	segments := strings.Split(route, "/")
	for index, segment := range segments {
		switch {
		case strings.HasPrefix(segment, ":"):
			segments[index] = "{" + snakeName(strings.TrimPrefix(segment, ":")) + "}"
		case strings.HasPrefix(segment, "*"):
			segments[index] = "{" + snakeName(strings.TrimPrefix(segment, "*")) + "...}"
		}
	}
	return strings.Join(segments, "/")
}

func appendMigrationCandidateModule(root string, service MigrationService, requiresAuthentication bool) error {
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	inputs := "    gateway = http_gateway.public_api\n"
	if requiresAuthentication {
		inputs += "    authentication = authentication.standard\n"
	}
	moduleSource := fmt.Sprintf("\nmodule %q {\n  source = %q\n\n  inputs = {\n%s  }\n}\n", service.Name, service.Package, inputs)
	data = append(append(data, '\n'), []byte(moduleSource)...)
	formatted, err := canonicalFormatSource(data, "scenery.scn")
	if err != nil {
		return err
	}
	return atomicWrite(path, formatted)
}

func candidateTagName(value string) string {
	if value == "" {
		return ""
	}
	name, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(name)
}

func candidateTypeDiagnostic(service, operation, field, goType string) Diagnostic {
	return Diagnostic{Code: "SCN5404", Severity: "warning", Message: "legacy Go type requires an explicit native contract choice", Address: resourceAddress(service, "operation", operation), Path: "/input/" + field, Details: map[string]any{"go_type": goType, "proposed_type": "json", "migration_disposition": "manual_choice"}}
}

func migrationCandidatePlanID(plan MigrationCandidatePlan) string {
	copy := plan
	copy.PlanID = ""
	return revisionHash("scenery.migration-candidate-plan.v1\x00", copy)
}

func migrationCandidateReceiptPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "migrations", "receipts", "candidate-"+name)
}
