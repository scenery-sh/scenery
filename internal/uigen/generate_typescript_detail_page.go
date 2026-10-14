package uigen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/clientprojection"
)

type reactDetailParam struct {
	name  string
	input string
	field clientprojection.OperationInputField
}

type reactDetailRelatedTable struct {
	spec  map[string]any
	page  reactTablePage
	param reactDetailParam
	input clientprojection.OperationInputField
}

type reactDetailPage struct {
	detail, operation, binding, record Resource
	params                             []reactDetailParam
	dialogs                            []reactTableDialog
	actionsComponent                   Resource
	related                            []reactDetailRelatedTable
}

func selectedReactDetailPages(resources, bindings []Resource, tablePages []reactTablePage) []reactDetailPage {
	byAddress := clientprojection.ResourcesByAddress(&Manifest{Resources: resources})
	selectedBindings := map[string]Resource{}
	for _, binding := range bindings {
		selectedBindings[binding.Address] = binding
	}
	tables := map[string]reactTablePage{}
	for _, page := range tablePages {
		tables[page.table.Address] = page
	}
	var pages []reactDetailPage
	for _, detail := range resources {
		if detail.Kind != "scenery.detail-page" || detail.Origin.Kind == "expanded" {
			continue
		}
		binding := selectedBindings[clientprojection.ResolveResourceRef(detail, clientprojection.RefString(detail.Spec["source"]), "binding")]
		if binding.Address == "" {
			continue
		}
		operation := byAddress[clientprojection.ResolveResourceRef(binding, clientprojection.RefString(binding.Spec["operation"]), "operation")]
		results := clientprojection.NamedChildren(operation.Spec, "result")
		if len(results) != 1 {
			continue
		}
		record := byAddress[clientprojection.ResolveResourceRef(operation, clientprojection.RefString(results[0]["type"]), "record")]
		shape := clientprojection.ResolveOperationInputShape(byAddress, operation)
		page := reactDetailPage{detail: detail, operation: operation, binding: binding, record: record}
		for _, param := range normalizedReactDetailParams(resources, detail) {
			input := clientprojection.StringValue(param["input"])
			page.params = append(page.params, reactDetailParam{name: clientprojection.StringValue(param["name"]), input: input, field: shape.Fields[input]})
		}
		for _, action := range clientprojection.OrderedChildren(detail.Spec, "action") {
			actionResource := Resource{Address: detail.Address + "/action/" + clientprojection.StringValue(action["name"]), Module: detail.Module, Name: clientprojection.StringValue(action["name"]), Spec: action}
			dialog := byAddress[clientprojection.ResolveResourceRef(detail, clientprojection.RefString(action["dialog"]), "form_dialog")]
			dialogBinding := selectedBindings[clientprojection.ResolveResourceRef(dialog, clientprojection.RefString(dialog.Spec["source"]), "binding")]
			dialogOperation := byAddress[clientprojection.ResolveResourceRef(dialogBinding, clientprojection.RefString(dialogBinding.Spec["operation"]), "operation")]
			dialogShape := clientprojection.ResolveOperationInputShape(byAddress, dialogOperation)
			if dialog.Address != "" && dialogBinding.Address != "" && dialogShape.Record != nil {
				page.dialogs = append(page.dialogs, reactTableDialog{action: actionResource, dialog: dialog, binding: dialogBinding, operation: dialogOperation, input: *dialogShape.Record, seedFromRow: true})
			}
		}
		if actions := clientprojection.OrderedChildren(detail.Spec, "actions"); len(actions) == 1 {
			page.actionsComponent = byAddress[clientprojection.ResolveResourceRef(detail, clientprojection.RefString(actions[0]["component"]), "react_component")]
		}
		params := map[string]reactDetailParam{}
		for _, param := range page.params {
			params[param.name] = param
		}
		for _, related := range clientprojection.OrderedChildren(detail.Spec, "table") {
			table := tables[clientprojection.ResolveResourceRef(detail, clientprojection.RefString(related["page"]), "table_page")]
			param := params[clientprojection.StringValue(related["param"])]
			relatedShape := clientprojection.ResolveOperationInputShape(byAddress, table.operation)
			if table.table.Address != "" && param.name != "" {
				page.related = append(page.related, reactDetailRelatedTable{spec: related, page: table, param: param, input: relatedShape.Fields[clientprojection.StringValue(related["input"])]})
			}
		}
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].detail.Address < pages[j].detail.Address })
	return pages
}

