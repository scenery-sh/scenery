package generate

import (
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"scenery.sh/internal/spec"
)

type reactTablePage struct {
	table, crud, record, resultRecord, operation, binding Resource
	stats                                                 *reactTableStats
	dialogs                                               []reactTableDialog
	itemsField                                            string
	metadataFields                                        []string
	pagination                                            string
}

type reactTableStats struct {
	spec, binding, operation, record Resource
}

type reactTableDialog struct {
	action, dialog, binding, operation, input Resource
	seedFromRow                               bool
}

type reactSplitPage struct {
	split, operation, binding Resource
}

type reactContentPage struct {
	content, operation, binding Resource
}

// Slot names mirror SplitPageSlots in ui/components/SplitPage.tsx and the
// split_page source schema; order fixes the generated import alias numbering.
var splitPageSlotNames = []string{"sidebar", "detail", "sidebar_actions", "detail_header"}

// Slot names mirror ContentPageSlots in ui/components/PageLayout.tsx and the
// content_page source schema; order fixes the generated import alias numbering.
var contentPageSlotNames = []string{"content", "actions"}

func renderTypeScriptReactWithCatalog(result *Result, target Resource, root string, bindings []Resource, assistants []Resource, catalog []generatedFile) ([]generatedFile, []string, error) {
	if _, ok := target.Spec["react"].(map[string]any); !ok {
		return nil, []string{}, nil
	}
	reactRoot := filepath.Join(root, "react")
	files := cloneProjection(catalog)
	if source := renderReactStatusMaps(result.Manifest.Resources); source != "" {
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, "status-maps.generated.ts"), Bytes: []byte(source)})
	}
	pages := selectedReactTablePages(result.Manifest.Resources, bindings)
	for _, page := range pages {
		source, renderErr := renderReactTablePage(result, target, reactRoot, page, bindings)
		if renderErr != nil {
			return nil, nil, renderErr
		}
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, page.table.Name+".generated.tsx"), Bytes: []byte(source)})
	}
	splitPages := selectedReactSplitPages(result.Manifest.Resources, bindings)
	for _, page := range splitPages {
		source, renderErr := renderReactSplitPage(result, target, reactRoot, page, bindings)
		if renderErr != nil {
			return nil, nil, renderErr
		}
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, page.split.Name+".generated.tsx"), Bytes: []byte(source)})
	}
	contentPages := selectedReactContentPages(result.Manifest.Resources, bindings)
	for _, page := range contentPages {
		source, renderErr := renderReactContentPage(result, target, reactRoot, page, bindings)
		if renderErr != nil {
			return nil, nil, renderErr
		}
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, page.content.Name+".generated.tsx"), Bytes: []byte(source)})
	}
	workspacePages := selectedReactWorkspacePages(result.Manifest.Resources, bindings)
	for _, page := range workspacePages {
		source, renderErr := renderReactWorkspacePage(result, target, reactRoot, page, bindings)
		if renderErr != nil {
			return nil, nil, renderErr
		}
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, page.workspace.Name+".generated.tsx"), Bytes: []byte(source)})
	}
	detailPages := selectedReactDetailPages(result.Manifest.Resources, bindings, pages)
	for _, page := range detailPages {
		source, renderErr := renderReactDetailPage(result, target, reactRoot, page, bindings)
		if renderErr != nil {
			return nil, nil, renderErr
		}
		files = append(files, generatedFile{Path: filepath.Join(reactRoot, page.detail.Name+".generated.tsx"), Bytes: []byte(source)})
	}
	routePages := reactRoutePages(pages, splitPages, contentPages, workspacePages...)
	routePages = appendReactDetailRoutePages(routePages, detailPages)
	files = append(files,
		generatedFile{Path: filepath.Join(reactRoot, "routes.generated.ts"), Bytes: []byte(renderReactRoutes(result, routePages))},
		generatedFile{Path: filepath.Join(reactRoot, "access.generated.tsx"), Bytes: []byte(renderReactAccessAdapter())},
		generatedFile{Path: filepath.Join(reactRoot, "app.generated.tsx"), Bytes: []byte(renderReactAppAdapter())},
		generatedFile{Path: filepath.Join(reactRoot, "index.ts"), Bytes: []byte(renderReactIndex(result.Manifest.Resources, detailPages))},
	)
	if len(assistants) > 0 {
		files = append(files,
			generatedFile{Path: filepath.Join(reactRoot, "assistants.generated.tsx"), Bytes: []byte(renderReactAssistantAdapter(assistants))},
		)
		for index := range files {
			if files[index].Path == filepath.Join(reactRoot, "index.ts") {
				files[index].Bytes = append(files[index].Bytes, []byte("export * from \"./assistants.generated.js\";\n")...)
				break
			}
		}
	}
	return files, []string{"react/scenery-ui"}, nil
}

