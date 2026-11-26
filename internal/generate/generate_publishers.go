package generate

import (
	"encoding/json"
	"fmt"
	"strings"
)

type eventPublisher struct {
	Name  string
	Event Resource
}

func serviceEventPublishers(resources map[string]Resource, service Resource) []eventPublisher {
	var publishers []eventPublisher
	for _, publisher := range namedChildren(service.Spec, "publisher") {
		event := resources[resolveResourceRef(service, refString(publisher["event"]), "event")]
		publishers = append(publishers, eventPublisher{Name: stringValue(publisher["name"]), Event: event})
	}
	return publishers
}

func (publisher eventPublisher) goPayload(module string) any {
	payload := publisher.Event.Spec["payload"]
	if publisher.Event.Module != module {
		return qualifyProjectedGoType(payload, publisher.Event.Module)
	}
	return payload
}

func renderGoPublisherContracts(b *strings.Builder, service Resource, publishers []eventPublisher, resolver *goContractTypeResolver) {
	if len(publishers) == 0 {
		return
	}
	for _, publisher := range publishers {
		name := goName(publisher.Name)
		payload := resolver.Type(publisher.goPayload(service.Module))
		fmt.Fprintf(b, "type %sEventPayload = %s\n", name, payload)
		fmt.Fprintf(b, "type %sEventPublisher interface { Emit(context.Context, %sEventPayload) (EventReceipt, error) }\n\n", name, name)
	}
	fmt.Fprintf(b, "type %sPublishers struct {\n", goName(service.Name))
	for _, publisher := range publishers {
		name := goName(publisher.Name)
		fmt.Fprintf(b, "\t%s %sEventPublisher\n", name, name)
	}
	b.WriteString("}\n\n")
}

func renderWireGoPublisher(b *strings.Builder, publisher eventPublisher) {
	name := goName(publisher.Name)
	adapter := semanticPathName(publisher.Name) + "EventPublisher"
	fmt.Fprintf(b, "type %s struct{}\n", adapter)
	fmt.Fprintf(b, "func (%s) Emit(ctx context.Context, payload contract.%sEventPayload) (contract.EventReceipt, error) {\n", adapter, name)
	fmt.Fprintf(b, "\traw, err := scenery.MarshalContractValue(payload, %q)\n", goWireTypeExpression(publisher.Event.Spec["payload"]))
	b.WriteString("\tif err != nil { return contract.EventReceipt{}, fmt.Errorf(\"encode event payload: %w\", err) }\n")
	b.WriteString("\tinvocation, _ := scenery.InvocationFromContext(ctx)\n")
	fmt.Fprintf(b, "\toutput, err := sceneryruntime.EmitWorkerEvent(ctx, invocation, %q, raw)\n", publisher.Name)
	b.WriteString("\tif err != nil { return contract.EventReceipt{}, err }\n\tvar receipt contract.EventReceipt\n")
	b.WriteString("\tif err := scenery.UnmarshalContractValue(output, &receipt, \"std.type.event_receipt\"); err != nil { return contract.EventReceipt{}, sceneryruntime.ContractSystemError(err) }\n")
	b.WriteString("\treturn receipt, nil\n}\n\n")
}

func (p rustContractProjection) renderRustPublishers(source *strings.Builder, service string, publishers []eventPublisher) {
	if len(publishers) == 0 {
		return
	}
	fmt.Fprintf(source, "#[derive(Default)]\npub struct %sPublishers {\n", service)
	for _, publisher := range publishers {
		fmt.Fprintf(source, "    pub %s: %sPublisher,\n", rustIdent(publisher.Name), goName(publisher.Name))
	}
	source.WriteString("}\n\n")
	for _, publisher := range publishers {
		name := goName(publisher.Name) + "Publisher"
		payload := p.rustType(publisher.Event.Spec["payload"], publisher.Event.Module)
		fmt.Fprintf(source, "#[derive(Clone, Copy, Default)]\npub struct %s;\nimpl %s {\n", name, name)
		fmt.Fprintf(source, "    pub async fn emit(&self, ctx: &sdk::Context, payload: %s) -> sdk::Result<sdk::EventReceipt> {\n        ctx.emit_event(%q, &payload).await\n    }\n}\n\n", payload, publisher.Name)
	}
}

func (p jsProjection) renderJSPublishers(types, api *strings.Builder, publishers []eventPublisher) {
	if len(publishers) == 0 {
		return
	}
	types.WriteString("export type { EventReceipt } from \"./worker.mjs\";\n")
	api.WriteString("export const publishers = Object.freeze({\n")
	for _, publisher := range publishers {
		value, module := publisher.Event.Spec["payload"], publisher.Event.Module
		alias := goName(publisher.Name) + "EventPayload"
		if payload := p.typeExpression(value, module); payload != alias {
			fmt.Fprintf(types, "export type %s = %s;\n", alias, payload)
		}
		descriptor, _ := json.Marshal(tsDescriptor(value, module))
		fmt.Fprintf(api, "  %q: { async emit(ctx: SDK.Context, payload: Types.%s): Promise<Types.EventReceipt> {\n"+
			"    const wire = Runtime.encodeTypedJSON(payload, %s, typeRegistry);\n"+
			"    return ctx.eventEmit(%q, wire);\n  } },\n", publisher.Name, alias, descriptor, publisher.Name)
	}
	api.WriteString("});\n")
}
