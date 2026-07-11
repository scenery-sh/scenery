package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/vnext"
	sceneryruntime "scenery.sh/runtime"
)

func runVNextBindingCLI(stdout, stderr io.Writer, arguments []string) (bool, error) {
	root, cfg, result, bindings, err := loadVNextCLIBindings()
	if err != nil || len(bindings) == 0 {
		return err != nil, err
	}
	binding, commandLength := matchVNextCLICommand(bindings, arguments)
	if binding.Address == "" {
		return false, nil
	}
	cli, _ := binding.Spec["cli"].(map[string]any)
	remaining, output, help, err := parseVNextCLIControlFlags(arguments[commandLength:])
	if err != nil {
		return true, err
	}
	if help {
		writeVNextCLIHelp(stdout, binding)
		return true, nil
	}
	input, err := buildVNextCLIInput(result.Manifest.Resources, binding, remaining)
	if err != nil {
		return true, fmt.Errorf("invalid_request: %w", err)
	}
	request, err := os.CreateTemp("", ".scenery-contract-cli-*.json")
	if err != nil {
		return true, err
	}
	requestPath := request.Name()
	defer os.Remove(requestPath)
	if err := request.Chmod(0o600); err == nil {
		err = json.NewEncoder(request).Encode(map[string]any{"api_version": "scenery.contract-cli-request/v1", "binding": binding.Address, "input": input})
	}
	if closeErr := request.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return true, err
	}
	built, err := build.AppForVNextTarget(root, cfg, "", "development")
	if err != nil {
		return true, err
	}
	command := exec.CommandContext(context.Background(), built.Binary, "--scenery-contract-cli-request", requestPath)
	command.Dir = root
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), root, ".env", ".env.local")
	if err != nil {
		return true, err
	}
	storageEnv, err := storageCapabilityEnv(cfg, nil, baseEnv, "")
	if err != nil {
		return true, err
	}
	command.Env = envWithOverrides(baseEnv, append(storageEnv, "SCENERY_APP_ID="+cfg.AppID(), "SCENERY_APP_ROOT="+root)...)
	var responseBytes, processStderr bytes.Buffer
	command.Stdout, command.Stderr = &responseBytes, &processStderr
	if err := command.Run(); err != nil {
		return true, fmt.Errorf("internal: contract CLI runtime failed: %w: %s", err, strings.TrimSpace(processStderr.String()))
	}
	var response sceneryruntime.ContractCLIResponse
	decoder := json.NewDecoder(&responseBytes)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || response.APIVersion != "scenery.contract-cli-response/v1" {
		return true, fmt.Errorf("internal: invalid contract CLI runtime response")
	}
	if response.Problem != nil {
		return true, &codedCLIError{err: fmt.Errorf("%s: %s", response.Problem.Code, response.Problem.Message), code: contractCLIProblemExit(response.Problem.Code)}
	}
	if response.Outcome == nil {
		return true, fmt.Errorf("internal: contract CLI runtime returned no outcome")
	}
	condition := response.Outcome.Kind + "." + response.Outcome.Name
	var mapping map[string]any
	for _, candidate := range vnextChildren(cli, "outcome") {
		if vnextRef(candidate["when"]) == condition {
			mapping = candidate
			break
		}
	}
	if mapping == nil {
		return true, fmt.Errorf("internal: CLI outcome %s has no mapping", condition)
	}
	exit, _ := vnextInteger(mapping["exit"])
	payload, err := selectVNextCLIOutput(response.Outcome.Payload, condition, mapping)
	if err != nil {
		return true, err
	}
	if output == "json" {
		err = json.NewEncoder(stdout).Encode(vnextEnvelope{
			APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: exit == 0,
			WorkspaceRevision: result.WorkspaceRevision, ContractRevision: result.Manifest.ContractRevision,
			ImplementationRevision: built.ImplementationRevisions, DeploymentRevision: nil,
			Data: map[string]any{"binding": binding.Address, "outcome": condition, "value": payload}, Diagnostics: []vnext.Diagnostic{},
		})
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

func loadVNextCLIBindings() (string, appcfg.Config, *vnext.Result, []vnext.Resource, error) {
	start, err := resolveAppRoot("")
	if err != nil {
		return "", appcfg.Config{}, nil, nil, nil
	}
	root, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return "", appcfg.Config{}, nil, nil, nil
	}
	if _, err := os.Stat(filepath.Join(root, "scenery.scn")); err != nil {
		return "", appcfg.Config{}, nil, nil, nil
	}
	result, err := vnext.Compile(root)
	if err != nil {
		return root, cfg, nil, nil, err
	}
	if !result.Valid() {
		return root, cfg, result, nil, fmt.Errorf("failed_precondition: edition-2027 contract is invalid")
	}
	var bindings []vnext.Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.binding/v1" && resource.Origin.Kind != "legacy_v0" && stringValueForCLI(resource.Spec["protocol"]) == "cli" {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return root, cfg, result, bindings, nil
}

