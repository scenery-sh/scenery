package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
	sceneryruntime "scenery.sh/runtime"
)

func runBindingCLI(stdout, stderr io.Writer, arguments []string) (bool, error) {
	root, cfg, result, bindings, err := loadCLIBindings()
	if err != nil || len(bindings) == 0 {
		return err != nil, err
	}
	binding, commandLength := matchCLICommand(bindings, arguments)
	if binding.Address == "" {
		return false, nil
	}
	control, err := parseCLIControlFlags(arguments[commandLength:])
	if err != nil {
		return true, err
	}
	if control.help {
		if control.output == "json" {
			return true, writeScopedHelpJSON(stdout, bindingHelpEntry(result.Manifest.Resources, binding))
		}
		writeCLIHelp(stdout, binding)
		return true, nil
	}
	run, err := executeBindingCLI(root, cfg, result, binding, control.remaining, control.live)
	if err != nil {
		return true, err
	}
	condition, exit, payload, mapping := run.Condition, run.Exit, run.Value, run.mapping
	if control.output == "json" {
		envelope := newCLIEnvelope(exit == 0, map[string]any{"binding": binding.Address, "outcome": condition, "value": payload, "execution": run.Execution}, nil)
		envelope.WorkspaceRevision = result.WorkspaceRevision
		envelope.ContractRevision = result.Manifest.ContractRevision
		envelope.ImplementationRevision = run.implementationRevision
		err = json.NewEncoder(stdout).Encode(envelope)
	} else if payload != nil {
		stream := stdout
		if mapping["stderr"] != nil {
			stream = stderr
		}
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return true, marshalErr
		}
		_, err = fmt.Fprintln(stream, string(encoded))
	}
	if err != nil {
		return true, err
	}
	if exit != 0 {
		return true, &silentCLIError{err: fmt.Errorf("CLI outcome %s", condition), code: exit}
	}
	return true, nil
}

// bindingCLIRun is one executed application CLI binding: its outcome, the
// exit code and output the binding maps it to, and the execution identity.
type bindingCLIRun struct {
	Binding   string         `json:"binding"`
	Condition string         `json:"outcome"`
	Exit      int            `json:"exit"`
	Value     any            `json:"value"`
	Execution map[string]any `json:"execution"`

	mapping                map[string]any
	implementationRevision any
}

// executeBindingCLI decodes arguments with the binding's declared grammar and
// runs the request standalone or in the running runtime.
func executeBindingCLI(root string, cfg appcfg.Config, result *compiler.Result, binding graph.Resource, arguments []string, live bool) (bindingCLIRun, error) {
	cli, _ := binding.Spec["cli"].(map[string]any)
	input, err := buildCLIInput(result.Manifest.Resources, binding, arguments)
	if err != nil {
		return bindingCLIRun{}, fmt.Errorf("invalid_request: %w", err)
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		return bindingCLIRun{}, err
	}
	request := sceneryruntime.ContractCLIRequest{
		Kind: sceneryruntime.ContractCLIRequestKind, SchemaRevision: sceneryruntime.ContractCLIRequestSchemaRevision,
		SpecRevision: result.Manifest.SpecRevision, Producer: cliProducer(), Binding: binding.Address, Input: encodedInput,
	}
	run := bindingCLIRun{Binding: binding.Address, Execution: map[string]any{"mode": "standalone"}}
	var response sceneryruntime.ContractCLIResponse
	if live {
		served, err := invokeLiveBindingCLI(root, request)
		if err != nil {
			return bindingCLIRun{}, err
		}
		response = served.Response
		run.Execution = map[string]any{"mode": "live", "generation": served.Generation, "process": served.Process, "contract_revision": served.ContractRevision, "implementation_revision": served.ImplementationRevision, "build_input_digest": served.BuildInputDigest}
	} else {
		standalone, revisions, err := invokeStandaloneBindingCLI(root, cfg, request)
		if err != nil {
			return bindingCLIRun{}, err
		}
		response, run.implementationRevision = standalone, revisions
	}
	if err := sceneryruntime.ValidateContractCLIResponse(response, result.Manifest.SpecRevision); err != nil {
		return bindingCLIRun{}, fmt.Errorf("internal: invalid contract CLI runtime response")
	}
	if invocation := response.Invocation; invocation != nil {
		run.Execution["invocation_id"] = invocation.InvocationID
		if invocation.TraceID != "" {
			run.Execution["trace_id"] = invocation.TraceID
		}
	}
	if response.Problem != nil {
		return bindingCLIRun{}, &codedCLIError{err: fmt.Errorf("%s: %s", response.Problem.Code, response.Problem.Message), code: contractCLIProblemExit(response.Problem.Code)}
	}
	if response.Outcome == nil {
		return bindingCLIRun{}, fmt.Errorf("internal: contract CLI runtime returned no outcome")
	}
	run.Condition = response.Outcome.Kind + "." + response.Outcome.Name
	for _, candidate := range contractChildren(cli, "outcome") {
		if contractReference(candidate["when"]) == run.Condition {
			run.mapping = candidate
			break
		}
	}
	if run.mapping == nil {
		return bindingCLIRun{}, fmt.Errorf("internal: CLI outcome %s has no mapping", run.Condition)
	}
	run.Exit = contractInteger(run.mapping["exit"])
	run.Value, err = selectCLIOutput(response.Outcome.Payload, run.Condition, run.mapping)
	if err != nil {
		return bindingCLIRun{}, err
	}
	return run, nil
}