func normalizedReactDetailParams(resources []Resource, detail Resource) []map[string]any {
	for _, resource := range resources {
		if resource.Kind != "scenery.page" || resource.Origin.Kind != "expanded" {
			continue
		}
		for _, step := range resource.Origin.ExpansionLineage {
			if step.Generator == detail.Address && step.GeneratorSchemaRevision == "scenery.detail-page" {
				return clientprojection.OrderedChildren(resource.Spec, "param")
			}
		}
	}
	return clientprojection.OrderedChildren(detail.Spec, "param")
}

func renderReactDetailPage(result *Input, target Resource, reactRoot string, page reactDetailPage, bindings []Resource) (string, error) {
	resources := clientprojection.ResourcesByAddress(result.Manifest)
	entityType := clientprojection.GoName(page.record.Name)
	componentName := clientprojection.GoName(page.detail.Name)
	paramsType := componentName + "Params"
	clientType := clientprojection.GoName(target.Name) + "Client"
	presentation := clientprojection.DefaultString(clientprojection.StringValue(page.detail.Spec["presentation"]), "page")
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	tanstack := []string{"useQuery", "useQueryClient"}
	if len(page.dialogs) > 0 {
		tanstack = append(tanstack, "useMutation")
	}
	fmt.Fprintf(&b, "import { %s } from \"@tanstack/react-query\";\n", strings.Join(tanstack, ", "))
	reactImports := []string{"useCallback", "useMemo"}
	if len(page.dialogs) > 0 {
		reactImports = append(reactImports, "useState")
	}
	if page.actionsComponent.Address != "" {
		reactImports = append([]string{"type ComponentType"}, reactImports...)
	}
	fmt.Fprintf(&b, "import { %s } from \"react\";\n", strings.Join(reactImports, ", "))
	runtimeImports := detailRuntimeImports(page)
	clientImports := append([]string{clientType, "url"}, runtimeImports...)
	fmt.Fprintf(&b, "import { %s } from \"../index.js\";\n", strings.Join(clientImports, ", "))
	fmt.Fprintf(&b, "import type { %s } from \"../index.js\";\n", entityType)
	uiImports := []string{"DetailDialog", "DetailField", "DetailPageLayout", "DetailSection", "Page", "QueryState", "StatusBadge", "queryStateProps", "requestStateFromQuery"}
	if len(page.related) > 0 {
		uiImports = append(uiImports, "DetailRelated")
	}
	if len(page.dialogs) > 0 {
		uiImports = append(uiImports, "Button", "FormDialog", "FormProblem")
		controls := reactDetailDialogControlUsage(page, resources)
		for _, control := range []string{"SelectField", "TextAreaField", "TextField"} {
			if controls[control] {
				uiImports = append(uiImports, control)
			}
		}
	}
	if reactDetailUsesIcons(page.detail) {
		uiImports = append(uiImports, "Icon")
	}
	fmt.Fprintf(&b, "import { %s } from \"./scenery-ui/index.js\";\n", strings.Join(uiImports, ", "))
	if page.actionsComponent.Address != "" {
		b.WriteString("import type { DetailPageActionProps } from \"./scenery-ui/index.js\";\n")
	}
	for _, related := range page.related {
		fmt.Fprintf(&b, "import { %sPage } from %s;\n", clientprojection.GoName(related.page.table.Name), strconv.Quote("./"+related.page.table.Name+".generated.js"))
	}
	statusMaps := referencedDetailStatusMaps(resources, page)
	if len(statusMaps) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./status-maps.generated.js\";\n", strings.Join(statusMaps, ", "))
	}
	if page.actionsComponent.Address != "" {
		module, err := reactComponentImport(result, reactRoot, page.actionsComponent)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "import { %s as SceneryDetailActions } from %s;\n", clientprojection.StringValue(page.actionsComponent.Spec["export"]), strconv.Quote(module))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "export type %s = {\n", paramsType)
	for _, param := range page.params {
		fmt.Fprintf(&b, "  readonly %s: string;\n", clientprojection.TSName(param.name))
	}
	b.WriteString("};\n\n")
	if page.actionsComponent.Address != "" {
		fmt.Fprintf(&b, "const DetailActions: ComponentType<DetailPageActionProps<%s, %s>> = SceneryDetailActions;\n\n", entityType, paramsType)
	}
	fmt.Fprintf(&b, "const detailQueryKeyPrefix = [\"scenery\", \"detail_page\", %s] as const;\n", strconv.Quote(page.detail.Address))
	for _, related := range page.related {
		fmt.Fprintf(&b, "const %sQueryKeyPrefix = [\"scenery\", \"table_page\", %s] as const;\n", clientprojection.TSName(clientprojection.StringValue(related.spec["name"])), strconv.Quote(related.page.table.Address))
	}
	b.WriteString("\nfunction detailValue(value: unknown): string { return value === undefined || value === null || value === \"\" ? \"—\" : String(value); }\n")
	b.WriteString("function detailDateTime(value: unknown): string { if (value === undefined || value === null || value === \"\") return \"—\"; const parsed = new Date(String(value)); return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString(); }\n\n")

	method := reactOperationClientMethod(page.operation, page.binding, bindings)
	fmt.Fprintf(&b, "export function %sContent({ client: providedClient, params, onClose }: { readonly client?: %s; readonly params: %s; readonly onClose?: () => void }) {\n", componentName, clientType, paramsType)
	fmt.Fprintf(&b, "  const defaultClient = useMemo(() => new %s({ baseUrl: url(new URL(\"/api/\", globalThis.location.origin).toString()), authentication: { credentials: \"include\" } }), []);\n", clientType)
	b.WriteString("  const client = providedClient ?? defaultClient;\n")
	paramDeps := detailParamDependencies(page.params, "params")
	fmt.Fprintf(&b, "  const detailQueryKey = useMemo(() => [...detailQueryKeyPrefix, %s] as const, [%s]);\n", detailParamKeyValues(page.params, "params"), strings.Join(paramDeps, ", "))
	for _, related := range page.related {
		name := clientprojection.GoName(clientprojection.StringValue(related.spec["name"]))
		value := reactDetailParamValue(related.input.Type, "params."+clientprojection.TSName(related.param.name))
		fmt.Fprintf(&b, "  const %sInput = useMemo(() => ({ %s: %s }), [params.%s]);\n", clientprojection.TSName(name), clientprojection.TSName(related.input.Name), value, clientprojection.TSName(related.param.name))
		fmt.Fprintf(&b, "  const %sQueryKeySuffix = useMemo(() => [\"detail_page\", %s, params.%s] as const, [params.%s]);\n", clientprojection.TSName(name), strconv.Quote(page.detail.Address), clientprojection.TSName(related.param.name), clientprojection.TSName(related.param.name))
	}
	b.WriteString("  const queryClient = useQueryClient();\n")
	b.WriteString("  const load = useCallback(async () => {\n")
	fmt.Fprintf(&b, "    const outcome = await client.%s({\n", method)
	for _, param := range page.params {
		fmt.Fprintf(&b, "      %s: %s,\n", clientprojection.TSName(param.input), reactDetailParamValue(param.field.Type, "params."+clientprojection.TSName(param.name)))
	}
	b.WriteString("    });\n")
	b.WriteString("    if (outcome.kind === \"result\") return { kind: \"result\", data: outcome.value } as const;\n")
	b.WriteString("    return { kind: \"error\", name: outcome.name, problem: outcome.problem } as const;\n")
	fmt.Fprintf(&b, "  }, [client, %s]);\n", strings.Join(paramDeps, ", "))
	b.WriteString("  const query = useQuery({ queryKey: detailQueryKey, queryFn: load });\n")
	fmt.Fprintf(&b, "  const state = requestStateFromQuery<{ readonly data: %s }>(query);\n", entityType)
	b.WriteString("  const onMutated = useCallback(async () => {\n    await Promise.all([\n")
	b.WriteString("      queryClient.invalidateQueries({ queryKey: detailQueryKey }),\n")
	for _, related := range page.related {
		name := clientprojection.TSName(clientprojection.StringValue(related.spec["name"]))
		fmt.Fprintf(&b, "      queryClient.invalidateQueries({ queryKey: [...%sQueryKeyPrefix, ...%sQueryKeySuffix] }),\n", name, name)
	}
	b.WriteString("    ]);\n  }, [queryClient, detailQueryKey")
	for _, related := range page.related {
		fmt.Fprintf(&b, ", %sQueryKeySuffix", clientprojection.TSName(clientprojection.StringValue(related.spec["name"])))
	}
	b.WriteString("]);\n")
	for _, dialog := range page.dialogs {
		writeReactDetailDialogState(&b, page, dialog, bindings, resources)
	}
	b.WriteString("  return <QueryState {...queryStateProps(state, ")
	b.WriteString(strconv.Quote(clientprojection.DefaultString(clientprojection.StringValue(page.detail.Spec["title"]), page.detail.Name)))
	b.WriteString(")} retry={() => void query.refetch()}")
	if loading := clientprojection.StringValue(page.detail.Spec["loading_label"]); loading != "" {
		fmt.Fprintf(&b, " loadingLabel=%s", jsxStringExpression(loading))
	}
	if title := clientprojection.StringValue(page.detail.Spec["error_title"]); title != "" {
		fmt.Fprintf(&b, " errorTitle=%s", jsxStringExpression(title))
	}
	b.WriteString(">\n    {state.kind === \"result\" ? <DetailPageLayout")
	if page.actionsComponent.Address != "" || len(page.dialogs) > 0 {
		b.WriteString(" actions={<>\n")
		if page.actionsComponent.Address != "" {
			b.WriteString("      <DetailActions data={state.data} params={params} onMutated={onMutated} onClose={onClose} />\n")
		}
		primary := primaryDialogIndex(page.dialogs)
		for index, dialog := range page.dialogs {
			fmt.Fprintf(&b, "      <Button label=%s", strconv.Quote(clientprojection.StringValue(dialog.action.Spec["label"])))
			if icon := clientprojection.StringValue(dialog.action.Spec["icon"]); icon != "" {
				fmt.Fprintf(&b, " icon={<Icon icon=%s size=\"sm\" />}", strconv.Quote(icon))
			}
			fmt.Fprintf(&b, " onClick={() => open%s(state.data)} size=\"sm\" variant=%s />\n", clientprojection.GoName(dialog.dialog.Name), strconv.Quote(map[bool]string{true: "primary", false: "secondary"}[index == primary]))
		}
		b.WriteString("    </>}")
	}
	b.WriteString(">\n")
	for _, section := range clientprojection.OrderedChildren(page.detail.Spec, "section") {
		fmt.Fprintf(&b, "      <DetailSection title=%s", jsxStringExpression(clientprojection.StringValue(section["label"])))
		if description := clientprojection.StringValue(section["description"]); description != "" {
			fmt.Fprintf(&b, " description=%s", jsxStringExpression(description))
		}
		b.WriteString(">\n")
		for _, field := range clientprojection.OrderedChildren(section, "field") {
			name := clientprojection.StringValue(field["name"])
			label := clientprojection.DefaultString(clientprojection.StringValue(field["label"]), humanLabel(name))
			valueExpression := "state.data." + clientprojection.TSName(name)
			fieldJSX := fmt.Sprintf("<DetailField label=%s>%s</DetailField>", jsxStringExpression(label), renderReactDetailField(resources, page, field, valueExpression))
			if field["hide_empty"] == true {
				fmt.Fprintf(&b, "        {%s !== undefined && %s !== null && String(%s) !== \"\" ? %s : null}\n", valueExpression, valueExpression, valueExpression, fieldJSX)
			} else {
				fmt.Fprintf(&b, "        %s\n", fieldJSX)
			}
		}
		b.WriteString("      </DetailSection>\n")
	}
	for _, related := range page.related {
		name := clientprojection.GoName(clientprojection.StringValue(related.spec["name"]))
		fmt.Fprintf(&b, "      <DetailRelated title=%s><%sPage client={client} injectedInput={%sInput} queryKeySuffix={%sQueryKeySuffix} /></DetailRelated>\n", jsxStringExpression(clientprojection.StringValue(related.spec["label"])), clientprojection.GoName(related.page.table.Name), clientprojection.TSName(name), clientprojection.TSName(name))
	}
	for _, dialog := range page.dialogs {
		writeReactDetailDialogJSX(&b, dialog, resources)
	}
	b.WriteString("    </DetailPageLayout> : null}\n  </QueryState>;\n}\n\n")

	if presentation == "page" || presentation == "both" {
		fmt.Fprintf(&b, "export function %sPage({ client, params }: { readonly client?: %s; readonly params: %s }) {\n", clientprojection.GoName(page.detail.Name), clientType, paramsType)
		fmt.Fprintf(&b, "  return <Page title=%s", jsxStringExpression(clientprojection.StringValue(page.detail.Spec["title"])))
		if description := clientprojection.StringValue(page.detail.Spec["description"]); description != "" {
			fmt.Fprintf(&b, " ariaLabel=%s", jsxStringExpression(description))
		}
		fmt.Fprintf(&b, "><%sContent client={client} params={params} /></Page>;\n}\n\n", componentName)
	}
	if presentation == "dialog" || presentation == "both" {
		fmt.Fprintf(&b, "export function %sDialog({ open, onClose, client", componentName)
		for _, param := range page.params {
			fmt.Fprintf(&b, ", %s", clientprojection.TSName(param.name))
		}
		fmt.Fprintf(&b, " }: { readonly open: boolean; readonly onClose: () => void; readonly client?: %s", clientType)
		for _, param := range page.params {
			fmt.Fprintf(&b, "; readonly %s: string", clientprojection.TSName(param.name))
		}
		b.WriteString(" }) {\n")
		fmt.Fprintf(&b, "  return <DetailDialog open={open} onOpenChange={(next) => { if (!next) onClose(); }} title=%s", jsxStringExpression(clientprojection.StringValue(page.detail.Spec["title"])))
		if description := clientprojection.StringValue(page.detail.Spec["description"]); description != "" {
			fmt.Fprintf(&b, " description=%s", jsxStringExpression(description))
		}
		b.WriteString(">\n")
		fmt.Fprintf(&b, "    <%sContent client={client} onClose={onClose} params={{ ", componentName)
		for index, param := range page.params {
			if index > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s", clientprojection.TSName(param.name))
		}
		b.WriteString(" }} />\n  </DetailDialog>;\n}\n")
	}
	return b.String(), nil
}

