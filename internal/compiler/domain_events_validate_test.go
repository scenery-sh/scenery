package compiler

import (
	"maps"
	"testing"
)

func TestCompileDomainEvents(t *testing.T) {
	result, err := Compile("../../testdata/apps/polyglot")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("polyglot event declarations: %+v", result.Diagnostics)
	}
	canonical := resourcesByAddress(result.Manifest)
	event := canonical["native/event/accepted"]
	service := canonical["consumer/service/consumer"]
	publisher := namedChildren(service.Spec, "publisher")[0]
	if refString(publisher["event"]) != event.Address {
		t.Fatalf("publisher did not resolve imported event: %#v", publisher)
	}
	if sub := canonical["pricing/subscription/receive_order"]; refString(sub.Spec["event"]) != event.Address {
		t.Fatalf("subscription did not resolve imported event: %#v", sub)
	}
	projected, diagnostics := ProjectSQLRequirements([]Resource{event}, false)
	if len(diagnostics) != 0 || len(projected) != 1 || projected[0].Kind != SQLDomainEvents || projected[0].Schema != "scenery" {
		t.Fatalf("standalone event SQL requirement: %+v, %+v", projected, diagnostics)
	}

	for _, test := range []struct {
		name, address, field, code string
		value                      any
	}{
		{"unknown payload", event.Address, "payload", "SCN2310", map[string]any{"$ref": "record.missing"}},
		{"idempotency path", event.Address, "idempotency", "SCN2312", map[string]any{"mode": "keyed", "key": []any{map[string]any{"$ref": "payload.missing"}}}},
		{"empty identity", event.Address, "external_name", "SCN2311", " "},
		{"duplicate identity", "native/event/delivery_marker", "external_name", "SCN2311", event.Spec["external_name"]},
		{"publisher ref", service.Address, "publisher", "SCN2313", map[string]any{"name": "missing", "event": map[string]any{"$ref": "event.missing"}}},
		{"non-durable subscriber", "pricing/execution/receive_order", "mode", "SCN2314", "direct"},
		{"mismatched payload", "typescript/operation/receive_order", "input", "SCN2315", map[string]any{"$ref": "record.ts_quote_input"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources := append([]Resource{}, result.Manifest.Resources...)
			for i := range resources {
				if resources[i].Address == test.address {
					resources[i].Spec = maps.Clone(resources[i].Spec)
					resources[i].Spec[test.field] = test.value
				}
			}
			diagnostics := append(validateTypeSystem(resources), validateResourceSemantics(resources)...)
			if !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("wanted %s, got %+v", test.code, diagnostics)
			}
		})
	}
	sub := canonical["pricing/subscription/receive_order"]
	sub.Address = "pricing/subscription/duplicate"
	resources := append(append([]Resource{}, result.Manifest.Resources...), sub)
	if diagnostics := validateResourceSemantics(resources); !hasDiagnostic(diagnostics, "SCN2314") {
		t.Fatalf("duplicate subscription was accepted: %+v", diagnostics)
	}
}

func TestDomainEventWireCompatibilityChecksNestedMembers(t *testing.T) {
	makeRecord := func(module, name, field, wire, ty string) Resource {
		return Resource{
			Module: module, Kind: "scenery.record", Name: name, Address: module + "/record/" + name,
			Spec: map[string]any{"field": map[string]any{"name": field, "wire_name": wire, "type": map[string]any{"$expression": ty}}},
		}
	}
	left := makeRecord("producer", "payload", "items", "items", "list(record.item)")
	right := makeRecord("consumer", "payload", "payload_items", "items", "list(record.item)")
	leftItem := makeRecord("producer", "item", "key", "key", "optional(nullable(string))")
	rightItem := makeRecord("consumer", "item", "id", "key", "optional(nullable(string))")
	check := func(want bool) {
		t.Helper()
		resources := resourcesByAddress(&Manifest{Resources: []Resource{left, right, leftItem, rightItem}})
		if got := matchingEventWireType(resources, left, "record.payload", right, "record.payload", map[[2]string]bool{}); got != want {
			t.Errorf("compatible = %t, want %t", got, want)
		}
	}
	check(true)
	rightItem.Spec["field"].(map[string]any)["default"] = nil
	check(false)
	rightItem = makeRecord("consumer", "item", "id", "key", "optional(string)")
	check(false)
	rightItem = makeRecord("consumer", "item", "id", "different", "optional(nullable(string))")
	check(false)
}