// invokeStandaloneBindingCLI builds the development target and runs the
// request in a new application process.
func invokeStandaloneBindingCLI(root string, cfg appcfg.Config, request sceneryruntime.ContractCLIRequest) (sceneryruntime.ContractCLIResponse, any, error) {
	file, err := os.CreateTemp("", ".scenery-contract-cli-*.json")
	if err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, err
	}
	requestPath := file.Name()
	defer func() { _ = os.Remove(requestPath) }()
	if err = file.Chmod(0o600); err == nil {
		err = json.NewEncoder(file).Encode(request)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, err
	}
	built, err := build.AppForTarget(root, cfg, "")
	if err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, err
	}
	command := exec.CommandContext(context.Background(), built.Binary, "--scenery-contract-cli-request", requestPath)
	command.Dir = root
	baseEnv := envpolicy.Environ()
	storageEnv, err := storageCapabilityEnv(context.Background(), root, cfg, nil, baseEnv, "")
	if err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, err
	}
	storageLease, err := storageTaskLease(context.Background(), storageEnv)
	if err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, err
	}
	if storageLease != nil {
		defer func() { _ = storageLease.Close() }()
	}
	command.Env = envWithOverrides(baseEnv, append(storageEnv, "SCENERY_APP_ID="+cfg.AppID(), "SCENERY_APP_ROOT="+root)...)
	var responseBytes, processStderr bytes.Buffer
	command.Stdout, command.Stderr = &responseBytes, &processStderr
	if err := command.Run(); err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, fmt.Errorf("internal: contract CLI runtime failed: %w: %s", err, strings.TrimSpace(processStderr.String()))
	}
	var response sceneryruntime.ContractCLIResponse
	decoder := json.NewDecoder(&responseBytes)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return sceneryruntime.ContractCLIResponse{}, nil, fmt.Errorf("internal: invalid contract CLI runtime response")
	}
	return response, built.ImplementationRevisions, nil
}

// invokeLiveBindingCLI waits until the running scenery up serves the source
// as it is now, then runs the request in the binding's service process of the
// serving generation.
func invokeLiveBindingCLI(root string, request sceneryruntime.ContractCLIRequest) (runtimeLiveCLIResult, error) {
	ctx := context.Background()
	wait, err := waitForWorktreeSource(ctx, root, defaultSourceWaitTimeout)
	if err != nil {
		return runtimeLiveCLIResult{}, err
	}
	if !wait.OK {
		detail := wait.CompileError
		if wait.BuildBlock != nil {
			detail = wait.BuildBlock.Cause
		}
		return runtimeLiveCLIResult{}, &codedCLIError{err: fmt.Errorf("failed_precondition: the running scenery up does not serve the current source (%s after %dms): %s", wait.Outcome, wait.WaitedMS, detail), code: 3}
	}
	callCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var live runtimeLiveCLIResult
	if err := worktreeRuntimeCall(callCtx, root, "cli/invoke", map[string]any{"request": request}, &live); err != nil {
		return runtimeLiveCLIResult{}, err
	}
	return live, nil
}

