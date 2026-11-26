package compiler

import (
	"bytes"
	"reflect"
	"strings"
)

func validateDomainEvents(resources []Resource, byAddress map[string]Resource) []Diagnostic {
	var diagnostics []Diagnostic
	names := map[string]Resource{}
	routes := map[[2]string]bool{}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.event":
			name := resource.Address
			if raw, set := resource.Spec["external_name"]; set {
				name = stringValue(raw)
			}
			if previous, exists := names[name]; exists {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2311", Severity: "error", Message: "event external_name must be globally unique", Address: resource.Address, Related: []Related{{Address: previous.Address}}})
			} else if strings.TrimSpace(name) == "" {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2311", "event external_name must not be empty", resource))
			} else {
				names[name] = resource
			}
			if !validEventKey(byAddress, resource) {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2312", "event idempotency must be none without a key or keyed with direct payload record fields", resource))
			}
		case "scenery.service":
			publishers := namedChildren(resource.Spec, "publisher")
			for _, publisher := range publishers {
				if byAddress[resolveResourceRef(resource, refString(publisher["event"]), "event")].Kind != "scenery.event" {
					diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "publisher "+stringValue(publisher["name"])+" must reference an event", resource))
				}
			}
			diagnostics = append(diagnostics, validatePolyglotPublisherNames(resource, publishers, resources, byAddress)...)
		case "scenery.subscription":
			diagnostics = append(diagnostics, validateSubscription(resource, byAddress, routes)...)
		}
	}
	return diagnostics
}

func validatePolyglotPublisherNames(service Resource, publishers []map[string]any, resources []Resource, byAddress map[string]Resource) []Diagnostic {
	if len(publishers) == 0 {
		return nil
	}
	runtime := stringValue(service.Spec["runtime"])
	if runtime != "rust" && !IsJSRuntime(runtime) {
		return nil
	}
	localTypes := map[string]Resource{}
	for _, resource := range resources {
		if resource.Module == service.Module && (resource.Kind == "scenery.record" || resource.Kind == "scenery.enum" || resource.Kind == "scenery.union") {
			localTypes[goName(resource.Name)] = resource
		}
	}
	var diagnostics []Diagnostic
	if runtime == "rust" {
		for _, client := range namedChildren(service.Spec, "client") {
			if strings.ReplaceAll(stringValue(client["name"]), "-", "_") == "publishers" {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", `Rust service client "publishers" collides with generated publishers field`, service))
			}
		}
		servicePublishers := goName(service.Name) + "Publishers"
		if collision, exists := localTypes[servicePublishers]; exists {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "generated Rust type "+servicePublishers+" collides with "+collision.Address, service))
		}
		seenFields := map[string]bool{}
		seenTypes := map[string]bool{}
		for _, publisher := range publishers {
			name := stringValue(publisher["name"])
			field := strings.ReplaceAll(name, "-", "_")
			typeName := goName(name) + "Publisher"
			if seenFields[field] || seenTypes[typeName] {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "publisher "+name+" collides with another generated Rust publisher identifier", service))
				continue
			}
			seenFields[field], seenTypes[typeName] = true, true
			if collision, exists := localTypes[typeName]; exists {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "generated Rust publisher type "+typeName+" collides with "+collision.Address, service))
			}
		}
		return diagnostics
	}
	if collision, exists := localTypes["EventReceipt"]; exists {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "generated EventReceipt type collides with "+collision.Address, service))
	}
	seenAliases := map[string]bool{}
	for _, publisher := range publishers {
		name := stringValue(publisher["name"])
		alias := goName(name) + "EventPayload"
		if seenAliases[alias] {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "publisher "+name+" collides with another generated TypeScript payload alias "+alias, service))
			continue
		}
		seenAliases[alias] = true
		if collision, exists := localTypes[alias]; exists {
			event := byAddress[resolveResourceRef(service, refString(publisher["event"]), "event")]
			payloadAddress := resolveResourceRef(event, typeExpression(event.Spec["payload"]), "")
			if payloadAddress != collision.Address {
				diagnostics = append(diagnostics, resourceDiagnostic("SCN2313", "generated TypeScript payload alias "+alias+" collides with "+collision.Address, service))
			}
		}
	}
	return diagnostics
}