func TestDomainEventWireCompatibilityCoversUnionsEnumsRecursionAndConstraints(t *testing.T) {
	// Recursive records
	leftNode := Resource{
		Module: "producer", Kind: "scenery.record", Name: "node", Address: "producer/record/node",
		Spec: map[string]any{"field": []any{
			map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}, "pattern": "^[a-z]+$"},
			map[string]any{"name": "next", "type": map[string]any{"$expression": "optional(record.node)"}},
		}},
	}
	rightNode := Resource{
		Module: "consumer", Kind: "scenery.record", Name: "item", Address: "consumer/record/item",
		Spec: map[string]any{"field": []any{
			map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}, "pattern": "^[a-z]+$"},
			map[string]any{"name": "next", "type": map[string]any{"$expression": "optional(record.item)"}},
		}},
	}
	checkPair := func(res []Resource, left Resource, a string, right Resource, b string, want bool) {
		t.Helper()
		byAddr := resourcesByAddress(&Manifest{Resources: res})
		if got := matchingEventWireType(byAddr, left, a, right, b, map[[2]string]bool{}); got != want {
			t.Errorf("matchingEventWireType(%s, %s) = %t, want %t", a, b, got, want)
		}
	}
	checkPair([]Resource{leftNode, rightNode}, leftNode, "record.node", rightNode, "record.item", true)
	// Constraint mismatch
	rightNodeBadPattern := rightNode
	rightNodeBadPattern.Spec = map[string]any{"field": []any{
		map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}, "pattern": "^[0-9]+$"},
		map[string]any{"name": "next", "type": map[string]any{"$expression": "optional(record.item)"}},
	}}
	checkPair([]Resource{leftNode, rightNodeBadPattern}, leftNode, "record.node", rightNodeBadPattern, "record.item", false)
	// unknown_fields mismatch
	rightNodeIgnore := rightNode
	rightNodeIgnore.Spec = maps.Clone(rightNode.Spec)
	rightNodeIgnore.Spec["unknown_fields"] = "ignore"
	checkPair([]Resource{leftNode, rightNodeIgnore}, leftNode, "record.node", rightNodeIgnore, "record.item", false)

	// Enum wire_value and open mismatch
	leftEnum := Resource{
		Module: "producer", Kind: "scenery.enum", Name: "status", Address: "producer/enum/status",
		Spec: map[string]any{"open": false, "value": map[string]any{"name": "ok", "wire_value": "OK"}},
	}
	rightEnum := Resource{
		Module: "consumer", Kind: "scenery.enum", Name: "state", Address: "consumer/enum/state",
		Spec: map[string]any{"open": false, "value": map[string]any{"name": "accepted", "wire_value": "OK"}},
	}
	checkPair([]Resource{leftEnum, rightEnum}, leftEnum, "enum.status", rightEnum, "enum.state", true)
	rightEnumOpen := rightEnum
	rightEnumOpen.Spec = maps.Clone(rightEnum.Spec)
	rightEnumOpen.Spec["open"] = true
	checkPair([]Resource{leftEnum, rightEnumOpen}, leftEnum, "enum.status", rightEnumOpen, "enum.state", false)

	// Union discriminator and wire_tag
	leftRec := Resource{Module: "producer", Kind: "scenery.record", Name: "a", Address: "producer/record/a", Spec: map[string]any{"field": map[string]any{"name": "n", "type": map[string]any{"$ref": "int64"}, "minimum": int64(1)}}}
	rightRec := Resource{Module: "consumer", Kind: "scenery.record", Name: "b", Address: "consumer/record/b", Spec: map[string]any{"field": map[string]any{"name": "n", "type": map[string]any{"$ref": "int64"}, "minimum": int64(1)}}}
	leftUnion := Resource{
		Module: "producer", Kind: "scenery.union", Name: "u", Address: "producer/union/u",
		Spec: map[string]any{"discriminator": "kind", "variant": map[string]any{"name": "first", "wire_tag": "v1", "type": map[string]any{"$ref": "record.a"}}},
	}
	rightUnion := Resource{
		Module: "consumer", Kind: "scenery.union", Name: "u", Address: "consumer/union/u",
		Spec: map[string]any{"discriminator": "kind", "variant": map[string]any{"name": "one", "wire_tag": "v1", "type": map[string]any{"$ref": "record.b"}}},
	}
	checkPair([]Resource{leftRec, rightRec, leftUnion, rightUnion}, leftUnion, "union.u", rightUnion, "union.u", true)
	rightUnionBadDisc := rightUnion
	rightUnionBadDisc.Spec = maps.Clone(rightUnion.Spec)
	rightUnionBadDisc.Spec["discriminator"] = "type"
	checkPair([]Resource{leftRec, rightRec, leftUnion, rightUnionBadDisc}, leftUnion, "union.u", rightUnionBadDisc, "union.u", false)
}

