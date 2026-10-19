package main

import (
	"io"
	"sort"
	"strings"

	"scenery.sh/internal/graph"
)

// helpApplicationBinding is the machine help for one application CLI
// binding, derived from the same graph declarations the binding parser uses.
type helpApplicationBinding struct {
	Address   string                 `json:"address"`
	Operation string                 `json:"operation"`
	InputType string                 `json:"input_type"`
	Arguments []helpBindingParameter `json:"arguments,omitempty"`
	Flags     []helpBindingParameter `json:"flags,omitempty"`
	Outcomes  []helpBindingOutcome   `json:"outcomes"`
}

type helpBindingParameter struct {
	Name     string `json:"name"`
	Short    string `json:"short,omitempty"`
	Position *int   `json:"position,omitempty"`
	Required bool   `json:"required"`
	Field    string `json:"field,omitempty"`
	Type     string `json:"type"`
}

type helpBindingOutcome struct {
	When   string `json:"when"`
	Exit   int    `json:"exit"`
	Stream string `json:"stream"`
}

// bindingHelpEntry describes an application CLI binding in the scenery.help
// shape so agents discover application commands the same way as built-ins.
func bindingHelpEntry(resources []graph.Resource, binding graph.Resource) helpCommandEntry {
	cli, _ := binding.Spec["cli"].(map[string]any)
	command := strings.Join(contractStrings(cli["command"]), " ")
	operation := resourceByReference(resources, binding, contractReference(binding.Spec["operation"]), "operation")
	fieldTypes := cliInputFieldTypes(resources, operation)
	parameter := func(item map[string]any) helpBindingParameter {
		field := cliTargetField(operation, contractReference(item["to"]))
		typeValue := operation.Spec["input"]
		if field != "" {
			typeValue = fieldTypes[field]
		}
		return helpBindingParameter{Name: stringValueForCLI(item["name"]), Short: stringValueForCLI(item["short"]), Required: item["required"] == true, Field: field, Type: cliTypeExpression(typeValue)}
	}
	descriptor := &helpApplicationBinding{Address: binding.Address, Operation: operation.Address, InputType: cliTypeExpression(operation.Spec["input"])}
	usage := "scenery " + command
	arguments := contractChildren(cli, "argument")
	sort.SliceStable(arguments, func(i, j int) bool {
		return contractInteger(arguments[i]["position"]) < contractInteger(arguments[j]["position"])
	})
	for _, argument := range arguments {
		item := parameter(argument)
		position := contractInteger(argument["position"])
		item.Position = &position
		descriptor.Arguments = append(descriptor.Arguments, item)
		if item.Required {
			usage += " <" + item.Name + ">"
		} else {
			usage += " [<" + item.Name + ">]"
		}
	}
	flags := []string{}
	for _, flag := range contractChildren(cli, "flag") {
		item := parameter(flag)
		descriptor.Flags = append(descriptor.Flags, item)
		text := "--" + item.Name + " <" + item.Type + ">"
		if item.Type == "bool" {
			text = "--" + item.Name
		}
		flags = append(flags, text)
		if item.Required {
			usage += " " + text
		} else {
			usage += " [" + text + "]"
		}
	}
	flags = append(flags, "-o human|json", "--help")
	for _, outcome := range contractChildren(cli, "outcome") {
		stream := "none"
		if outcome["stdout"] != nil {
			stream = "stdout"
		} else if outcome["stderr"] != nil {
			stream = "stderr"
		}
		descriptor.Outcomes = append(descriptor.Outcomes, helpBindingOutcome{When: contractReference(outcome["when"]), Exit: contractInteger(outcome["exit"]), Stream: stream})
	}
	return helpCommandEntry{
		Command:            command,
		Group:              "Application",
		Summary:            "Run application operation " + operation.Address + ".",
		Usage:              []string{usage + " [-o human|json]"},
		Flags:              flags,
		AppRootRequirement: "required",
		ApplicationBinding: descriptor,
		JSON:               true,
		Stability:          "application",
	}
}

// writeBindingHelpJSON writes machine help for the application CLI binding
// named by topics. It reports false when no binding matches.
func writeBindingHelpJSON(stdout io.Writer, topics []string) (bool, error) {
	_, _, result, bindings, err := loadCLIBindings()
	if err != nil || len(bindings) == 0 {
		return err != nil, err
	}
	binding, length := matchCLICommand(bindings, topics)
	if binding.Address == "" || length != len(topics) {
		return false, nil
	}
	return true, writeScopedHelpJSON(stdout, bindingHelpEntry(result.Manifest.Resources, binding))
}
