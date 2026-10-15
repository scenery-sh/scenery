package generate

import "scenery.sh/internal/clientprojection"

func wireName(field map[string]any, fallback string) string {
	return clientprojection.WireName(field, fallback)
}

func goName(value string) string { return clientprojection.GoName(value) }

func namedChildren(spec map[string]any, name string) []map[string]any {
	return clientprojection.NamedChildren(spec, name)
}

func orderedChildren(spec map[string]any, name string) []map[string]any {
	return clientprojection.OrderedChildren(spec, name)
}

func tsType(value any) string { return clientprojection.TSType(value) }

func tsName(value string) string { return clientprojection.TSName(value) }

func lastRef(value string) string { return clientprojection.LastRef(value) }

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	return clientprojection.ResourcesByAddress(manifest)
}

func resourceAddress(module, blockType, name string) string {
	return clientprojection.ResourceAddress(module, blockType, name)
}

func defaultString(value, fallback string) string {
	return clientprojection.DefaultString(value, fallback)
}

func stringValues(value any) []string { return clientprojection.StringValues(value) }

func cloneMapValue(value any) map[string]any { return clientprojection.CloneMapValue(value) }

func integerValue(value any) (int, bool) { return clientprojection.IntegerValue(value) }

type operationInputField = clientprojection.OperationInputField

type operationInputShape = clientprojection.OperationInputShape

func resolveOperationInputShape(resources map[string]Resource, operation Resource) operationInputShape {
	return clientprojection.ResolveOperationInputShape(resources, operation)
}

func moduleInstancePath(resource Resource) string {
	return clientprojection.ModuleInstancePath(resource)
}

func resolveResourceRef(resource Resource, reference, kind string) string {
	return clientprojection.ResolveResourceRef(resource, reference, kind)
}

func refString(value any) string { return clientprojection.RefString(value) }

func stringValue(value any) string { return clientprojection.StringValue(value) }

func typeExpression(value any) string { return clientprojection.TypeExpression(value) }

func isOptionalType(value any) bool { return clientprojection.IsOptionalType(value) }