func selectedReactTablePages(resources, bindings []Resource) []reactTablePage {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	selectedBindings := map[string]Resource{}
	for _, binding := range bindings {
		selectedBindings[binding.Address] = binding
	}
	var pages []reactTablePage
	for _, table := range resources {
		if table.Kind != "scenery.table-page" || table.Origin.Kind == "expanded" {
			continue
		}
		source := byAddress[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
		var page reactTablePage
		switch source.Kind {
		case "scenery.crud":
			binding := selectedBindings[resourceAddress(source.Module, "binding", source.Name+"_list_http")]
			if binding.Address == "" {
				continue
			}
			operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
			entity := byAddress[resolveResourceRef(source, refString(source.Spec["entity"]), "entity")]
			record := byAddress[resolveResourceRef(entity, refString(entity.Spec["type"]), "record")]
			page = reactTablePage{table: table, crud: source, record: record, operation: operation, binding: binding, itemsField: "items", pagination: "cursor"}
		case "scenery.binding":
			binding := selectedBindings[source.Address]
			if binding.Address == "" {
				continue
			}
			operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
			results := namedChildren(operation.Spec, "result")
			if len(results) != 1 {
				continue
			}
			resultRecord := byAddress[resolveResourceRef(operation, refString(results[0]["type"]), "record")]
			itemsField := stringValue(table.Spec["items"])
			items := namedResourceChild(resultRecord.Spec, "field", itemsField)
			itemType, ok := unwrapReactCollectionType(typeExpression(items["type"]), "list")
			if !ok {
				continue
			}
			record := byAddress[resolveResourceRef(operation, itemType, "record")]
			if record.Kind != "scenery.record" {
				continue
			}
			pagination := ""
			if len(orderedChildren(table.Spec, "pagination")) > 0 {
				pagination = "page"
			}
			page = reactTablePage{
				table: table, record: record, resultRecord: resultRecord, operation: operation, binding: binding,
				itemsField: itemsField, metadataFields: stringValues(table.Spec["metadata"]), pagination: pagination,
			}
		default:
			continue
		}
		if children := orderedChildren(table.Spec, "stats"); len(children) == 1 {
			spec := Resource{Address: table.Address + "/stats", Module: table.Module, Name: table.Name + "_stats", Spec: children[0]}
			statsBinding := selectedBindings[resolveResourceRef(table, refString(children[0]["source"]), "binding")]
			statsOperation := byAddress[resolveResourceRef(statsBinding, refString(statsBinding.Spec["operation"]), "operation")]
			results := namedChildren(statsOperation.Spec, "result")
			if statsBinding.Address != "" && len(results) == 1 {
				statsRecord := byAddress[resolveResourceRef(statsOperation, refString(results[0]["type"]), "record")]
				page.stats = &reactTableStats{spec: spec, binding: statsBinding, operation: statsOperation, record: statsRecord}
			}
		}
		addDialog := func(action Resource, dialogValue any, seedFromRow bool) {
			dialog := byAddress[resolveResourceRef(table, refString(dialogValue), "form_dialog")]
			for index := range page.dialogs {
				if page.dialogs[index].dialog.Address == dialog.Address {
					page.dialogs[index].seedFromRow = page.dialogs[index].seedFromRow || seedFromRow
					return
				}
			}
			dialogBinding := selectedBindings[resolveResourceRef(dialog, refString(dialog.Spec["source"]), "binding")]
			dialogOperation := byAddress[resolveResourceRef(dialogBinding, refString(dialogBinding.Spec["operation"]), "operation")]
			shape := resolveOperationInputShape(byAddress, dialogOperation)
			if dialog.Address != "" && dialogBinding.Address != "" && shape.Record != nil {
				page.dialogs = append(page.dialogs, reactTableDialog{
					action: action, dialog: dialog, binding: dialogBinding, operation: dialogOperation, input: *shape.Record, seedFromRow: seedFromRow,
				})
			}
		}
		for _, action := range orderedChildren(table.Spec, "action") {
			addDialog(
				Resource{Address: table.Address + "/action/" + stringValue(action["name"]), Module: table.Module, Name: stringValue(action["name"]), Spec: action},
				action["dialog"],
				false,
			)
		}
		if details := orderedChildren(table.Spec, "row_detail"); len(details) == 1 && details[0]["dialog"] != nil {
			addDialog(Resource{}, details[0]["dialog"], true)
		}
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].table.Address < pages[j].table.Address })
	return pages
}

