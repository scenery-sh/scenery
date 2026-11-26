package generate

import (
	"fmt"
	"strings"
)

func renderWorkerOperations(b *strings.Builder, operations, bindings []Resource) {
	for _, operation := range operations {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		name := goName(operation.Name)
		if operationUsesHTTPStreamOut(operation, bindings) {
			resultWrapper := name + "Result"
			for _, entry := range operationVariantWrappers(name, operation) {
				if entry.Kind == "result" {
					resultWrapper = entry.WrapperName
					break
				}
			}
			fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterWorkerEventOperation(%q, func(ctx context.Context, data []byte) ([]byte, sceneryruntime.RawEventProducer, error) {\n", operation.Address)
			fmt.Fprintf(b, "\t\t\t\tinput, err := contract.Unmarshal%sInput(data); if err != nil { return nil, nil, err }\n", name)
			b.WriteString("\t\t\t\tsceneryruntime.SetWorkerRequestPayload(ctx, input)\n")
			fmt.Fprintf(b, "\t\t\t\toutcome, events, err := service.%s(ctx, input)\n", method)
			b.WriteString("\t\t\t\tif err != nil { return nil, nil, err }\n")
			fmt.Fprintf(b, "\t\t\t\tswitch outcome.(type) {\n\t\t\t\tcase contract.%s, *contract.%s:\n", resultWrapper, resultWrapper)
			b.WriteString("\t\t\t\t\tif !events.IsSet() { return nil, nil, fmt.Errorf(\"stream_out result requires an EventStream\") }\n")
			b.WriteString("\t\t\t\tdefault:\n\t\t\t\t\tif events.IsSet() { return nil, nil, fmt.Errorf(\"stream_out error outcome cannot attach an EventStream\") }\n\t\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t\tout, err := contract.Marshal%sOutcome(outcome)\n\t\t\t\tif err != nil { return nil, nil, err }\n", name)
			fmt.Fprintf(b, "\t\t\t\treturn out, events.Bind(contract.Marshal%sEvent), nil\n\t\t\t}); err != nil { return err }\n", name)
			continue
		}
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterWorkerOperation(%q, func(ctx context.Context, data []byte) ([]byte, scenery.ByteStream, error) {\n", operation.Address)
		b.WriteString("\t\t\t\tvar stream scenery.ByteStream\n")
		fmt.Fprintf(b, "\t\t\t\tinput, err := contract.Unmarshal%sInput(data); if err != nil { return nil, stream, err }\n", name)
		b.WriteString("\t\t\t\tsceneryruntime.SetWorkerRequestPayload(ctx, input)\n")
		if operationUsesHTTPStream(operation, bindings) {
			fmt.Fprintf(b, "\t\t\t\toutcome, stream, err := service.%s(ctx, input)\n", method)
		} else {
			fmt.Fprintf(b, "\t\t\t\toutcome, err := service.%s(ctx, input)\n", method)
		}
		b.WriteString("\t\t\t\tif err != nil { return nil, stream, err }\n")
		fmt.Fprintf(b, "\t\t\t\tout, err := contract.Marshal%sOutcome(outcome)\n", name)
		b.WriteString("\t\t\t\treturn out, stream, err\n\t\t\t}); err != nil { return err }\n")
	}
}
