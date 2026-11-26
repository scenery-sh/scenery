package generate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"scenery.sh/internal/compiler"
	sdksource "scenery.sh/sdk/js"
)

func jsServices(result *compiler.Result) []Resource {
	return slices.Collect(func(yield func(Resource) bool) {
		for _, resource := range result.Manifest.Resources {
			if resource.Kind == "scenery.service" && compiler.IsJSRuntime(stringValue(resource.Spec["runtime"])) && !yield(resource) {
				return
			}
		}
	})
}

func renderJSContractFiles(result *compiler.Result, services []Resource) ([]generatedFile, error) {
	idx := newResourceIndex(result.Manifest.Resources)
	var files []generatedFile
	directories := make(map[string]bool)
	for _, service := range services {
		target, err := compiler.SelectJSWorkerTarget(result, service, "development")
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(result.Root, target.Directory, "scenerycontract")
		if !managedContractDirectory(result, dir) {
			return nil, fmt.Errorf("contract output for JavaScript at %s is outside managed_generated_roots", dir)
		}
		if directories[dir] {
			return nil, fmt.Errorf("JavaScript contract directory %s is used by more than one service", dir)
		}
		directories[dir] = true
		entries, err := sdksource.SDK.ReadDir("scenery-sdk")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			data, err := fs.ReadFile(sdksource.SDK, "scenery-sdk/"+entry.Name())
			if err != nil {
				return nil, err
			}
			files = append(files, generatedFile{Path: filepath.Join(dir, entry.Name()), Bytes: data})
		}
		types, contract, entry, err := renderJSService(service, target, idx, result.Manifest.Resources)
		if err != nil {
			return nil, err
		}
		sources := map[string]string{
			"codec.ts": renderJSWorkerCodec(), "types.ts": types, "index.ts": contract, "main.ts": entry,
		}
		for _, name := range slices.Sorted(maps.Keys(sources)) {
			files = append(files, generatedFile{Path: filepath.Join(dir, name), Bytes: []byte(sources[name])})
		}
	}
	return files, nil
}

func renderJSWorkerCodec() string {
	return renderTSRuntimeSections(renderTSRuntimePublic()+renderTSRuntimeInternals(), tsRuntimeCapabilities{}) + `
export function decodeWorkerValue(value: unknown, descriptor: TypeDescriptor, registry: TypeRegistry): unknown {
  return decodeTypedValue(value, descriptor, registry, "$", new Set());
}
`
}

type jsProjection struct {
	module string
	types  map[string]Resource
}