func renderReactStatusMaps(resources []Resource) string {
	var maps []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.status-map" {
			maps = append(maps, resource)
		}
	}
	if len(maps) == 0 {
		return ""
	}
	sort.Slice(maps, func(i, j int) bool { return maps[i].Address < maps[j].Address })
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	b.WriteString("import type { BadgeVariant, StatusMap } from \"./scenery-ui/index.js\";\n\n")
	b.WriteString("export const SceneryStatusBadgeVariants = {\n")
	for _, variant := range spec.StatusBadgeVariants() {
		fmt.Fprintf(&b, "  %s: true,\n", strconv.Quote(variant))
	}
	b.WriteString("} as const satisfies Partial<Record<BadgeVariant, true>>;\n\n")
	for _, statusMap := range maps {
		fmt.Fprintf(&b, "export const %s: StatusMap = {\n", reactStatusMapName(statusMap))
		for _, status := range orderedChildren(statusMap.Spec, "status") {
			fmt.Fprintf(&b, "  %s: { label: %s, variant: %s },\n", strconv.Quote(stringValue(status["name"])), strconv.Quote(stringValue(status["label"])), strconv.Quote(stringValue(status["variant"])))
		}
		b.WriteString("};\n\n")
	}
	return b.String()
}

func renderReactIndex(resources []Resource, detailPages ...[]reactDetailPage) string {
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	b.WriteString("export * from \"./routes.generated.js\";\n")
	b.WriteString("export * from \"./access.generated.js\";\n")
	b.WriteString("export * from \"./app.generated.js\";\n")
	for _, resource := range resources {
		if resource.Kind == "scenery.status-map" {
			b.WriteString("export * from \"./status-maps.generated.js\";\n")
			break
		}
	}
	if len(detailPages) == 1 {
		for _, page := range detailPages[0] {
			fmt.Fprintf(&b, "export * from %s;\n", strconv.Quote("./"+page.detail.Name+".generated.js"))
		}
	}
	return b.String()
}

func reactStatusMapName(resource Resource) string {
	return goName(resource.Module + "_" + resource.Name + "_status_map")
}

func selectedReactSplitPages(resources, bindings []Resource) []reactSplitPage {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	selectedBindings := map[string]Resource{}
	for _, binding := range bindings {
		selectedBindings[binding.Address] = binding
	}
	var pages []reactSplitPage
	for _, split := range resources {
		if split.Kind != "scenery.split-page" || split.Origin.Kind == "expanded" {
			continue
		}
		binding := selectedBindings[resolveResourceRef(split, refString(split.Spec["source"]), "binding")]
		if binding.Address == "" {
			continue
		}
		operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		pages = append(pages, reactSplitPage{split: split, operation: operation, binding: binding})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].split.Address < pages[j].split.Address })
	return pages
}

func selectedReactContentPages(resources, bindings []Resource) []reactContentPage {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	selectedBindings := map[string]Resource{}
	for _, binding := range bindings {
		selectedBindings[binding.Address] = binding
	}
	var pages []reactContentPage
	for _, content := range resources {
		if content.Kind != "scenery.content-page" || content.Origin.Kind == "expanded" {
			continue
		}
		if content.Spec["source"] == nil {
			pages = append(pages, reactContentPage{content: content})
			continue
		}
		sourceRef := refString(content.Spec["source"])
		binding := selectedBindings[resolveResourceRef(content, sourceRef, "binding")]
		if binding.Address == "" {
			continue
		}
		operation := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]
		pages = append(pages, reactContentPage{content: content, operation: operation, binding: binding})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].content.Address < pages[j].content.Address })
	return pages
}

func writeReactPageOpen(b *strings.Builder, pageName, clientName string) {
	fmt.Fprintf(b, "export function %sPage({ client: providedClient }: { readonly client?: %sClient } = {}) {\n", pageName, clientName)
	fmt.Fprintf(b, "  const defaultClient = useMemo(() => new %sClient({ baseUrl: url(new URL(\"/api/\", globalThis.location.origin).toString()), authentication: { credentials: \"include\" } }), []);\n", clientName)
	b.WriteString("  const client = providedClient ?? defaultClient;\n")
}

func writeReactLoad(b *strings.Builder, params, stateType string, writeCall func(*strings.Builder), resultExpr string) {
	writeReactLoadWithDependencies(b, params, stateType, writeCall, resultExpr, "client")
}