func validEventKey(resources map[string]Resource, event Resource) bool {
	raw, exists := event.Spec["idempotency"]
	if !exists {
		return true
	}
	idempotency, _ := raw.(map[string]any)
	switch idempotency["mode"] {
	case "none":
		return idempotency["key"] == nil
	case "keyed":
		keys, ok := idempotency["key"].([]any)
		if !ok || len(keys) == 0 {
			return false
		}
		for _, key := range keys {
			name, ok := strings.CutPrefix(typeExpression(key), "payload.")
			if !ok || !validSemanticName(name) || recordFieldType(resources, event.Module, event.Spec["payload"], []string{name}) == nil {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validateSubscription(subscription Resource, resources map[string]Resource, routes map[[2]string]bool) []Diagnostic {
	invoke, _ := subscription.Spec["invoke"].(map[string]any)
	event := resources[resolveResourceRef(subscription, refString(subscription.Spec["event"]), "event")]
	operation := resources[resolveResourceRef(subscription, refString(invoke["operation"]), "operation")]
	execution := resources[resolveResourceRef(subscription, refString(invoke["execution"]), "execution")]
	if event.Kind != "scenery.event" || operation.Kind != "scenery.operation" || operation.Module != subscription.Module ||
		execution.Kind != "scenery.execution" || execution.Spec["mode"] != "durable" ||
		resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation") != operation.Address {
		return []Diagnostic{resourceDiagnostic("SCN2314", "subscription requires an event, a local operation, and its durable execution", subscription)}
	}
	if !validSubscriptionPolicy(resources, subscription, invoke["authorization"], "authorization", "std.authorization.application") ||
		!validSubscriptionPolicy(resources, subscription, invoke["pipeline"], "pipeline", "std.pipeline.empty") {
		return []Diagnostic{resourceDiagnostic("SCN2314", "subscription requires a declared or standard authorization and pipeline", subscription)}
	}
	route := [2]string{event.Address, operation.Address}
	if routes[route] {
		return []Diagnostic{resourceDiagnostic("SCN2314", "duplicate subscription for this event and operation", subscription)}
	}
	routes[route] = true
	if !matchingEventWireType(resources, event, typeExpression(event.Spec["payload"]), operation, typeExpression(operation.Spec["input"]), map[[2]string]bool{}) {
		return []Diagnostic{resourceDiagnostic("SCN2315", "subscription operation input must match the event payload wire type", subscription)}
	}
	policy := subscription
	policy.Spec = invoke
	return validateBindingAuthorizationTypes(resources, policy)
}

func validSubscriptionPolicy(resources map[string]Resource, owner Resource, value any, kind, standard string) bool {
	reference := refString(value)
	return reference == standard || resources[resolveResourceRef(owner, reference, kind)].Kind == "scenery."+kind
}

func matchingEventWireType(resources map[string]Resource, left Resource, a string, right Resource, b string, seen map[[2]string]bool) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	an, aa, composite := strings.Cut(a, "(")
	bn, ba, otherComposite := strings.Cut(b, "(")
	if composite || otherComposite {
		if !composite || !otherComposite || an != bn {
			return false
		}
		as, bs := splitTypeArguments(strings.TrimSuffix(aa, ")")), splitTypeArguments(strings.TrimSuffix(ba, ")"))
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !matchingEventWireType(resources, left, as[i], right, bs[i], seen) {
				return false
			}
		}
		return true
	}
	l := resources[resolveResourceRef(left, a, "")]
	r := resources[resolveResourceRef(right, b, "")]
	if l.Address == "" || r.Address == "" {
		return a == b && l.Address == r.Address
	}
	pair := [2]string{l.Address, r.Address}
	if l.Address == r.Address || seen[pair] {
		return true
	}
	seen[pair] = true
	if l.Kind != r.Kind || (l.Spec["open"] == true) != (r.Spec["open"] == true) {
		return false
	}
	switch l.Kind {
	case "scenery.record":
		if defaultString(stringValue(l.Spec["unknown_fields"]), "reject") != defaultString(stringValue(r.Spec["unknown_fields"]), "reject") {
			return false
		}
		return matchingEventWireChildren(resources, l, r, "field", "wire_name", seen)
	case "scenery.enum":
		return matchingEventWireChildren(resources, l, r, "value", "wire_value", seen)
	case "scenery.union":
		return l.Spec["discriminator"] == r.Spec["discriminator"] &&
			matchingEventWireChildren(resources, l, r, "variant", "wire_tag", seen)
	default:
		return false
	}
}

func matchingEventWireChildren(resources map[string]Resource, left, right Resource, child, wire string, seen map[[2]string]bool) bool {
	as, bs := namedChildren(left.Spec, child), namedChildren(right.Spec, child)
	if len(as) != len(bs) {
		return false
	}
	byWire := make(map[string]map[string]any, len(bs))
	wireValue := func(item map[string]any) string {
		if value, ok := item[wire].(string); ok {
			return value
		}
		return stringValue(item["name"])
	}
	for _, item := range bs {
		byWire[wireValue(item)] = item
	}
	for _, a := range as {
		wire := wireValue(a)
		b := byWire[wire]
		if b == nil || !matchingEventWireType(resources, left, typeExpression(a["type"]), right, typeExpression(b["type"]), seen) {
			return false
		}
		delete(byWire, wire)
		if child == "field" && !matchingEventFieldConstraints(resources, left, a, right, b) {
			return false
		}
	}
	return true
}

func matchingEventFieldConstraints(resources map[string]Resource, left Resource, a map[string]any, right Resource, b map[string]any) bool {
	if (a["unique_items"] == true) != (b["unique_items"] == true) {
		return false
	}
	for _, key := range []string{"minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items"} {
		if !reflect.DeepEqual(a[key], b[key]) {
			return false
		}
	}
	_, leftHasDefault := a["default"]
	_, rightHasDefault := b["default"]
	if !leftHasDefault || !rightHasDefault {
		return leftHasDefault == rightHasDefault
	}
	leftDefault, leftErr := StaticFieldDefaultJSON(resources, left, a)
	rightDefault, rightErr := StaticFieldDefaultJSON(resources, right, b)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDefault, rightDefault)
}