func renderJSService(service Resource, target compiler.JSWorkerTarget, idx *resourceIndex, resources []Resource) (string, string, string, error) {
	operations := compiler.ServiceOperations(resources, service)
	clients, err := serviceGoClients(idx, service)
	if err != nil {
		return "", "", "", err
	}
	publishers := serviceEventPublishers(idx.byAddress, service)
	reachable := make(map[string]bool)
	collect := func(resource Resource) {
		for _, value := range contractTypeValues([]Resource{resource}) {
			collectABITypeReferences(value, resource.Module, idx.byAddress, reachable)
		}
	}
	for _, operation := range operations {
		collect(operation)
	}
	for _, client := range clients {
		collect(client.Operation)
	}
	for _, publisher := range publishers {
		collect(publisher.Event)
	}
	for before := -1; before != len(reachable); {
		before = len(reachable)
		for address := range reachable {
			collect(idx.byAddress[address])
		}
	}

	projection := jsProjection{module: service.Module, types: make(map[string]Resource)}
	var shapes []Resource
	for address := range reachable {
		projection.types[address] = idx.byAddress[address]
		shapes = append(shapes, idx.byAddress[address])
	}
	slices.SortFunc(shapes, func(a, b Resource) int { return strings.Compare(a.Address, b.Address) })

	var types, api, entry strings.Builder
	types.WriteString(renderTSTypes(nil))
	api.WriteString("// Code generated for the application. DO NOT EDIT.\n" +
		"import * as Runtime from \"./codec.js\";\n" +
		"import * as SDK from \"./worker.mjs\";\n" +
		"import type * as Types from \"./types.js\";\n" +
		"export * from \"./types.js\";\n" +
		"export { CapabilityError, eventStream, sseEvent, standaloneContext, stream } from \"./worker.mjs\";\n" +
		"export type { Context, ConfigSnapshot, EventStream, EventStreamBody, SSEEvent, StandaloneContextOptions, Stream } from \"./worker.mjs\";\n" +
		"export { date, dateTime, decimal, duration, jsonNumber, relativePath, url, uuid } from \"./codec.js\";\n")
	api.WriteString(renderTSRegistry(shapes))
	for _, shape := range shapes {
		projection.renderShape(&types, shape)
	}

	fmt.Fprintf(&entry, "// Code generated for the application. DO NOT EDIT.\n"+
		"import * as SDK from \"./worker.mjs\";\n"+
		"import * as Contract from \"./index.js\";\n"+
		"import { parseExactJSON } from \"./codec.js\";\n"+
		"import { %s as construct } from %q;\n\n",
		stringValue(specChild(service, "implementation")["constructor"]), "../"+filepath.ToSlash(target.Entry))
	entry.WriteString("const launch = SDK.readLaunch();\n" +
		"if (launch.identity.service !== Contract.serviceAddress) throw new Error(\"worker service mismatch\");\n")
	publisherInput := ""
	if len(publishers) > 0 {
		publisherInput = ", publishers: Contract.publishers"
	}
	if runtime := stringValue(service.Spec["runtime"]); runtime == "js" {
		fmt.Fprintf(&entry, `// JavaScript object literals infer string discriminants without annotations.
type JSOutput<T> = T extends Promise<infer R> ? Promise<JSOutput<R>>
  : T extends SDK.Stream<infer R> ? SDK.Stream<JSOutput<R>>
  : T extends SDK.EventStream<infer R, infer E> ? SDK.EventStream<JSOutput<R>, E>
  : T extends { kind: string; name: string } ? Omit<T, "kind" | "name"> & { kind: string; name: string }
  : T;
type JSService = { [K in keyof Contract.Service]: (...args: Parameters<Contract.Service[K]>) => JSOutput<ReturnType<Contract.Service[K]>> };
const create: (input: Contract.ConstructorInput) => JSService | Promise<JSService> = construct;
const service = await create({ clients: Contract.clients%s, ...SDK.startupInputs(parseExactJSON) }) as Contract.Service;
`, publisherInput)
	} else {
		fmt.Fprintf(&entry, "const service: Contract.Service = await construct({ clients: Contract.clients%s, ...SDK.startupInputs(parseExactJSON) });\n", publisherInput)
	}
	fmt.Fprintf(&api, "export const serviceAddress = %q;\n", service.Address)
	projection.renderClients(&types, &api, clients)
	projection.renderJSPublishers(&types, &api, publishers)
	publisherField := ""
	if len(publishers) > 0 {
		publisherField = "  readonly publishers: typeof publishers;\n"
	}
	api.WriteString("export interface ConstructorInput {\n" +
		"  readonly clients: typeof clients;\n" + publisherField +
		"  readonly config: SDK.ConfigSnapshot | undefined;\n" +
		"  readonly databases: Readonly<Record<string, string>>;\n}\n" +
		"export type Constructor = (input: ConstructorInput) => Service | Promise<Service>;\n\n")

	var handlers strings.Builder
	api.WriteString("export interface Service {\n")
	lifecycle := specChild(service, "lifecycle")
	for _, phase := range []string{"start", "stop"} {
		if method := stringValue(lifecycle[phase]); method != "" {
			fmt.Fprintf(&api, "  %q(): void | Promise<void>;\n", method)
			fmt.Fprintf(&entry, "if (typeof service[%q] !== \"function\") throw new Error(\"missing lifecycle hook\");\n", method)
		}
	}
	for _, operation := range operations {
		name := goName(operation.Name)
		method := stringValue(specChild(operation, "handler")["method"])
		if method == "" {
			return "", "", "", fmt.Errorf("JavaScript operation %s requires a handler method", operation.Address)
		}
		canStream := operationUsesHTTPStream(operation, resources)
		canStreamOut := operationUsesHTTPStreamOut(operation, resources)
		projection.renderOperation(&types, operation, name, canStreamOut)
		codecName := name + "Codec"
		fmt.Fprintf(&api, "  %s;\n", jsOperationSignature(method, name, canStream, canStreamOut))
		fmt.Fprintf(&entry, "if (typeof service[%q] !== \"function\") throw new Error(\"missing worker handler\");\n", method)
		fmt.Fprintf(&handlers, "const %s = SDK.outcomeCodec(Runtime, typeRegistry, %s as const);\n", codecName, jsOutcomeDescriptors(operation))
		if canStreamOut {
			resultVariant, _ := operationStreamOutResult(operation)
			resultName := stringValue(resultVariant["name"])
			eventDescBytes, _ := json.Marshal(tsDescriptor(resultVariant["type"], operation.Module))
			fmt.Fprintf(&handlers, "const %sEventDescriptor = %s as const;\n", name, string(eventDescBytes))
			fmt.Fprintf(&handlers, "async function dispatch%s(service: Service, input: unknown, ctx: SDK.Context) {\n"+
				"  const decoded = Runtime.decodeWorkerValue(input, %s, typeRegistry) as Types.%sInput;\n"+
				"  const output = await service[%q](ctx, decoded);\n"+
				"  const { outcome, events } = SDK.unpackEvents<Types.%sOutcome, Types.%sEvent>(output);\n"+
				"  if (outcome !== null && typeof outcome === \"object\" && outcome.kind === \"result\" && outcome.name === %q) {\n"+
				"    if (events === undefined) throw new Error(\"stream_out result requires an EventStream\");\n"+
				"    const encodedEvents = (async function* () {\n"+
				"      for await (const item of events) {\n"+
				"        const ev = SDK.isSSEEvent<Types.%sEvent>(item) ? item : { data: item, id: undefined, event: undefined };\n"+
				"        const data = Runtime.encodeTypedJSON(ev.data, %sEventDescriptor, typeRegistry);\n"+
				"        let line = '{\"data\":' + data;\n"+
				"        if (ev.id !== undefined) line += ',\"id\":' + JSON.stringify(ev.id);\n"+
				"        if (ev.event !== undefined) line += ',\"event\":' + JSON.stringify(ev.event);\n"+
				"        yield line + '}';\n"+
				"      }\n"+
				"    })();\n"+
				"    return { outcome: '{\"kind\":\"result\",\"name\":' + JSON.stringify(%q) + ',\"value\":null}', events: encodedEvents };\n"+
				"  }\n"+
				"  if (events !== undefined) throw new Error(\"stream_out error outcome cannot attach an EventStream\");\n"+
				"  return { outcome: %s.encode(outcome) };\n}\n",
				name, jsInputDescriptor(operation), name, method, name, name, resultName, name, name, resultName, codecName)
			continue
		}
		fmt.Fprintf(&handlers, "async function dispatch%s(service: Service, input: unknown, ctx: SDK.Context) {\n"+
			"  const decoded = Runtime.decodeWorkerValue(input, %s, typeRegistry) as Types.%sInput;\n"+
			"  const output = await service[%q](ctx, decoded);\n", name, jsInputDescriptor(operation), name, method)
		if canStream {
			fmt.Fprintf(&handlers, "  const { outcome, body, size } = SDK.unpack<Types.%sOutcome>(output);\n"+
				"  return { outcome: %s.encode(outcome), body, size };\n}\n", name, codecName)
		} else {
			fmt.Fprintf(&handlers, "  return { outcome: %s.encode(output) };\n}\n", codecName)
		}
	}
	api.WriteString("}\n\n")
	api.WriteString(handlers.String())
	api.WriteString("export async function dispatch(service: Service, operation: string, input: unknown, ctx: SDK.Context) {\n  switch (operation) {\n")
	for _, operation := range operations {
		fmt.Fprintf(&api, "    case %q: return dispatch%s(service, input, ctx);\n", operation.Address, goName(operation.Name))
	}
	api.WriteString("    default: throw new Error(\"unknown worker operation\");\n  }\n}\n")
	if method := stringValue(lifecycle["start"]); method != "" {
		fmt.Fprintf(&entry, "await service[%q]();\n", method)
	}
	entry.WriteString("try {\n  await SDK.serve(launch, parseExactJSON, (operation, input, context) => Contract.dispatch(service, operation, input, context));\n} finally {\n")
	if method := stringValue(lifecycle["stop"]); method != "" {
		fmt.Fprintf(&entry, "  await service[%q]();\n", method)
	}
	entry.WriteString("}\n")
	return types.String(), api.String(), entry.String(), nil
}