func writeReactLoadWithDependencies(b *strings.Builder, params, stateType string, writeCall func(*strings.Builder), resultExpr string, dependencies ...string) {
	fmt.Fprintf(b, "  const load = useCallback(async (%s): Promise<%s> => {\n", params, stateType)
	writeCall(b)
	fmt.Fprintf(b, "    if (outcome.kind === \"result\") return %s;\n", resultExpr)
	b.WriteString("    return { kind: \"error\", name: outcome.name, problem: outcome.problem };\n")
	fmt.Fprintf(b, "  }, [%s]);\n", strings.Join(dependencies, ", "))
}

func renderReactContentPage(result *Result, target Resource, reactRoot string, page reactContentPage, bindings []Resource) (string, error) {
	static := page.binding.Address == ""
	resultType := ""
	if !static {
		resultType = tsType(namedChildren(page.operation.Spec, "result")[0]["type"])
	}
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	if static {
		b.WriteString("import { Page } from \"./scenery-ui/index.js\";\n")
	} else {
		b.WriteString("import { useQuery } from \"@tanstack/react-query\";\n")
		b.WriteString("import { useCallback, useMemo } from \"react\";\n")
		fmt.Fprintf(&b, "import { %sClient, url } from \"../index.js\";\n", goName(target.Name))
		fmt.Fprintf(&b, "import type { %s } from \"../index.js\";\n", resultType)
		b.WriteString("import { Page, defineContentPageSlots, requestStateFromQuery } from \"./scenery-ui/index.js\";\n")
		b.WriteString("import type { ContentPageSlotProps, ContentPageState } from \"./scenery-ui/index.js\";\n")
	}
	for index, slot := range contentPageSlotNames {
		children := orderedChildren(page.content.Spec, slot)
		if len(children) == 0 {
			continue
		}
		componentAddress := resolveResourceRef(page.content, refString(children[0]["component"]), "react_component")
		component := resourcesByAddress(result.Manifest)[componentAddress]
		module, err := reactComponentImport(result, reactRoot, component)
		if err != nil {
			return "", err
		}
		alias := fmt.Sprintf("SceneryContentSlot%d", index+1)
		aliases[slot] = alias
		fmt.Fprintf(&b, "import { %s as %s } from %s;\n", stringValue(component.Spec["export"]), alias, strconv.Quote(module))
	}
	b.WriteString("\n")
	if !static {
		fmt.Fprintf(&b, "const slots = defineContentPageSlots<%s>()({\n", resultType)
		for _, slot := range contentPageSlotNames {
			if alias := aliases[slot]; alias != "" {
				fmt.Fprintf(&b, "  %s: %s,\n", slot, alias)
			}
		}
		b.WriteString("});\n\n")
		fmt.Fprintf(&b, "const queryKey = [\"scenery\", \"content_page\", %s] as const;\n\n", strconv.Quote(page.content.Address))
		method := reactOperationClientMethod(page.operation, page.binding, bindings)
		writeReactPageOpen(&b, goName(page.content.Name), goName(target.Name))
		writeReactLoad(&b, "", "ContentPageState<"+resultType+">", func(b *strings.Builder) {
			fmt.Fprintf(b, "    const outcome = await client.%s({});\n", method)
		}, `{ kind: "result", data: outcome.value }`)
		b.WriteString("  const query = useQuery({ queryKey, queryFn: load });\n")
		b.WriteString("  const state: ContentPageState<" + resultType + "> = requestStateFromQuery<{ readonly data: " + resultType + " }>(query);\n")
		b.WriteString("  const slotProps: ContentPageSlotProps<" + resultType + "> = { state };\n")
	} else {
		fmt.Fprintf(&b, "export function %sPage() {\n", goName(page.content.Name))
	}
	fmt.Fprintf(&b, "  return <Page title=%s", jsxStringExpression(stringValue(page.content.Spec["title"])))
	if label := stringValue(page.content.Spec["aria_label"]); label != "" {
		fmt.Fprintf(&b, " ariaLabel=%s", jsxStringExpression(label))
	}
	if maxWidth, ok := integerValue(page.content.Spec["max_width"]); ok {
		fmt.Fprintf(&b, " maxWidth={%d}", maxWidth)
	}
	if aliases["actions"] != "" {
		if static {
			fmt.Fprintf(&b, " actions={<%s />}", aliases["actions"])
		} else {
			b.WriteString(" actions={<slots.actions {...slotProps} />}")
		}
	}
	if static {
		fmt.Fprintf(&b, "><%s /></Page>;\n}\n", aliases["content"])
	} else {
		b.WriteString("><slots.content {...slotProps} /></Page>;\n}\n")
	}
	return b.String(), nil
}