func loadCLIBindings() (string, appcfg.Config, *compiler.Result, []graph.Resource, error) {
	return loadCLIBindingsAt("")
}

// loadCLIBindingsAt compiles the app at appRoot (or the working directory)
// and returns its CLI bindings. An unresolvable root has no bindings.
func loadCLIBindingsAt(appRoot string) (string, appcfg.Config, *compiler.Result, []graph.Resource, error) {
	start, err := resolveAppRoot(appRoot)
	if err != nil {
		return "", appcfg.Config{}, nil, nil, nil
	}
	root, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return "", appcfg.Config{}, nil, nil, nil
	}
	result, err := compiler.Compile(root)
	if err != nil {
		return root, cfg, nil, nil, err
	}
	if !result.Valid() {
		return root, cfg, result, nil, fmt.Errorf("failed_precondition: app contract is invalid")
	}
	var bindings []graph.Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.binding" && stringValueForCLI(resource.Spec["protocol"]) == "cli" {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return root, cfg, result, bindings, nil
}

func matchCLICommand(bindings []graph.Resource, arguments []string) (graph.Resource, int) {
	var matched graph.Resource
	matchedLength := 0
	for _, binding := range bindings {
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := contractStrings(cli["command"])
		if len(command) <= matchedLength || len(arguments) < len(command) {
			continue
		}
		match := true
		for index := range command {
			match = match && arguments[index] == command[index]
		}
		if match {
			matched, matchedLength = binding, len(command)
		}
	}
	return matched, matchedLength
}

// cliControlFlags are the Scenery-owned flags of an application CLI binding
// invocation; the rest are the binding's own arguments.
type cliControlFlags struct {
	remaining []string
	output    string
	help      bool
	live      bool
}

func parseCLIControlFlags(arguments []string) (cliControlFlags, error) {
	control := cliControlFlags{remaining: make([]string, 0, len(arguments)), output: "human"}
	for index := 0; index < len(arguments); index++ {
		switch {
		case arguments[index] == "--help" || arguments[index] == "-h":
			control.help = true
		case arguments[index] == "--live":
			control.live = true
		case arguments[index] == "-o" && index+1 < len(arguments):
			index++
			control.output = arguments[index]
		case strings.HasPrefix(arguments[index], "-o="):
			control.output = strings.TrimPrefix(arguments[index], "-o=")
		default:
			control.remaining = append(control.remaining, arguments[index])
		}
	}
	if control.output != "human" && control.output != "json" {
		return cliControlFlags{}, usageErrorf("unsupported output %q", control.output)
	}
	return control, nil
}

