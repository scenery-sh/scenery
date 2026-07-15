package generate

import "strings"

func normalizePackageABIValue(value any, ownerModule string, idx *resourceIndex) any {
	switch typed := value.(type) {
	case map[string]any:
		if reference := refString(typed); reference != "" {
			return map[string]any{"$ref": normalizePackageABITypeReference(reference, ownerModule, idx)}
		}
		if expression, ok := typed["$expression"].(string); ok {
			return map[string]any{"$expression": normalizePackageABITypeExpression(expression, ownerModule, idx)}
		}
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			result[key] = normalizePackageABIValue(nested, ownerModule, idx)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, nested := range typed {
			result[index] = normalizePackageABIValue(nested, ownerModule, idx)
		}
		return result
	default:
		return value
	}
}

func normalizePackageABITypeExpression(expression, ownerModule string, idx *resourceIndex) string {
	expression = strings.TrimSpace(expression)
	open := strings.IndexByte(expression, '(')
	if open < 0 || !strings.HasSuffix(expression, ")") {
		return normalizePackageABITypeReference(expression, ownerModule, idx)
	}
	name := strings.TrimSpace(expression[:open])
	arguments := splitTypeArguments(expression[open+1 : len(expression)-1])
	for index := range arguments {
		arguments[index] = normalizePackageABITypeExpression(arguments[index], ownerModule, idx)
	}
	return name + "(" + strings.Join(arguments, ",") + ")"
}

func normalizePackageABITypeReference(reference, ownerModule string, idx *resourceIndex) string {
	if !strings.Contains(reference, "/") {
		return reference
	}
	resource, ok := idx.byAddress[reference]
	if !ok || !isNamedContractType(resource) {
		return reference
	}
	kind := strings.TrimPrefix(resource.Kind, "scenery.")
	if resource.Module == ownerModule {
		return kind + "." + resource.Name
	}
	return packageABITypeIdentity(resource, idx)
}

func packageABITypeIdentity(resource Resource, idx *resourceIndex) string {
	kind := strings.TrimPrefix(resource.Kind, "scenery.")
	if importPath, ok := idx.contractImport(resource.Module); ok {
		return importPath + "#" + kind + "." + resource.Name
	}
	identity := ""
	for _, module := range idx.moduleDecls(resource.Module) {
		metadata, _ := module.Spec["package"].(map[string]any)
		identity = stringValue(metadata["name"])
		break
	}
	if identity == "" {
		identity = "anonymous"
	}
	return "package:" + identity + "#" + kind + "." + resource.Name
}
