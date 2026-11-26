package generate

import (
	"fmt"
	"strings"
)

// WorkerSignatures describes the source-level ABI enforced by the generated
// adapter, using the same method and type names as the renderer.
func WorkerSignatures(resources []Resource, service Resource) []string {
	runtime := stringValue(service.Spec["runtime"])
	constructor := stringValue(specChild(service, "implementation")["constructor"])
	serviceType := goName(service.Name)
	var signatures []string
	switch runtime {
	case "rust":
		signatures = append(signatures, fmt.Sprintf("pub fn %s(input: scenerycontract::%sConstructorInput) -> sdk::Result<impl scenerycontract::%sService>", rustMethodName(constructor), serviceType, serviceType))
	case "js":
		signatures = append(signatures, fmt.Sprintf("function %s(input /* ConstructorInput */) // returns Service or Promise<Service>", constructor))
	case "ts":
		signatures = append(signatures, fmt.Sprintf("%s(input: ConstructorInput): Service | Promise<Service>", constructor))
	default:
		return nil
	}
	for _, operation := range resources {
		if operation.Kind != "scenery.operation" || resolveResourceRef(operation, refString(operation.Spec["service"]), "service") != service.Address {
			continue
		}
		method := stringValue(specChild(operation, "handler")["method"])
		name := goName(operation.Name)
		streaming := operationUsesHTTPStream(operation, resources)
		if runtime == "rust" {
			signatures = append(signatures, rustOperationSignature(method, name, streaming))
			continue
		}
		if runtime == "js" {
			output := jsOperationOutput(name, streaming)
			signatures = append(signatures, fmt.Sprintf("%q(context /* SDK.Context */, input /* Types.%sInput */) // returns %s or Promise<%s>", method, name, output, output))
			continue
		}
		signatures = append(signatures, jsOperationSignature(method, name, streaming))
	}
	return signatures
}

func rustOperationSignature(method, name string, streaming bool) string {
	outcome := name + "Outcome"
	if streaming {
		outcome = "(" + outcome + ", Option<sdk::Stream>)"
	}
	return fmt.Sprintf("fn %s(&self, ctx: sdk::Context, input: %sInput) -> impl std::future::Future<Output = sdk::Result<%s>> + Send", rustMethodName(method), name, outcome)
}

func jsOperationSignature(method, name string, streaming bool) string {
	outcome := jsOperationOutput(name, streaming)
	return fmt.Sprintf("%q(context: SDK.Context, input: Types.%sInput): %s | Promise<%s>", method, name, outcome, outcome)
}

func jsOperationOutput(name string, streaming bool) string {
	outcome := "Types." + name + "Outcome"
	if streaming {
		outcome += " | SDK.Stream<" + outcome + ">"
	}
	return outcome
}

func goConstructorSignature(name, service, receiver string) string {
	return fmt.Sprintf("func %s(ctx context.Context, input scenerycontract.%sConstructorInput) (*%s, error)", name, goName(service), receiver)
}

func goHandlerSignature(method, name, receiver string, streaming bool) string {
	outcome := "scenerycontract." + goName(name) + "Outcome"
	if streaming {
		outcome += ", scenery.ByteStream"
	}
	return fmt.Sprintf("func (service *%s) %s(ctx context.Context, input scenerycontract.%sInput) (%s, error)", receiver, method, goName(name), outcome)
}

func expectedSignature(value string) string { return "Expected signature: " + strings.TrimSpace(value) }