func buildCLIInput(resources []graph.Resource, binding graph.Resource, arguments []string) (any, error) {
	cli, _ := binding.Spec["cli"].(map[string]any)
	operation := resourceByReference(resources, binding, contractReference(binding.Spec["operation"]), "operation")
	fieldTypes := cliInputFieldTypes(resources, operation)
	input := any(map[string]any{})
	set := func(target string, raw any) error {
		field := cliTargetField(operation, target)
		typeValue := operation.Spec["input"]
		if field != "" {
			typeValue = fieldTypes[field]
		}
		value, err := coerceCLIValue(fmt.Sprint(raw), typeValue)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if field == "" {
			input = value
			return nil
		}
		input.(map[string]any)[field] = value
		return nil
	}
	flagsByLong, flagsByShort := map[string]map[string]any{}, map[string]map[string]any{}
	for _, flag := range contractChildren(cli, "flag") {
		flagsByLong[stringValueForCLI(flag["name"])] = flag
		if short := stringValueForCLI(flag["short"]); short != "" {
			flagsByShort[short] = flag
		}
	}
	positionals, seenFlags := []string{}, map[string]bool{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			positionals = append(positionals, arguments[index+1:]...)
			break
		}
		name, value, isFlag := "", "", false
		var flag map[string]any
		if strings.HasPrefix(argument, "--") {
			name, value, _ = strings.Cut(strings.TrimPrefix(argument, "--"), "=")
			flag, isFlag = flagsByLong[name]
		} else if strings.HasPrefix(argument, "-") && len(argument) == 2 {
			name = strings.TrimPrefix(argument, "-")
			flag, isFlag = flagsByShort[name]
		}
		if !isFlag {
			if strings.HasPrefix(argument, "-") {
				return nil, usageErrorf("unknown flag %q", argument)
			}
			positionals = append(positionals, argument)
			continue
		}
		longName := stringValueForCLI(flag["name"])
		if seenFlags[longName] {
			return nil, usageErrorf("flag --%s was provided more than once", longName)
		}
		seenFlags[longName] = true
		if value == "" {
			field := cliTargetField(operation, contractReference(flag["to"]))
			if cliTypeExpression(fieldTypes[field]) == "bool" {
				value = "true"
			} else if index+1 < len(arguments) {
				index++
				value = arguments[index]
			} else {
				return nil, usageErrorf("flag --%s requires a value", longName)
			}
		}
		if err := set(contractReference(flag["to"]), value); err != nil {
			return nil, err
		}
	}
	for _, argument := range contractChildren(cli, "argument") {
		position := contractInteger(argument["position"])
		if position < len(positionals) {
			if err := set(contractReference(argument["to"]), positionals[position]); err != nil {
				return nil, err
			}
		} else if argument["required"] == true {
			return nil, usageErrorf("missing argument %s", stringValueForCLI(argument["name"]))
		}
	}
	if len(positionals) > len(contractChildren(cli, "argument")) {
		return nil, usageErrorf("unexpected argument %q", positionals[len(contractChildren(cli, "argument"))])
	}
	for name, flag := range flagsByLong {
		if flag["required"] == true && !seenFlags[name] {
			return nil, usageErrorf("missing required flag --%s", name)
		}
	}
	return input, nil
}

func writeCLIHelp(output io.Writer, binding graph.Resource) {
	cli, _ := binding.Spec["cli"].(map[string]any)
	command := strings.Join(contractStrings(cli["command"]), " ")
	_, _ = fmt.Fprintf(output, "Usage: scenery %s", command)
	arguments := contractChildren(cli, "argument")
	sort.Slice(arguments, func(i, j int) bool {
		left := contractInteger(arguments[i]["position"])
		right := contractInteger(arguments[j]["position"])
		return left < right
	})
	for _, argument := range arguments {
		_, _ = fmt.Fprintf(output, " <%s>", stringValueForCLI(argument["name"]))
	}
	for _, flag := range contractChildren(cli, "flag") {
		_, _ = fmt.Fprintf(output, " [--%s <value>]", stringValueForCLI(flag["name"]))
	}
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Outcomes:")
	for _, outcome := range contractChildren(cli, "outcome") {
		exit := contractInteger(outcome["exit"])
		_, _ = fmt.Fprintf(output, "  %-28s exit %d\n", contractReference(outcome["when"]), exit)
	}
}

func runBindingCompletion(output io.Writer, words []string) error {
	_, _, _, bindings, err := loadCLIBindings()
	if err != nil {
		return err
	}
	candidates := map[string]bool{}
	for _, binding := range bindings {
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := contractStrings(cli["command"])
		matches := len(words) <= len(command)
		for index := range words {
			matches = matches && words[index] == command[index]
		}
		if matches && len(words) < len(command) {
			candidates[command[len(words)]] = true
		} else if matches {
			for _, flag := range contractChildren(cli, "flag") {
				candidates["--"+stringValueForCLI(flag["name"])] = true
			}
		}
	}
	values := make([]string, 0, len(candidates))
	for candidate := range candidates {
		values = append(values, candidate)
	}
	sort.Strings(values)
	for _, candidate := range values {
		_, _ = fmt.Fprintln(output, candidate)
	}
	return nil
}

