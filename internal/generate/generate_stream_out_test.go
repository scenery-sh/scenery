package generate

import (
	"strings"
	"testing"
)

func TestStreamOutCodegenAcrossRuntimesAndTypeScriptClient(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Module: "app", Name: "public", Kind: "scenery.http-gateway", Spec: map[string]any{"exposure": "internet", "base_path": "/api"}},
		{Address: "app/service/ticker", Module: "app", Name: "ticker", Kind: "scenery.service", Spec: map[string]any{
			"runtime":        "go",
			"implementation": map[string]any{"constructor": "NewTicker"},
		}},
		{Address: "app/record/watch_input", Module: "app", Name: "watch_input", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{map[string]any{"name": "symbol", "type": map[string]any{"$ref": "string"}}},
		}},
		{Address: "app/record/price_tick", Module: "app", Name: "price_tick", Kind: "scenery.record", Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "symbol", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "seq", "type": map[string]any{"$ref": "int64"}},
				map[string]any{"name": "price", "type": map[string]any{"$ref": "decimal"}},
			},
		}},
		{Address: "app/operation/watch_prices", Module: "app", Name: "watch_prices", Kind: "scenery.operation", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.ticker"},
			"input":   map[string]any{"$ref": "record.watch_input"},
			"handler": map[string]any{"method": "WatchPrices"},
			"result":  map[string]any{"name": "event", "type": map[string]any{"$ref": "record.price_tick"}},
			"error":   map[string]any{"name": "unknown_symbol", "type": map[string]any{"$ref": "std.type.problem"}},
		}},
		{Address: "app/binding/watch_prices_http", Module: "app", Name: "watch_prices_http", Kind: "scenery.binding", Spec: map[string]any{
			"gateway":   map[string]any{"$ref": "app/http_gateway/public"},
			"operation": map[string]any{"$ref": "operation.watch_prices"},
			"protocol":  "http",
			"delivery":  "stream_out",
			"http": map[string]any{
				"method": "GET",
				"path":   "/prices/{symbol}/events",
				"path_parameter": map[string]any{
					"name": "symbol",
					"to":   map[string]any{"$ref": "operation.watch_prices.input.symbol"},
				},
				"response": []any{
					map[string]any{
						"name": "event", "when": map[string]any{"$ref": "result.event"}, "status": "200",
						"body": map[string]any{"codec": "sse", "from": map[string]any{"$ref": "result.event"}},
					},
					map[string]any{
						"name": "unknown_symbol", "when": map[string]any{"$ref": "error.unknown_symbol"}, "status": "404",
						"body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "error.unknown_symbol"}},
					},
				},
			},
		}},
	}
	bindings := []Resource{resources[5]}
	operations := []Resource{resources[4]}

	// 1. Go contract & worker codegen
	idx := newResourceIndex(resources)
	typeResolver := newGoContractTypeResolver("app", resources)
	goContract, err := renderContractAPI("app", "example.com/app/scenerycontract", "abi", resources, idx, typeResolver)
	if err != nil {
		t.Fatalf("render Go contract API: %v", err)
	}
	for _, want := range []string{
		"type WatchPricesStream = scenery.EventStream[PriceTick]",
		"func MarshalWatchPricesEvent(value PriceTick)",
		"type WatchPricesEvent struct{}",
		`[]byte("{\"kind\":\"result\",\"name\":\"event\",\"value\":null}")`,
	} {
		if !strings.Contains(goContract, want) {
			t.Fatalf("Go contract missing %q:\n%s", want, goContract)
		}
	}
	var workerOps strings.Builder
	renderWorkerOperations(&workerOps, operations, bindings)
	if !strings.Contains(workerOps.String(), "RegisterWorkerEventOperation") || !strings.Contains(workerOps.String(), "events.Bind(contract.MarshalWatchPricesEvent)") {
		t.Fatalf("Go worker adapter missing event stream wiring:\n%s", workerOps.String())
	}

	// 2. Rust, TS, Python handler signatures
	for _, tc := range []struct {
		runtime string
		want    string
	}{
		{"rust", "Option<sdk::EventStream<WatchPricesEvent>>"},
		{"ts", "SDK.EventStream<Types.WatchPricesOutcome, Types.WatchPricesEvent>"},
		{"python", "EventStream[WatchPricesOutcome, WatchPricesEventData]"},
	} {
		svc := resources[1]
		svc.Spec = map[string]any{"runtime": tc.runtime, "implementation": map[string]any{"constructor": "NewTicker"}}
		sigs := strings.Join(WorkerSignatures(resources, svc), "\n")
		if !strings.Contains(sigs, tc.want) {
			t.Fatalf("%s WorkerSignatures missing %q:\n%s", tc.runtime, tc.want, sigs)
		}
	}

	// 3. TypeScript browser client types, runtime capability gating, and bindings
	tsTypes := renderTSTypes(resources, bindings)
	for _, want := range []string{
		"export interface ServerSentEvent<T>",
		`{ readonly kind: "result"; readonly name: "event"; readonly events: AsyncIterable<ServerSentEvent<PriceTick>> }`,
		`{ readonly kind: "error"; readonly name: "unknown_symbol"; readonly problem: Problem }`,
	} {
		if !strings.Contains(tsTypes, want) {
			t.Fatalf("TypeScript types.ts missing %q:\n%s", want, tsTypes)
		}
	}
	target := Resource{Address: "app/typescript_client/public_api", Name: "public_api", Kind: "scenery.typescript-client", Spec: map[string]any{}}
	caps := tsRuntimeCapabilitiesFor(target, bindings, resources)
	if !caps.sse {
		t.Fatal("expected tsRuntimeCapabilities.sse to be true")
	}
	sseRuntime := renderTSRuntime(caps)
	if !strings.Contains(sseRuntime, "export async function* decodeSSEStream") || !strings.Contains(sseRuntime, `headers.set("accept", media.length > 0 ? media.join(", ") : "text/event-stream, application/problem+json")`) {
		t.Fatal("SSE TypeScript runtime missing decodeSSEStream or accept header")
	}
	plainRuntime := renderTSRuntime(tsRuntimeCapabilities{})
	if strings.Contains(plainRuntime, "decodeSSEStream") || strings.Contains(plainRuntime, "streamOut") {
		t.Fatal("non-SSE TypeScript runtime leaked SSE runtime code")
	}
	tsClient := renderTSClient(target, bindings, resources)
	if !strings.Contains(tsClient, `"streamOut":true`) || !strings.Contains(tsClient, `"codec":"sse"`) || !strings.Contains(tsClient, `"text/event-stream"`) {
		t.Fatalf("TypeScript client.ts missing SSE binding descriptor:\n%s", tsClient)
	}
}