func (p jsProjection) renderClients(types, api *strings.Builder, clients []goClientBinding) {
	api.WriteString("export const clients = Object.freeze({\n")
	for _, client := range clients {
		op := client.Operation
		name := client.Field + "Client"
		p.renderOperation(types, op, name, false)
		if client.Delivery == "enqueue" {
			fmt.Fprintf(api, "  %q: { async enqueue(ctx: SDK.Context, input: Types.%sInput): Promise<Types.EnqueueReceipt> {\n"+
				"    const wire = Runtime.encodeTypedJSON(input, %s, typeRegistry);\n"+
				"    const reply = await ctx.bindingRequest(%q, wire, true);\n"+
				"    return Runtime.decodeWorkerValue(reply, { kind: \"primitive\", name: \"execution_receipt\" }, typeRegistry) as Types.EnqueueReceipt;\n  } },\n",
				client.Name, name, jsInputDescriptor(op), client.Name)
		} else {
			fmt.Fprintf(api, "  %q: { async invoke(ctx: SDK.Context, input: Types.%sInput): Promise<Types.%sOutcome> {\n"+
				"    const wire = Runtime.encodeTypedJSON(input, %s, typeRegistry);\n"+
				"    const reply = await ctx.bindingRequest(%q, wire);\n"+
				"    return SDK.outcomeCodec(Runtime, typeRegistry, %s as const).decode(reply) as Types.%sOutcome;\n  } },\n",
				client.Name, name, name, jsInputDescriptor(op), client.Name, jsOutcomeDescriptors(op), name)
		}
	}
	api.WriteString("});\n")
}