func selectCLIOutput(payload json.RawMessage, condition string, mapping map[string]any) (any, error) {
	output, _ := mapping["stdout"].(map[string]any)
	if output == nil {
		output, _ = mapping["stderr"].(map[string]any)
	}
	if output == nil {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	from := contractReference(output["from"])
	path := strings.TrimPrefix(from, condition)
	for _, segment := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		if segment == "" {
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("CLI output path %s is unavailable", from)
		}
		value, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("CLI output path %s is unavailable", from)
		}
	}
	return value, nil
}

func coerceCLIValue(raw string, typeValue any) (any, error) {
	typeName := cliTypeExpression(typeValue)
	for _, wrapper := range []string{"optional", "nullable"} {
		if strings.HasPrefix(typeName, wrapper+"(") && strings.HasSuffix(typeName, ")") {
			typeName = strings.TrimSpace(typeName[len(wrapper)+1 : len(typeName)-1])
		}
	}
	switch typeName {
	case "bool":
		return strconv.ParseBool(raw)
	case "int", "int8", "int16", "int32", "int64":
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return nil, err
		}
		return json.Number(raw), nil
	case "uint", "uint8", "uint16", "uint32", "uint64":
		if _, err := strconv.ParseUint(raw, 10, 64); err != nil {
			return nil, err
		}
		return json.Number(raw), nil
	case "float32", "float64", "decimal":
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return nil, err
		}
		return json.Number(raw), nil
	case "json":
		var value any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	if strings.HasPrefix(typeName, "list(") || strings.HasPrefix(typeName, "set(") || strings.HasPrefix(typeName, "map(") || strings.HasPrefix(typeName, "tuple(") {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		return value, nil
	}
	return raw, nil
}

func cliInputFieldTypes(resources []graph.Resource, operation graph.Resource) map[string]any {
	result := map[string]any{}
	reference := contractReference(operation.Spec["input"])
	record := resourceByReference(resources, operation, reference, "record")
	for _, field := range contractChildren(record.Spec, "field") {
		result[stringValueForCLI(field["name"])] = field["type"]
	}
	return result
}

func cliTargetField(operation graph.Resource, target string) string {
	prefix := "operation." + operation.Name + ".input"
	if target == prefix {
		return ""
	}
	return strings.TrimPrefix(target, prefix+".")
}

func resourceByReference(resources []graph.Resource, owner graph.Resource, reference, kind string) graph.Resource {
	for _, resource := range resources {
		if resource.Address == reference || resource.Module == owner.Module && resource.Kind == "scenery."+kind && resource.Name == strings.TrimPrefix(reference, kind+".") {
			return resource
		}
	}
	return graph.Resource{}
}

func contractChildren(spec map[string]any, name string) []map[string]any {
	switch value := spec[name].(type) {
	case map[string]any:
		return []map[string]any{value}
	case []map[string]any:
		return append([]map[string]any(nil), value...)
	case []any:
		result := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func contractReference(value any) string {
	if object, ok := value.(map[string]any); ok {
		if reference, ok := object["$ref"].(string); ok {
			return reference
		}
	}
	text, _ := value.(string)
	return text
}

func contractStrings(value any) []string {
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}

func contractInteger(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	case json.Number:
		parsed, _ := strconv.ParseInt(number.String(), 10, 64)
		return int(parsed)
	case map[string]any:
		if number["$scalar"] == "int" {
			parsed, _ := strconv.ParseInt(fmt.Sprint(number["value"]), 10, 64)
			return int(parsed)
		}
	default:
		return 0
	}
	return 0
}

func cliTypeExpression(value any) string {
	if reference := contractReference(value); reference != "" {
		return reference
	}
	if object, ok := value.(map[string]any); ok {
		if expression, ok := object["$expression"].(string); ok {
			return expression
		}
	}
	return fmt.Sprint(value)
}

func stringValueForCLI(value any) string {
	text, _ := value.(string)
	return text
}

func contractCLIProblemExit(code string) int {
	switch code {
	case "invalid_argument", "not_found", "already_exists", "failed_precondition":
		return 2
	case "permission_denied", "unauthenticated":
		return 5
	case "unavailable", "unimplemented":
		return 4
	default:
		return 10
	}
}
