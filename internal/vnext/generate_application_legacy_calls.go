package vnext

import (
	"fmt"
	"go/token"
	"strings"
)

// renderLegacyCallAliasRegistrations projects frozen v0 Go call symbols onto
// the one active native service. It registers private in-process aliases only;
// HTTP route ownership remains exclusively with the native bindings.
func renderLegacyCallAliasRegistrations(b *strings.Builder, service Resource, operations []Resource) error {
	symbols := serviceLegacyCallSymbols(service, operations)
	for _, operation := range operations {
		symbol := symbols[operation.Address]
		if symbol == "" {
			continue
		}
		if !token.IsIdentifier(symbol) || symbol[0] < 'A' || symbol[0] > 'Z' {
			return fmt.Errorf("legacy call alias %q for %s is not an exported Go identifier", symbol, operation.Address)
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := strings.TrimSpace(stringValue(handler["method"]))
		if method == "" {
			return fmt.Errorf("legacy call alias operation %s has no native handler method", operation.Address)
		}
		operationName := goName(operation.Name)
		inputType := goWireTypeExpression(operation.Spec["input"])
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterEndpointChecked(&sceneryruntime.Endpoint{Service: %q, Name: %q, Access: sceneryruntime.Private, Methods: []string{\"INTERNAL\"}, ResponseType: sceneryruntime.TypeOf[any](), Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {\n", service.Name, symbol)
		fmt.Fprintf(b, "\t\t\t\traw, err := implementation.SceneryVNextLegacyCall%sInput(pathArgs, payload); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }\n", symbol)
		fmt.Fprintf(b, "\t\t\t\tvar input contract.%sInput; if err := scenery.UnmarshalContractValue(raw, &input, %q); err != nil { return nil, sceneryruntime.ContractSystemError(err) }\n", operationName, inputType)
		fmt.Fprintf(b, "\t\t\t\tif service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; outcome, err := service.%s(ctx, input); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }\n", method)
		fmt.Fprintf(b, "\t\t\t\tencoded, err := contract.Marshal%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return implementation.SceneryVNextLegacyCall%sOutput(encoded)\n", operationName, symbol)
		b.WriteString("\t\t\t}}); err != nil { return err }\n")
	}
	return nil
}