func renderReactSplitPage(result *Result, target Resource, reactRoot string, page reactSplitPage, bindings []Resource) (string, error) {
	resultType := tsType(namedChildren(page.operation.Spec, "result")[0]["type"])
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	b.WriteString("import { useQuery } from \"@tanstack/react-query\";\n")
	b.WriteString("import { useCallback, useEffect, useMemo, useState } from \"react\";\n")
	fmt.Fprintf(&b, "import { %sClient, url } from \"../index.js\";\n", goName(target.Name))
	fmt.Fprintf(&b, "import type { %s } from \"../index.js\";\n", resultType)
	b.WriteString("import { SplitPage, defineSplitPageSlots, requestStateFromQuery } from \"./scenery-ui/index.js\";\n")
	b.WriteString("import type { SplitPageSlotProps, SplitPageState } from \"./scenery-ui/index.js\";\n")
	for index, slot := range splitPageSlotNames {
		children := orderedChildren(page.split.Spec, slot)
		if len(children) == 0 {
			continue
		}
		componentAddress := resolveResourceRef(page.split, refString(children[0]["component"]), "react_component")
		component := resourcesByAddress(result.Manifest)[componentAddress]
		module, err := reactComponentImport(result, reactRoot, component)
		if err != nil {
			return "", err
		}
		alias := fmt.Sprintf("ScenerySplitSlot%d", index+1)
		aliases[slot] = alias
		fmt.Fprintf(&b, "import { %s as %s } from %s;\n", stringValue(component.Spec["export"]), alias, strconv.Quote(module))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "const slots = defineSplitPageSlots<%s>()({\n", resultType)
	for _, slot := range splitPageSlotNames {
		if alias := aliases[slot]; alias != "" {
			fmt.Fprintf(&b, "  %s: %s,\n", tsName(slot), alias)
		}
	}
	b.WriteString("});\n\n")
	fmt.Fprintf(&b, "const queryKey = [\"scenery\", \"split_page\", %s] as const;\n\n", strconv.Quote(page.split.Address))
	method := reactOperationClientMethod(page.operation, page.binding, bindings)
	writeReactPageOpen(&b, goName(page.split.Name), goName(target.Name))
	writeReactLoad(&b, "", "SplitPageState<"+resultType+">", func(b *strings.Builder) {
		fmt.Fprintf(b, "    const outcome = await client.%s({});\n", method)
	}, `{ kind: "result", data: outcome.value }`)
	fmt.Fprintf(&b, "  const queryParameter = %s;\n", strconv.Quote(defaultString(stringValue(page.split.Spec["query_parameter"]), "selected")))
	b.WriteString("  const query = useQuery({ queryKey, queryFn: load });\n")
	b.WriteString("  const state: SplitPageState<" + resultType + "> = requestStateFromQuery<{ readonly data: " + resultType + " }>(query);\n")
	b.WriteString("  const [selection, setSelection] = useState<string | null>(() => typeof globalThis.location === \"undefined\" ? null : new URLSearchParams(globalThis.location.search).get(queryParameter));\n")
	b.WriteString("  useEffect(() => { if (typeof globalThis.location === \"undefined\" || typeof globalThis.addEventListener !== \"function\") return; const syncSelectionFromURL = () => setSelection(new URLSearchParams(globalThis.location.search).get(queryParameter)); syncSelectionFromURL(); globalThis.addEventListener(\"popstate\", syncSelectionFromURL); return () => globalThis.removeEventListener(\"popstate\", syncSelectionFromURL); }, [queryParameter]);\n")
	b.WriteString("  const onSelectionChange = useCallback((next: string | null) => { setSelection(next); if (typeof globalThis.location !== \"undefined\") { const nextURL = new URL(globalThis.location.href); if (next === null) nextURL.searchParams.delete(queryParameter); else nextURL.searchParams.set(queryParameter, next); globalThis.history.pushState({}, \"\", nextURL); } }, [queryParameter]);\n")
	b.WriteString("  const slotProps: SplitPageSlotProps<" + resultType + "> = { state, selection, onSelectionChange };\n")
	fmt.Fprintf(&b, "  return <SplitPage sidebarTitle=%s", jsxStringExpression(stringValue(page.split.Spec["title"])))
	if label := stringValue(page.split.Spec["aria_label"]); label != "" {
		fmt.Fprintf(&b, " ariaLabel=%s", jsxStringExpression(label))
	}
	if label := stringValue(page.split.Spec["sidebar_label"]); label != "" {
		fmt.Fprintf(&b, " sidebarLabel=%s", jsxStringExpression(label))
	}
	if aliases["sidebar_actions"] != "" {
		b.WriteString(" sidebarActions={<slots.sidebarActions {...slotProps} />}")
	}
	b.WriteString(" sidebar={<slots.sidebar {...slotProps} />}")
	if aliases["detail_header"] != "" {
		b.WriteString(" detailHeader={<slots.detailHeader {...slotProps} />}")
	}
	b.WriteString(" detail={<slots.detail {...slotProps} />} />;\n}\n")
	return b.String(), nil
}