func TestPolyglotPublisherIdentifierCollisions(t *testing.T) {
	event := Resource{
		Module: "events", Kind: "scenery.event", Name: "order", Address: "events/event/order",
		Spec: map[string]any{"payload": map[string]any{"$ref": "record.order"}},
	}
	payload := Resource{
		Module: "events", Kind: "scenery.record", Name: "order", Address: "events/record/order",
		Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}},
	}
	rustService := Resource{
		Module: "rustmod", Kind: "scenery.service", Name: "pricing", Address: "rustmod/service/pricing",
		Spec: map[string]any{
			"runtime":   "rust",
			"client":    map[string]any{"name": "publishers", "binding": map[string]any{"$ref": "binding.x"}},
			"publisher": map[string]any{"name": "order", "event": map[string]any{"$ref": event.Address}},
		},
	}
	if diags := validateDomainEvents([]Resource{event, payload, rustService}, resourcesByAddress(&Manifest{Resources: []Resource{event, payload, rustService}})); !hasDiagnostic(diags, "SCN2313") {
		t.Fatalf("expected SCN2313 for Rust client named publishers, got %+v", diags)
	}
	tsService := Resource{
		Module: "tsmod", Kind: "scenery.service", Name: "ts", Address: "tsmod/service/ts",
		Spec: map[string]any{
			"runtime":   "ts",
			"publisher": map[string]any{"name": "order", "event": map[string]any{"$ref": event.Address}},
		},
	}
	tsCollision := Resource{
		Module: "tsmod", Kind: "scenery.record", Name: "event_receipt", Address: "tsmod/record/event_receipt",
		Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}},
	}
	tsRes := []Resource{event, payload, tsService, tsCollision}
	if diags := validateDomainEvents(tsRes, resourcesByAddress(&Manifest{Resources: tsRes})); !hasDiagnostic(diags, "SCN2313") {
		t.Fatalf("expected SCN2313 for TS EventReceipt collision, got %+v", diags)
	}
	// Self-named payload record in the same module is allowed (JS codegen skips self-alias).
	selfPayload := Resource{
		Module: "tsmod", Kind: "scenery.record", Name: "order_event_payload", Address: "tsmod/record/order_event_payload",
		Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}},
	}
	selfEvent := Resource{
		Module: "tsmod", Kind: "scenery.event", Name: "order", Address: "tsmod/event/order",
		Spec: map[string]any{"payload": map[string]any{"$ref": "record.order_event_payload"}},
	}
	tsSelfService := Resource{
		Module: "tsmod", Kind: "scenery.service", Name: "ts", Address: "tsmod/service/ts",
		Spec: map[string]any{
			"runtime":   "ts",
			"publisher": map[string]any{"name": "order", "event": map[string]any{"$ref": selfEvent.Address}},
		},
	}
	selfRes := []Resource{selfPayload, selfEvent, tsSelfService}
	if diags := validateDomainEvents(selfRes, resourcesByAddress(&Manifest{Resources: selfRes})); hasDiagnostic(diags, "SCN2313") {
		t.Fatalf("self-named TS payload record should be accepted, got %+v", diags)
	}
}
