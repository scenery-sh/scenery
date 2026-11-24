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
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterWorkerOperation(%q, func(ctx context.Context, data []byte) ([]byte, scenery.ByteStream, error) {\n", operation.Address)
		b.WriteString("\t\t\t\tvar stream scenery.ByteStream\n")
		fmt.Fprintf(b, "\t\t\t\tinput, err := contract.Unmarshal%sInput(data); if err != nil { return nil, stream, err }\n", name)
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