func detailRuntimeImports(page reactDetailPage) []string {
	set := map[string]bool{}
	add := func(value any) {
		switch unwrapReactType(clientprojection.TypeExpression(value)) {
		case "date":
			set["date"] = true
		case "datetime":
			set["dateTime"] = true
		case "decimal":
			set["decimal"] = true
		case "duration":
			set["duration"] = true
		case "relative_path":
			set["relativePath"] = true
		case "url":
			set["url"] = true
		case "uuid":
			set["uuid"] = true
		}
	}
	for _, param := range page.params {
		add(param.field.Type)
	}
	for _, related := range page.related {
		add(related.input.Type)
	}
	delete(set, "url") // url is always imported once as the client base-url constructor.
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func reactDetailParamValue(value any, expression string) string {
	switch unwrapReactType(clientprojection.TypeExpression(value)) {
	case "bool":
		return expression + " === \"true\""
	case "int", "int64", "uint64", "size":
		return "BigInt(" + expression + ")"
	case "int32", "uint32", "float32", "float64":
		return "Number(" + expression + ")"
	case "date":
		return "date(" + expression + ")"
	case "datetime":
		return "dateTime(" + expression + ")"
	case "decimal":
		return "decimal(" + expression + ")"
	case "duration":
		return "duration(" + expression + ")"
	case "relative_path":
		return "relativePath(" + expression + ")"
	case "url":
		return "url(" + expression + ")"
	case "uuid":
		return "uuid(" + expression + ")"
	default:
		if typeName := clientprojection.TSType(value); typeName != "string" {
			return expression + " as " + typeName
		}
		return expression
	}
}

func detailParamDependencies(params []reactDetailParam, owner string) []string {
	result := make([]string, 0, len(params))
	for _, param := range params {
		result = append(result, owner+"."+clientprojection.TSName(param.name))
	}
	return result
}

func detailParamKeyValues(params []reactDetailParam, owner string) string {
	values := make([]string, 0, len(params))
	for _, param := range params {
		values = append(values, owner+"."+clientprojection.TSName(param.name))
	}
	return strings.Join(values, ", ")
}

func reactDetailUsesIcons(detail Resource) bool {
	for _, action := range clientprojection.OrderedChildren(detail.Spec, "action") {
		if clientprojection.StringValue(action["icon"]) != "" {
			return true
		}
	}
	return false
}

func referencedDetailStatusMaps(resources map[string]Resource, page reactDetailPage) []string {
	set := map[string]bool{}
	for _, section := range clientprojection.OrderedChildren(page.detail.Spec, "section") {
		for _, field := range clientprojection.OrderedChildren(section, "field") {
			if statusMap := resolveReferencedStatusMap(resources, page.detail, field["status_map"]); statusMap.Address != "" {
				set[reactStatusMapName(statusMap)] = true
			}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func renderReactDetailField(resources map[string]Resource, page reactDetailPage, field map[string]any, expression string) string {
	appearance := clientprojection.DefaultString(clientprojection.StringValue(field["appearance"]), "auto")
	if appearance == "badge" {
		statusMap := resolveReferencedStatusMap(resources, page.detail, field["status_map"])
		return fmt.Sprintf("<StatusBadge status={String(%s)} map={%s} />", expression, reactStatusMapName(statusMap))
	}
	recordField := namedResourceChild(page.record.Spec, "field", clientprojection.StringValue(field["name"]))
	fieldType := unwrapReactType(clientprojection.TypeExpression(recordField["type"]))
	if appearance == "datetime" || appearance == "auto" && (fieldType == "date" || fieldType == "datetime") {
		return "{detailDateTime(" + expression + ")}"
	}
	return "{detailValue(" + expression + ")}"
}

func reactDetailDialogControlUsage(page reactDetailPage, resources map[string]Resource) map[string]bool {
	used := map[string]bool{}
	for _, dialog := range page.dialogs {
		for _, field := range reactDialogFields(dialog) {
			control := clientprojection.DefaultString(clientprojection.StringValue(field["control"]), "auto")
			statusMap := resolveReferencedStatusMap(resources, dialog.dialog, field["status_map"])
			enumValues := clientprojection.EnumWireValues(resources, dialog.input.Module, field["type"])
			switch {
			case control == "select" || control == "auto" && (len(enumValues) > 0 || statusMap.Address != ""):
				used["SelectField"] = true
			case control == "textarea":
				used["TextAreaField"] = true
			default:
				used["TextField"] = true
			}
		}
	}
	return used
}

func writeReactDetailDialogState(b *strings.Builder, page reactDetailPage, dialog reactTableDialog, bindings []Resource, resources map[string]Resource) {
	name := clientprojection.GoName(dialog.dialog.Name)
	fields := reactDialogFields(dialog)
	fmt.Fprintf(b, "  const [is%sOpen, set%sOpen] = useState(false);\n", name, name)
	fmt.Fprintf(b, "  const [%sProblem, set%sProblem] = useState<{ readonly code: string; readonly message: string; readonly path?: string }>();\n", clientprojection.TSName(dialog.dialog.Name), name)
	fmt.Fprintf(b, "  const [%sValues, set%sValues] = useState({\n", clientprojection.TSName(dialog.dialog.Name), name)
	for _, field := range fields {
		fmt.Fprintf(b, "    %s: %s,\n", clientprojection.TSName(clientprojection.StringValue(field["name"])), strconv.Quote(reactDialogInitialValue(dialog, field, resources)))
	}
	b.WriteString("  });\n")
	fmt.Fprintf(b, "  const open%s = (data: %s) => {\n", name, clientprojection.GoName(page.record.Name))
	fmt.Fprintf(b, "    set%sValues({\n", name)
	for _, field := range fields {
		fieldName := clientprojection.StringValue(field["name"])
		fmt.Fprintf(b, "      %s: detailValue(data.%s) === \"—\" ? %s : detailValue(data.%s),\n", clientprojection.TSName(fieldName), clientprojection.TSName(fieldName), strconv.Quote(reactDialogInitialValue(dialog, field, resources)), clientprojection.TSName(fieldName))
	}
	b.WriteString("    });\n")
	fmt.Fprintf(b, "    set%sProblem(undefined);\n", name)
	fmt.Fprintf(b, "    set%sOpen(true);\n", name)
	b.WriteString("  };\n")
	method := reactOperationClientMethod(dialog.operation, dialog.binding, bindings)
	fmt.Fprintf(b, "  const %sMutation = useMutation({\n", clientprojection.TSName(dialog.dialog.Name))
	b.WriteString("    mutationFn: async () => {\n")
	fmt.Fprintf(b, "      const outcome = await client.%s({\n", method)
	for _, field := range fields {
		fieldName := clientprojection.StringValue(field["name"])
		fmt.Fprintf(b, "        %s: %s,\n", clientprojection.TSName(fieldName), reactDialogSubmitValue(dialog, field, clientprojection.TSName(dialog.dialog.Name)+"Values."+clientprojection.TSName(fieldName), resources))
	}
	b.WriteString("      });\n")
	b.WriteString("      if (outcome.kind === \"result\") return outcome.value;\n")
	fmt.Fprintf(b, "      set%sProblem(outcome.problem);\n", name)
	b.WriteString("      throw outcome.problem;\n")
	b.WriteString("    },\n")
	b.WriteString("    onError: (error) => {\n")
	b.WriteString("      const failure = error as { readonly code?: unknown; readonly message?: unknown; readonly path?: unknown };\n")
	fmt.Fprintf(b, "      set%sProblem({ code: typeof failure.code === \"string\" ? failure.code : \"request_failed\", message: typeof failure.message === \"string\" ? failure.message : \"Request failed.\", path: typeof failure.path === \"string\" ? failure.path : undefined });\n", name)
	b.WriteString("    },\n")
	b.WriteString("    onSuccess: async () => {\n      await onMutated();\n")
	fmt.Fprintf(b, "      set%sProblem(undefined);\n      set%sOpen(false);\n", name, name)
	b.WriteString("    },\n  });\n")
}

func writeReactDetailDialogJSX(b *strings.Builder, dialog reactTableDialog, resources map[string]Resource) {
	name := clientprojection.GoName(dialog.dialog.Name)
	valueName := clientprojection.TSName(dialog.dialog.Name) + "Values"
	mutationName := clientprojection.TSName(dialog.dialog.Name) + "Mutation"
	fmt.Fprintf(b, "      {is%sOpen ? <FormDialog title=%s", name, jsxStringExpression(clientprojection.StringValue(dialog.dialog.Spec["title"])))
	if description := clientprojection.StringValue(dialog.dialog.Spec["description"]); description != "" {
		fmt.Fprintf(b, " subtitle=%s", jsxStringExpression(description))
	}
	fmt.Fprintf(b, " onOpenChange={set%sOpen} onSubmit={() => %s.mutate()} footer={<>\n", name, mutationName)
	fmt.Fprintf(b, "        <Button label=\"Cancel\" onClick={() => set%sOpen(false)} variant=\"secondary\" />\n", name)
	fmt.Fprintf(b, "        <Button isLoading={%s.isPending} label=%s type=\"submit\" variant=\"primary\" />\n", mutationName, strconv.Quote(clientprojection.DefaultString(clientprojection.StringValue(dialog.dialog.Spec["submit_label"]), clientprojection.StringValue(dialog.action.Spec["label"]))))
	b.WriteString("      </>}>\n")
	for _, field := range reactDialogFields(dialog) {
		fieldName := clientprojection.StringValue(field["name"])
		label := clientprojection.DefaultString(clientprojection.StringValue(field["label"]), humanLabel(fieldName))
		control := clientprojection.DefaultString(clientprojection.StringValue(field["control"]), "auto")
		statusMap := resolveReferencedStatusMap(resources, dialog.dialog, field["status_map"])
		enumValues := clientprojection.EnumWireValues(resources, dialog.input.Module, field["type"])
		if control == "select" || control == "auto" && (len(enumValues) > 0 || statusMap.Address != "") {
			fmt.Fprintf(b, "        <SelectField label=%s onChange={(value) => set%sValues((values) => ({ ...values, %s: value }))} options={[%s]} value={%s.%s} />\n", strconv.Quote(label), name, clientprojection.TSName(fieldName), reactFilterOptions(enumValues, statusMap), valueName, clientprojection.TSName(fieldName))
		} else if control == "textarea" {
			fmt.Fprintf(b, "        <TextAreaField label=%s onChange={(value) => set%sValues((values) => ({ ...values, %s: value }))} placeholder=%s value={%s.%s} />\n", strconv.Quote(label), name, clientprojection.TSName(fieldName), strconv.Quote(clientprojection.StringValue(field["placeholder"])), valueName, clientprojection.TSName(fieldName))
		} else {
			inputType := "text"
			if unwrapReactType(clientprojection.TypeExpression(field["type"])) == "date" {
				inputType = "date"
			}
			fmt.Fprintf(b, "        <TextField label=%s onChange={(value) => set%sValues((values) => ({ ...values, %s: value }))} placeholder=%s type=%s value={%s.%s} />\n", strconv.Quote(label), name, clientprojection.TSName(fieldName), strconv.Quote(clientprojection.StringValue(field["placeholder"])), strconv.Quote(inputType), valueName, clientprojection.TSName(fieldName))
		}
	}
	fmt.Fprintf(b, "        <FormProblem problem={%sProblem} />\n      </FormDialog> : null}\n", clientprojection.TSName(dialog.dialog.Name))
}
