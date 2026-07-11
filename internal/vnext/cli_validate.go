package vnext

import (
	"fmt"
	"sort"
	"strings"
)

var reservedCLICommands = map[string]bool{
	"agent": true, "build": true, "changes": true, "check": true, "compile": true, "completion": true,
	"console": true, "db": true, "deploy": true, "diff": true, "doctor": true, "down": true, "explain": true,
	"fmt": true, "generate": true, "get": true, "graph": true, "harness": true, "help": true, "inspect": true,
	"internal": true, "list": true, "logs": true, "metrics": true, "migrate": true, "prune": true, "ps": true,
	"schema": true, "storage": true, "symphony": true, "system": true, "task": true, "test": true, "traces": true,
	"up": true, "upgrade": true, "validate": true, "version": true, "worker": true, "worktree": true,
}

func validateCLIBindings(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	commands := map[string]string{}
	var diagnostics []Diagnostic
	for _, binding := range resources {
		if binding.Kind != "scenery.binding/v1" || binding.Origin.Kind == "legacy_v0" || stringValue(binding.Spec["protocol"]) != "cli" {
			continue
		}
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := stringValues(cli["command"])
		validCommand := len(command) > 0
		for _, segment := range command {
			validCommand = validCommand && validCLIName(segment)
		}
		key := strings.Join(command, "\x00")
		if !validCommand {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2410", "CLI command requires lower-kebab-case non-empty segments", binding))
		} else if reservedCLICommands[command[0]] {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2410", "CLI command conflicts with reserved scenery command "+command[0], binding))
		} else if previous := commands[key]; previous != "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2411", Severity: "error", Message: "duplicate CLI command " + strings.Join(command, " "), Address: binding.Address, Related: []Related{{Address: previous}}})
		} else {
			commands[key] = binding.Address
		}
		operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		shape := resolveOperationInputShape(byAddress, operation)
		mapped, positions, flags, shorts := map[string]bool{}, map[int]bool{}, map[string]bool{}, map[string]bool{}
		for _, group := range []string{"context", "argument", "flag"} {
			for _, mapping := range namedChildren(cli, group) {
				target := refString(mapping["to"])
				field, whole, valid := resolveOperationInputTarget(operation, shape, target)
				if group == "context" && whole {
					valid = false
				}
				mapKey := target
				if valid && !whole {
					mapKey = field.Name
				}
				if !valid || mapped[mapKey] {
					diagnostics = append(diagnostics, profileDiagnostic("SCN2412", "CLI input mapping target is invalid or duplicated: "+target, binding))
				} else {
					mapped[mapKey] = true
				}
				switch group {
				case "argument":
					position, ok := integerValue(mapping["position"])
					if !ok || position < 0 || positions[position] {
						diagnostics = append(diagnostics, profileDiagnostic("SCN2413", "CLI argument positions must be unique non-negative integers", binding))
					} else {
						positions[position] = true
					}
				case "flag":
					name, short := stringValue(mapping["name"]), stringValue(mapping["short"])
					if !validCLIName(name) || flags[name] || short != "" && (len(short) != 1 || shorts[short]) {
						diagnostics = append(diagnostics, profileDiagnostic("SCN2414", "CLI flags require unique portable long names and one-character short names", binding))
					}
					flags[name], shorts[short] = true, short != ""
				}
			}
		}
		if len(positions) > 0 {
			ordered := make([]int, 0, len(positions))
			for position := range positions {
				ordered = append(ordered, position)
			}
			sort.Ints(ordered)
			for index, position := range ordered {
				if index != position {
					diagnostics = append(diagnostics, profileDiagnostic("SCN2413", "CLI argument positions must be contiguous from zero", binding))
					break
				}
			}
		}
		if shape.Record != nil {
			for name, field := range shape.Fields {
				if !field.Optional && !field.HasDefault && !mapped[name] {
					diagnostics = append(diagnostics, profileDiagnostic("SCN2415", "required CLI input field has no mapping: "+name, binding))
				}
			}
		} else {
			whole := "operation." + operation.Name + ".input"
			if shape.Unit && len(mapped) != 0 {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2415", "unit CLI input cannot have argument, flag, or context mappings", binding))
			} else if !shape.Unit && !mapped[whole] {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2415", "non-record CLI input must be populated exactly once", binding))
			}
		}
		outcomes := map[string]bool{}
		for _, outcome := range namedChildren(cli, "outcome") {
			when := refString(outcome["when"])
			exit, ok := integerValue(outcome["exit"])
			if when == "" || outcomes[when] || !ok || exit < 0 || exit > 255 {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2416", "CLI outcomes require a unique condition and exit status from 0 through 255", binding))
			}
			outcomes[when] = true
			for _, stream := range []string{"stdout", "stderr"} {
				output, _ := outcome[stream].(map[string]any)
				if output == nil {
					continue
				}
				codec, from := stringValue(output["codec"]), refString(output["from"])
				if codec != "json" && codec != "problem_json" || from == "" || from != when && !strings.HasPrefix(from, when+".") {
					diagnostics = append(diagnostics, profileDiagnostic("SCN2417", fmt.Sprintf("CLI %s output must use json or problem_json from its mapped outcome", stream), binding))
				}
			}
		}
		if stringValue(binding.Spec["delivery"]) != "enqueue" {
			for _, kind := range []string{"result", "error"} {
				for _, variant := range namedChildren(operation.Spec, kind) {
					when := kind + "." + stringValue(variant["name"])
					if !outcomes[when] {
						diagnostics = append(diagnostics, profileDiagnostic("SCN2418", "reachable operation outcome has no CLI mapping: "+when, binding))
					}
				}
			}
		} else if !outcomes["dispatch.enqueued"] {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2418", "enqueue CLI binding requires dispatch.enqueued outcome mapping", binding))
		}
	}
	return diagnostics
}

func validCLIName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || index > 0 && char >= '0' && char <= '9' || index > 0 && char == '-' && index+1 < len(value) && value[index-1] != '-' {
			continue
		}
		return false
	}
	return true
}