func reactDialogFields(dialog reactTableDialog) []map[string]any {
	declared := orderedChildren(dialog.dialog.Spec, "field")
	overrides := map[string]map[string]any{}
	for _, field := range declared {
		overrides[stringValue(field["name"])] = field
	}
	recordFields := namedChildren(dialog.input.Spec, "field")
	result := make([]map[string]any, 0, len(recordFields))
	for _, original := range recordFields {
		field := cloneMapValue(original)
		maps.Copy(field, overrides[stringValue(original["name"])])
		result = append(result, field)
	}
	return result
}

func reactDialogInitialValue(dialog reactTableDialog, field map[string]any, resources map[string]Resource) string {
	statusMap := resolveReferencedStatusMap(resources, dialog.dialog, field["status_map"])
	if statuses := orderedChildren(statusMap.Spec, "status"); len(statuses) > 0 {
		return stringValue(statuses[0]["name"])
	}
	if values := enumWireValues(resources, dialog.input.Module, field["type"]); len(values) > 0 {
		return values[0]
	}
	return ""
}

func reactDialogControlUsage(page reactTablePage, resources map[string]Resource) map[string]bool {
	used := map[string]bool{}
	for _, dialog := range page.dialogs {
		for _, field := range reactDialogFields(dialog) {
			control := defaultString(stringValue(field["control"]), "auto")
			statusMap := resolveReferencedStatusMap(resources, dialog.dialog, field["status_map"])
			enumValues := enumWireValues(resources, dialog.input.Module, field["type"])
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

func reactDialogSubmitValue(dialog reactTableDialog, field map[string]any, expression string, resources map[string]Resource) string {
	values := enumWireValues(resources, dialog.input.Module, field["type"])
	if len(values) > 0 {
		predicate := reactLiteralPredicate(expression, values)
		if isOptionalType(field["type"]) {
			return predicate + " ? " + expression + " : undefined"
		}
		return predicate + " ? " + expression + " : " + strconv.Quote(values[0])
	}
	if isOptionalType(field["type"]) {
		return expression + " || undefined"
	}
	return expression
}

func primaryDialogIndex(dialogs []reactTableDialog) int {
	for index, dialog := range dialogs {
		if dialog.action.Spec["primary"] == true {
			return index
		}
	}
	if len(dialogs) > 0 {
		return 0
	}
	return -1
}

func headerTableDialogs(dialogs []reactTableDialog) []reactTableDialog {
	result := make([]reactTableDialog, 0, len(dialogs))
	for _, dialog := range dialogs {
		if dialog.action.Address != "" {
			result = append(result, dialog)
		}
	}
	return result
}

func primaryTableDialog(dialogs []reactTableDialog) *reactTableDialog {
	headers := headerTableDialogs(dialogs)
	index := primaryDialogIndex(headers)
	if index < 0 {
		return nil
	}
	return &headers[index]
}

func rowTableDialog(dialogs []reactTableDialog) *reactTableDialog {
	for index := range dialogs {
		if dialogs[index].seedFromRow {
			return &dialogs[index]
		}
	}
	return nil
}

func reactDialogOpenExpression(dialog reactTableDialog, rowExpression string) string {
	if dialog.seedFromRow {
		return fmt.Sprintf("open%s(%s)", goName(dialog.dialog.Name), rowExpression)
	}
	return fmt.Sprintf("set%sOpen(true)", goName(dialog.dialog.Name))
}

func reactTableUsesIcons(table Resource) bool {
	for _, action := range orderedChildren(table.Spec, "action") {
		if stringValue(action["icon"]) != "" {
			return true
		}
	}
	for _, export := range orderedChildren(table.Spec, "export") {
		if stringValue(export["icon"]) != "" {
			return true
		}
	}
	return false
}

func reactTableStatsUsesIcons(stats *reactTableStats) bool {
	if stats == nil {
		return false
	}
	for _, tile := range orderedChildren(stats.spec.Spec, "tile") {
		if stringValue(tile["icon"]) != "" {
			return true
		}
	}
	return false
}

func referencedReactStatusMaps(resources map[string]Resource, page reactTablePage) []string {
	set := map[string]bool{}
	add := func(owner Resource, value any) {
		if statusMap := resolveReferencedStatusMap(resources, owner, value); statusMap.Address != "" {
			set[reactStatusMapName(statusMap)] = true
		}
	}
	// Columns pass status-map constants to QueryTable at runtime. Filters and
	// dialog fields compile their maps into literal option lists instead.
	for _, column := range orderedChildren(page.table.Spec, "column") {
		add(page.table, column["status_map"])
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveReferencedStatusMap(resources map[string]Resource, owner Resource, value any) Resource {
	if value == nil {
		return Resource{}
	}
	return resources[resolveResourceRef(owner, refString(value), "status_map")]
}

func reactFilterOptions(enumValues []string, statusMap Resource) string {
	if statusMap.Address != "" {
		var options []string
		for _, status := range orderedChildren(statusMap.Spec, "status") {
			options = append(options, "{ value: "+strconv.Quote(stringValue(status["name"]))+", label: "+strconv.Quote(stringValue(status["label"]))+" }")
		}
		return strings.Join(options, ", ")
	}
	var options []string
	for _, value := range enumValues {
		options = append(options, "{ value: "+strconv.Quote(value)+", label: "+strconv.Quote(humanLabel(value))+" }")
	}
	return strings.Join(options, ", ")
}

func unwrapReactType(value string) string {
	value = strings.TrimSpace(value)
	for {
		open := strings.IndexByte(value, '(')
		if open < 0 || !strings.HasSuffix(value, ")") {
			return value
		}
		wrapper := strings.TrimSpace(value[:open])
		if wrapper != "optional" && wrapper != "nullable" {
			return value
		}
		value = strings.TrimSpace(value[open+1 : len(value)-1])
	}
}

func reactPageTypeImports(page reactTablePage, fields map[string]map[string]any, resources map[string]Resource) []string {
	set := map[string]bool{}
	shape := resolveOperationInputShape(resources, page.operation)
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		name := stringValue(filter["name"])
		field := fields[name]
		input := defaultString(strings.TrimSpace(stringValue(filter["input"])), name)
		fieldType := shape.Fields[input].Type
		if field != nil && len(enumWireValues(resources, page.operation.Module, fieldType)) > 0 {
			set[reactTableFilterValueType(fieldType)] = true
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		if name != goName(page.record.Name) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func namedResourceChild(spec map[string]any, kind, name string) map[string]any {
	for _, child := range namedChildren(spec, kind) {
		if stringValue(child["name"]) == name {
			return child
		}
	}
	return nil
}

func unwrapReactCollectionType(value, collection string) (string, bool) {
	value = unwrapReactType(value)
	prefix := collection + "("
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, ")") {
		return "", false
	}
	return strings.TrimSpace(value[len(prefix) : len(value)-1]), true
}

func reactTableResultExpression(page reactTablePage) string {
	metadata := reactTableMetadataResultExpression(page)
	if page.pagination == "cursor" {
		return `{ kind: "result", items: outcome.value.items, nextCursor: outcome.value.nextCursor }`
	}
	if page.pagination == "page" {
		pagination := firstReactTableChild(page.table.Spec, "pagination")
		return fmt.Sprintf(`{ kind: "result", items: outcome.value.%s, total: reactTableSafeTotal(outcome.value.%s)%s }`, tsName(page.itemsField), tsName(stringValue(pagination["total"])), metadata)
	}
	return fmt.Sprintf(`{ kind: "result", items: outcome.value.%s%s }`, tsName(page.itemsField), metadata)
}

func reactTableMetadataResultExpression(page reactTablePage) string {
	if len(page.metadataFields) == 0 {
		return ""
	}
	fields := make([]string, 0, len(page.metadataFields))
	for _, field := range page.metadataFields {
		name := tsName(field)
		fields = append(fields, name+": outcome.value."+name)
	}
	return ", metadata: { " + strings.Join(fields, ", ") + " }"
}

func firstReactTableChild(value map[string]any, kind string) map[string]any {
	children := orderedChildren(value, kind)
	if len(children) == 0 {
		return nil
	}
	return children[0]
}

func reactTableMappedName(mapping map[string]any, key, fallback string) string {
	if mapping == nil {
		return fallback
	}
	return defaultString(strings.TrimSpace(stringValue(mapping[key])), fallback)
}

func reactTableSearchable(page reactTablePage, shape operationInputShape, input string) bool {
	if page.pagination == "cursor" {
		list, _ := page.crud.Spec["list"].(map[string]any)
		return len(stringValues(list["search"])) > 0
	}
	return shape.Fields[input].Name != ""
}

func reactTableListInput(value any) bool {
	_, ok := unwrapReactCollectionType(unwrapReactType(typeExpression(value)), "list")
	return ok
}

func reactTableIntegerExpression(expression string, value any) string {
	switch unwrapReactType(typeExpression(value)) {
	case "int", "int64", "uint64", "size":
		return "BigInt(" + expression + ")"
	default:
		return expression
	}
}

func reactTableLiteral(value, valueType any) string {
	if scalar, ok := value.(map[string]any); ok && stringValue(scalar["$scalar"]) != "" {
		value = scalar["value"]
	}
	typeName := tsType(valueType)
	switch typeName {
	case "bigint":
		return fmt.Sprint(value) + "n"
	case "number":
		return fmt.Sprint(value)
	case "boolean":
		if value == true {
			return "true"
		}
		return "false"
	case "string":
		return strconv.Quote(stringValue(value))
	default:
		return strconv.Quote(stringValue(value)) + " as " + typeName
	}
}

func reactTableFilterValueType(value any) string {
	expression := unwrapReactType(typeExpression(value))
	if inner, ok := unwrapReactCollectionType(expression, "list"); ok {
		expression = inner
	}
	return tsType(map[string]any{"$expression": expression})
}

func reactClientMethod(page reactTablePage, bindings []Resource) string {
	return reactOperationClientMethod(page.operation, page.binding, bindings)
}

func reactOperationClientMethod(operation, selectedBinding Resource, bindings []Resource) string {
	count := 0
	for _, binding := range bindings {
		if lastRef(refString(binding.Spec["operation"])) == operation.Name {
			count++
		}
	}
	method := tsName(operation.Name)
	if count > 1 {
		method += "Via" + goName(selectedBinding.Name)
	}
	return method
}

func reactComponentImport(result *Result, reactRoot string, component Resource) (string, error) {
	base := result.Root
	if component.Module != "app" {
		for _, resource := range result.Manifest.Resources {
			if resource.Kind == "scenery.module" && moduleInstancePath(resource) == component.Module {
				base = filepath.Join(result.Root, filepath.FromSlash(defaultString(stringValue(resource.Spec["workspace_package_root"]), stringValue(resource.Spec["source"]))))
				break
			}
		}
	}
	path := filepath.Join(base, filepath.FromSlash(stringValue(component.Spec["module"])))
	relative, err := filepath.Rel(reactRoot, path)
	if err != nil {
		return "", fmt.Errorf("react_component %s import path: %w", component.Address, err)
	}
	extension := filepath.Ext(relative)
	if extension == ".ts" || extension == ".tsx" || extension == ".jsx" {
		relative = strings.TrimSuffix(relative, extension) + ".js"
	}
	relative = filepath.ToSlash(relative)
	if !strings.HasPrefix(relative, ".") {
		relative = "./" + relative
	}
	return relative, nil
}

func reactLiteralPredicate(expression string, values []string) string {
	if len(values) == 0 {
		return "false"
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = expression + " === " + strconv.Quote(value)
	}
	return strings.Join(parts, " || ")
}

func renderReactRowLink(template string) string {
	expression := strconv.Quote(template)
	for _, match := range httpPathParameterPattern.FindAllStringSubmatch(template, -1) {
		expression = "(" + expression + ").replace(" + strconv.Quote("{"+match[1]+"}") + ", encodeURIComponent(String(row." + tsName(match[1]) + ")))"
	}
	return expression
}

func humanLabel(value string) string {
	parts := strings.Fields(strings.ReplaceAll(value, "_", " "))
	for index := range parts {
		first, size := utf8.DecodeRuneInString(parts[index])
		parts[index] = string(unicode.ToUpper(first)) + parts[index][size:]
	}
	return strings.Join(parts, " ")
}

func jsxStringExpression(value string) string {
	return "{" + strconv.Quote(value) + "}"
}

func unionOrNever(values []string) string {
	if len(values) == 0 {
		return "never"
	}
	return strings.Join(values, " | ")
}

func objectTypeOrEmpty(fields []string) string {
	if len(fields) == 0 {
		return "Record<never, never>"
	}
	return "{ " + strings.Join(fields, "; ") + " }"
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}