// serviceEventContracts includes the external payload types the worker
// generated its publisher against, including records nested in composites.
func serviceEventContracts(manifest *Manifest, service Resource) []string {
	var refs []string
	for _, publisher := range namedChildren(service.Spec, "publisher") {
		refs = append(refs, resolveResourceRef(service, refString(publisher["event"]), "event"))
	}
	for _, resource := range manifest.Resources {
		if resource.Kind == "scenery.subscription" && resource.Module == service.Module {
			refs = append(refs, resolveResourceRef(resource, refString(resource.Spec["event"]), "event"))
		}
	}
	if len(refs) == 0 {
		return nil
	}
	resources := resourcesByAddress(manifest)
	seen := map[string]bool{}
	var visit func(Resource, any)
	visit = func(owner Resource, value any) {
		for _, reference := range typeExpressionNames(typeExpression(value)) {
			address := resolveResourceRef(owner, reference, "")
			resource := resources[address]
			if resource.Address == "" || seen[address] {
				continue
			}
			seen[address] = true
			refs = append(refs, address)
			for _, kind := range []string{"field", "variant"} {
				for _, child := range namedChildren(resource.Spec, kind) {
					visit(resource, child["type"])
				}
			}
		}
	}
	for _, address := range refs {
		event := resources[address]
		visit(event, event.Spec["payload"])
	}
	return refs
}