func (p jsProjection) renderOperation(source *strings.Builder, operation Resource, name string, streamOut bool) {
	input := p.typeExpression(operation.Spec["input"], operation.Module)
	if input != name+"Input" {
		fmt.Fprintf(source, "export type %sInput = %s;\n", name, input)
	}
	if streamOut {
		if resultVariant, ok := operationStreamOutResult(operation); ok {
			fmt.Fprintf(source, "export type %sEvent = %s;\n", name, p.typeExpression(resultVariant["type"], operation.Module))
		}
	}
	fmt.Fprintf(source, "export type %sOutcome =\n", name)
	count := 0
	for _, kind := range []string{"result", "error"} {
		field := "value"
		if kind == "error" {
			field = "problem"
		}
		for _, variant := range namedChildren(operation.Spec, kind) {
			if streamOut && kind == "result" {
				fmt.Fprintf(source, "  | { readonly kind: %q; readonly name: %q }\n", kind, stringValue(variant["name"]))
				count++
				continue
			}
			fmt.Fprintf(source, "  | { readonly kind: %q; readonly name: %q; readonly %s: %s }\n", kind,
				stringValue(variant["name"]), field, p.typeExpression(variant["type"], operation.Module))
			count++
		}
	}
	if count == 0 {
		source.WriteString("  never\n")
	}
	source.WriteString(";\n\n")
}

func jsInputDescriptor(operation Resource) string {
	data, _ := json.Marshal(tsDescriptor(operation.Spec["input"], operation.Module))
	return string(data)
}

func jsOutcomeDescriptors(operation Resource) string {
	variants := make(map[string]any)
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(operation.Spec, kind) {
			variants[kind+"."+stringValue(variant["name"])] = tsDescriptor(variant["type"], operation.Module)
		}
	}
	data, _ := json.Marshal(variants)
	return string(data)
}

func (p jsProjection) name(resource Resource) string {
	if resource.Module == p.module {
		return goName(resource.Name)
	}
	return goName(strings.ReplaceAll(resource.Module, "/", "_")) + goName(resource.Name)
}

func (p jsProjection) typeExpression(value any, module string) string {
	return p.renderExpression(qualifyProjectedGoExpression(typeExpression(value), module))
}

func (p jsProjection) renderExpression(expression string) string {
	if resource, ok := p.types[expression]; ok {
		return p.name(resource)
	}
	if name, arguments, ok := parseTSExpression(expression); ok {
		for index, argument := range arguments {
			arguments[index] = p.renderExpression(argument)
		}
		switch name {
		case "optional":
			return arguments[0]
		case "nullable":
			return arguments[0] + " | null"
		case "list", "set":
			return "ReadonlyArray<" + arguments[0] + ">"
		case "map":
			return "Readonly<Record<string, " + arguments[0] + ">>"
		case "tuple":
			return "readonly [" + strings.Join(arguments, ", ") + "]"
		}
	}
	return tsType(map[string]any{"$ref": expression})
}

func (p jsProjection) renderShape(source *strings.Builder, resource Resource) {
	name := p.name(resource)
	switch resource.Kind {
	case "scenery.record":
		fmt.Fprintf(source, "export interface %s {\n", name)
		for _, field := range namedChildren(resource.Spec, "field") {
			fmt.Fprintf(source, "  readonly %s%s: %s;\n", tsName(stringValue(field["name"])), tsOptional(field["type"]), p.typeExpression(field["type"], resource.Module))
		}
		if resource.Spec["unknown_fields"] == "preserve" {
			source.WriteString("  readonly unknownFields: Readonly<Record<string, JsonValue>>;\n")
		}
		source.WriteString("}\n\n")
	case "scenery.enum":
		values := namedChildren(resource.Spec, "value")
		var variants []string
		for _, item := range values {
			variants = append(variants, strconv.Quote(wireName(item, stringValue(item["name"]))))
		}
		if resource.Spec["open"] == true {
			variants = append(variants, "(string & { readonly __unknown"+name+": unique symbol })")
		}
		if len(variants) == 0 {
			variants = append(variants, "never")
		}
		fmt.Fprintf(source, "export type %s = %s;\n\n", name, strings.Join(variants, " | "))
	case "scenery.union":
		fmt.Fprintf(source, "export type %s =\n", name)
		for _, variant := range namedChildren(resource.Spec, "variant") {
			fmt.Fprintf(source, "  | { readonly kind: %q; readonly value: %s }\n", wireName(variant, stringValue(variant["name"])), p.typeExpression(variant["type"], resource.Module))
		}
		if resource.Spec["open"] == true {
			source.WriteString("  | { readonly kind: string; readonly value: JsonValue; readonly unknown: true }\n")
		}
		source.WriteString(";\n\n")
	}
}