func matchVNextCLICommand(bindings []vnext.Resource, arguments []string) (vnext.Resource, int) {
	var matched vnext.Resource
	matchedLength := 0
	for _, binding := range bindings {
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := vnextStrings(cli["command"])
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

func parseVNextCLIControlFlags(arguments []string) ([]string, string, bool, error) {
	remaining := make([]string, 0, len(arguments))
	output, help := "human", false
	for index := 0; index < len(arguments); index++ {
		switch {
		case arguments[index] == "--help" || arguments[index] == "-h":
			help = true
		case arguments[index] == "-o" && index+1 < len(arguments):
			index++
			output = arguments[index]
		case strings.HasPrefix(arguments[index], "-o="):
			output = strings.TrimPrefix(arguments[index], "-o=")
		default:
			remaining = append(remaining, arguments[index])
		}
	}
	if output != "human" && output != "json" {
		return nil, "", false, fmt.Errorf("unsupported output %q", output)
	}
	return remaining, output, help, nil
}

func buildVNextCLIInput(resources []vnext.Resource, binding vnext.Resource, arguments []string) (any, error) {
	cli, _ := binding.Spec["cli"].(map[string]any)
	operation := vnextResourceByRef(resources, binding, vnextRef(binding.Spec["operation"]), "operation")
	fieldTypes := vnextCLIInputFieldTypes(resources, operation)
	input := any(map[string]any{})
	set := func(target string, raw any) error {
		field := vnextCLITargetField(operation, target)
		typeValue := operation.Spec["input"]
		if field != "" {
			typeValue = fieldTypes[field]
		}
		value, err := coerceVNextCLIValue(fmt.Sprint(raw), typeValue)
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
	for _, flag := range vnextChildren(cli, "flag") {
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
				return nil, fmt.Errorf("unknown flag %q", argument)
			}
			positionals = append(positionals, argument)
			continue
		}
		longName := stringValueForCLI(flag["name"])
		if seenFlags[longName] {
			return nil, fmt.Errorf("flag --%s was provided more than once", longName)
		}
		seenFlags[longName] = true
		if value == "" {
			field := vnextCLITargetField(operation, vnextRef(flag["to"]))
			if vnextCLITypeExpression(fieldTypes[field]) == "bool" {
				value = "true"
			} else if index+1 < len(arguments) {
				index++
				value = arguments[index]
			} else {
				return nil, fmt.Errorf("flag --%s requires a value", longName)
			}
		}
		if err := set(vnextRef(flag["to"]), value); err != nil {
			return nil, err
		}
	}
	for _, argument := range vnextChildren(cli, "argument") {
		position, _ := vnextInteger(argument["position"])
		if position < len(positionals) {
			if err := set(vnextRef(argument["to"]), positionals[position]); err != nil {
				return nil, err
			}
		} else if argument["required"] == true {
			return nil, fmt.Errorf("missing argument %s", stringValueForCLI(argument["name"]))
		}
	}
	if len(positionals) > len(vnextChildren(cli, "argument")) {
		return nil, fmt.Errorf("unexpected argument %q", positionals[len(vnextChildren(cli, "argument"))])
	}
	for name, flag := range flagsByLong {
		if flag["required"] == true && !seenFlags[name] {
			return nil, fmt.Errorf("missing required flag --%s", name)
		}
	}
	return input, nil
}

func writeVNextCLIHelp(output io.Writer, binding vnext.Resource) {
	cli, _ := binding.Spec["cli"].(map[string]any)
	command := strings.Join(vnextStrings(cli["command"]), " ")
	fmt.Fprintf(output, "Usage: scenery %s", command)
	arguments := vnextChildren(cli, "argument")
	sort.Slice(arguments, func(i, j int) bool {
		left, _ := vnextInteger(arguments[i]["position"])
		right, _ := vnextInteger(arguments[j]["position"])
		return left < right
	})
	for _, argument := range arguments {
		fmt.Fprintf(output, " <%s>", stringValueForCLI(argument["name"]))
	}
	for _, flag := range vnextChildren(cli, "flag") {
		fmt.Fprintf(output, " [--%s <value>]", stringValueForCLI(flag["name"]))
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Outcomes:")
	for _, outcome := range vnextChildren(cli, "outcome") {
		exit, _ := vnextInteger(outcome["exit"])
		fmt.Fprintf(output, "  %-28s exit %d\n", vnextRef(outcome["when"]), exit)
	}
}

func runVNextCompletion(output io.Writer, words []string) error {
	_, _, _, bindings, err := loadVNextCLIBindings()
	if err != nil {
		return err
	}
	candidates := map[string]bool{}
	for _, binding := range bindings {
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := vnextStrings(cli["command"])
		matches := len(words) <= len(command)
		for index := range words {
			matches = matches && words[index] == command[index]
		}
		if matches && len(words) < len(command) {
			candidates[command[len(words)]] = true
		} else if matches {
			for _, flag := range vnextChildren(cli, "flag") {
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
		fmt.Fprintln(output, candidate)
	}
	return nil
}

func selectVNextCLIOutput(payload json.RawMessage, condition string, mapping map[string]any) (any, error) {
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
	from := vnextRef(output["from"])
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

func coerceVNextCLIValue(raw string, typeValue any) (any, error) {
	typeName := vnextCLITypeExpression(typeValue)
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

func vnextCLIInputFieldTypes(resources []vnext.Resource, operation vnext.Resource) map[string]any {
	result := map[string]any{}
	reference := vnextRef(operation.Spec["input"])
	record := vnextResourceByRef(resources, operation, reference, "record")
	for _, field := range vnextChildren(record.Spec, "field") {
		result[stringValueForCLI(field["name"])] = field["type"]
	}
	return result
}

func vnextCLITargetField(operation vnext.Resource, target string) string {
	prefix := "operation." + operation.Name + ".input"
	if target == prefix {
		return ""
	}
	return strings.TrimPrefix(target, prefix+".")
}

func vnextResourceByRef(resources []vnext.Resource, owner vnext.Resource, reference, kind string) vnext.Resource {
	for _, resource := range resources {
		if resource.Address == reference || resource.Module == owner.Module && resource.Kind == "scenery."+kind+"/v1" && resource.Name == strings.TrimPrefix(reference, kind+".") {
			return resource
		}
	}
	return vnext.Resource{}
}

func vnextChildren(spec map[string]any, name string) []map[string]any {
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

func vnextRef(value any) string {
	if object, ok := value.(map[string]any); ok {
		if reference, ok := object["$ref"].(string); ok {
			return reference
		}
	}
	text, _ := value.(string)
	return text
}

func vnextStrings(value any) []string {
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

func vnextInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case float64:
		return int(number), number == float64(int(number))
	case json.Number:
		parsed, err := strconv.ParseInt(number.String(), 10, 64)
		return int(parsed), err == nil
	case map[string]any:
		if number["$scalar"] == "int" {
			parsed, err := strconv.ParseInt(fmt.Sprint(number["value"]), 10, 64)
			return int(parsed), err == nil
		}
	default:
		return 0, false
	}
	return 0, false
}

func vnextCLITypeExpression(value any) string {
	if reference := vnextRef(value); reference != "" {
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
