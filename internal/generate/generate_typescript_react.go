package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"scenery.sh/internal/spec"
)

type reactTablePage struct {
	table, crud, record, operation, binding Resource
	stats                                   *reactTableStats
	dialogs                                 []reactTableDialog
	itemsField                              string
	paginated                               bool
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

func renderTypeScriptReact(result *Result, target Resource, root string, bindings []Resource) ([]generatedFile, []string, error) {
	if _, ok := target.Spec["react"].(map[string]any); !ok {
		return nil, []string{}, nil
	}
	reactRoot := filepath.Join(root, "react")
	catalogRoot := filepath.Join(reactRoot, "scenery-ui")
	files, err := renderUICatalog(result.Root, catalogRoot)
	if err != nil {
		return nil, nil, err
	}
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
	routePages := reactRoutePages(pages, splitPages, contentPages)
	files = append(files,
		generatedFile{Path: filepath.Join(reactRoot, "routes.generated.ts"), Bytes: []byte(renderReactRoutes(result, routePages))},
		generatedFile{Path: filepath.Join(reactRoot, "app.generated.tsx"), Bytes: []byte(renderReactAppAdapter())},
		generatedFile{Path: filepath.Join(reactRoot, "index.ts"), Bytes: []byte(renderReactIndex(result.Manifest.Resources))},
	)
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
			page = reactTablePage{table: table, crud: source, record: record, operation: operation, binding: binding, itemsField: "items", paginated: true}
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
			page = reactTablePage{table: table, record: record, operation: operation, binding: binding, itemsField: itemsField}
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

func renderReactIndex(resources []Resource) string {
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	b.WriteString("export * from \"./routes.generated.js\";\n")
	b.WriteString("export * from \"./app.generated.js\";\n")
	for _, resource := range resources {
		if resource.Kind == "scenery.status-map" {
			b.WriteString("export * from \"./status-maps.generated.js\";\n")
			break
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
		binding := selectedBindings[resolveResourceRef(content, refString(content.Spec["source"]), "binding")]
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
	fmt.Fprintf(b, "  const load = useCallback(async (%s): Promise<%s> => {\n", params, stateType)
	writeCall(b)
	fmt.Fprintf(b, "    if (outcome.kind === \"result\") return %s;\n", resultExpr)
	b.WriteString("    return { kind: \"error\", name: outcome.name, problem: outcome.problem };\n")
	b.WriteString("  }, [client]);\n")
}

func renderReactContentPage(result *Result, target Resource, reactRoot string, page reactContentPage, bindings []Resource) (string, error) {
	resultType := tsType(namedChildren(page.operation.Spec, "result")[0]["type"])
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	b.WriteString("import { useQuery } from \"@tanstack/react-query\";\n")
	b.WriteString("import { useCallback, useMemo } from \"react\";\n")
	fmt.Fprintf(&b, "import { %sClient, url } from \"../index.js\";\n", goName(target.Name))
	fmt.Fprintf(&b, "import type { %s } from \"../index.js\";\n", resultType)
	b.WriteString("import { Page, defineContentPageSlots, requestStateFromQuery } from \"./scenery-ui/index.js\";\n")
	b.WriteString("import type { ContentPageSlotProps, ContentPageState } from \"./scenery-ui/index.js\";\n")
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
	fmt.Fprintf(&b, "  return <Page title=%s", jsxStringExpression(stringValue(page.content.Spec["title"])))
	if label := stringValue(page.content.Spec["aria_label"]); label != "" {
		fmt.Fprintf(&b, " ariaLabel=%s", jsxStringExpression(label))
	}
	if maxWidth, ok := integerValue(page.content.Spec["max_width"]); ok {
		fmt.Fprintf(&b, " maxWidth={%d}", maxWidth)
	}
	if aliases["actions"] != "" {
		b.WriteString(" actions={<slots.actions {...slotProps} />}")
	}
	b.WriteString("><slots.content {...slotProps} /></Page>;\n}\n")
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

func renderReactTablePage(result *Result, target Resource, reactRoot string, page reactTablePage, bindings []Resource) (string, error) {
	resources := resourcesByAddress(result.Manifest)
	if len(orderedChildren(page.table.Spec, "stats")) > 0 && page.stats == nil {
		return "", fmt.Errorf("table_page %s stats binding is not included by TypeScript client %s", page.table.Address, target.Address)
	}
	if actions := orderedChildren(page.table.Spec, "action"); len(actions) != len(headerTableDialogs(page.dialogs)) {
		return "", fmt.Errorf("table_page %s dialog bindings are not all included by TypeScript client %s", page.table.Address, target.Address)
	}
	if details := orderedChildren(page.table.Spec, "row_detail"); len(details) == 1 && details[0]["dialog"] != nil && rowTableDialog(page.dialogs) == nil {
		return "", fmt.Errorf("table_page %s row dialog binding is not included by TypeScript client %s", page.table.Address, target.Address)
	}
	fields := map[string]map[string]any{}
	for _, field := range namedChildren(page.record.Spec, "field") {
		fields[stringValue(field["name"])] = field
	}
	components := map[string]Resource{}
	collect := func(value any) {
		if value == nil {
			return
		}
		address := resolveResourceRef(page.table, refString(value), "react_component")
		if component := resources[address]; component.Address != "" {
			components[address] = component
		}
	}
	for _, kind := range []string{"column", "filter", "toolbar", "empty", "row_detail"} {
		for _, item := range orderedChildren(page.table.Spec, kind) {
			collect(item["component"])
		}
	}
	componentAddresses := make([]string, 0, len(components))
	for address := range components {
		componentAddresses = append(componentAddresses, address)
	}
	sort.Strings(componentAddresses)

	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
	var tanstackImports []string
	if page.stats != nil {
		tanstackImports = append(tanstackImports, "useQuery")
	}
	if len(page.dialogs) > 0 {
		tanstackImports = append(tanstackImports, "useMutation", "useQueryClient")
	}
	if len(tanstackImports) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"@tanstack/react-query\";\n", strings.Join(tanstackImports, ", "))
	}
	reactImports := []string{"useCallback", "useMemo"}
	if len(page.dialogs) > 0 {
		reactImports = append(reactImports, "useState")
	}
	fmt.Fprintf(&b, "import { %s } from \"react\";\n", strings.Join(reactImports, ", "))
	clientImports := []string{goName(target.Name) + "Client"}
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		field := fields[stringValue(filter["name"])]
		if field != nil && unwrapReactType(typeExpression(field["type"])) == "datetime" {
			clientImports = append(clientImports, "dateTime")
			break
		}
	}
	clientImports = append(clientImports, "url")
	fmt.Fprintf(&b, "import { %s } from \"../index.js\";\n", strings.Join(clientImports, ", "))
	fmt.Fprintf(&b, "import type { %s", goName(page.record.Name))
	typeImports := reactPageTypeImports(page, fields, resources)
	if page.stats != nil {
		typeImports = append(typeImports, goName(page.stats.record.Name))
	}
	for _, name := range typeImports {
		fmt.Fprintf(&b, ", %s", name)
	}
	b.WriteString(" } from \"../index.js\";\n")
	uiImports := []string{"Page", "QueryTable"}
	if page.stats != nil {
		uiImports = append(uiImports, "QueryState", "StatGrid", "StatTile", "queryStateProps", "requestStateFromQuery")
	}
	if len(page.dialogs) > 0 {
		uiImports = append(uiImports, "Button", "FormDialog", "FormProblem")
		dialogControls := reactDialogControlUsage(page, resources)
		for _, control := range []string{"SelectField", "TextAreaField", "TextField"} {
			if dialogControls[control] {
				uiImports = append(uiImports, control)
			}
		}
	}
	if reactTableUsesIcons(page.table) {
		uiImports = append(uiImports, "Icon")
	}
	if len(componentAddresses) > 0 {
		uiImports = append(uiImports, "defineTablePageSlots")
	}
	fmt.Fprintf(&b, "import { %s } from \"./scenery-ui/index.js\";\n", strings.Join(uiImports, ", "))
	uiTypeImports := []string{"TablePageQuery", "TablePageResult"}
	for _, column := range orderedChildren(page.table.Spec, "column") {
		if column["component"] != nil {
			uiTypeImports = append([]string{"TablePageCellProps"}, uiTypeImports...)
			break
		}
	}
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		field := fields[stringValue(filter["name"])]
		if filter["component"] != nil && field != nil && (len(enumWireValues(resources, page.record.Module, field["type"])) > 0 || unwrapReactType(typeExpression(field["type"])) == "string") {
			uiTypeImports = append([]string{"TablePageFilterProps"}, uiTypeImports...)
			break
		}
	}
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		field := fields[stringValue(filter["name"])]
		if filter["component"] != nil && field != nil && unwrapReactType(typeExpression(field["type"])) == "datetime" {
			uiTypeImports = append([]string{"TablePageDateTimeRange"}, uiTypeImports...)
			break
		}
	}
	fmt.Fprintf(&b, "import type { %s } from \"./scenery-ui/index.js\";\n", strings.Join(uiTypeImports, ", "))
	statusMapImports := referencedReactStatusMaps(resources, page)
	if len(statusMapImports) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./status-maps.generated.js\";\n", strings.Join(statusMapImports, ", "))
	}
	aliases := map[string]string{}
	for index, address := range componentAddresses {
		component := components[address]
		module, err := reactComponentImport(result, reactRoot, component)
		if err != nil {
			return "", err
		}
		alias := fmt.Sprintf("SceneryOverride%d", index+1)
		aliases[address] = alias
		fmt.Fprintf(&b, "import { %s as %s } from %s;\n", stringValue(component.Spec["export"]), alias, strconv.Quote(module))
	}
	b.WriteString("\n")
	rowType := goName(page.record.Name)
	for _, column := range orderedChildren(page.table.Spec, "column") {
		if column["component"] == nil {
			continue
		}
		field := stringValue(column["name"])
		alias := aliases[resolveResourceRef(page.table, refString(column["component"]), "react_component")]
		fmt.Fprintf(&b, "function %s%sCell(props: TablePageCellProps<%s, %s[%s]>) { return <%s row={props.row} value={props.row.%s} />; }\n", goName(page.table.Name), goName(field), rowType, rowType, strconv.Quote(tsName(field)), alias, tsName(field))
	}
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		if filter["component"] == nil {
			continue
		}
		field := stringValue(filter["name"])
		alias := aliases[resolveResourceRef(page.table, refString(filter["component"]), "react_component")]
		values := enumWireValues(resources, page.record.Module, fields[field]["type"])
		if len(values) > 0 {
			typeName := tsType(fields[field]["type"])
			fmt.Fprintf(&b, "function %s%sFilter(props: TablePageFilterProps<string>) { const value = props.value !== undefined && (%s) ? props.value : undefined; return <%s label={props.label} value={value} onChange={props.onChange} />; }\n", goName(page.table.Name), goName(field), reactLiteralPredicate("props.value", values), alias)
			_ = typeName // the imported enum type is exercised by the generated list input.
		} else {
			fmt.Fprintf(&b, "const %s%sFilter = %s;\n", goName(page.table.Name), goName(field), alias)
		}
	}

	cellKeys, filterKeys, filterValueTypes := []string{}, []string{}, []string{}
	for _, column := range orderedChildren(page.table.Spec, "column") {
		if column["component"] != nil {
			cellKeys = append(cellKeys, strconv.Quote(tsName(stringValue(column["name"]))))
		}
	}
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		if filter["component"] != nil {
			field := stringValue(filter["name"])
			key := strconv.Quote(tsName(field))
			filterKeys = append(filterKeys, key)
			valueType := "TablePageDateTimeRange"
			if len(enumWireValues(resources, page.record.Module, fields[field]["type"])) > 0 || unwrapReactType(typeExpression(fields[field]["type"])) == "string" {
				valueType = "string"
			}
			filterValueTypes = append(filterValueTypes, "readonly "+key+": "+valueType)
		}
	}
	if len(componentAddresses) > 0 {
		fmt.Fprintf(&b, "\nconst slots = defineTablePageSlots<%s, %s, %s>()({\n", rowType, unionOrNever(cellKeys), objectTypeOrEmpty(filterValueTypes))
		if len(cellKeys) > 0 {
			b.WriteString("  cells: {\n")
			for _, column := range orderedChildren(page.table.Spec, "column") {
				if column["component"] != nil {
					field := stringValue(column["name"])
					fmt.Fprintf(&b, "    %s: %s%sCell,\n", tsName(field), goName(page.table.Name), goName(field))
				}
			}
			b.WriteString("  },\n")
		}
		if len(filterKeys) > 0 {
			b.WriteString("  filters: {\n")
			for _, filter := range orderedChildren(page.table.Spec, "filter") {
				if filter["component"] != nil {
					field := stringValue(filter["name"])
					fmt.Fprintf(&b, "    %s: %s%sFilter,\n", tsName(field), goName(page.table.Name), goName(field))
				}
			}
			b.WriteString("  },\n")
		}
		for _, slot := range []string{"toolbar", "empty"} {
			for _, value := range orderedChildren(page.table.Spec, slot) {
				if value["component"] != nil {
					alias := aliases[resolveResourceRef(page.table, refString(value["component"]), "react_component")]
					fmt.Fprintf(&b, "  %s: %s,\n", slot, alias)
				}
			}
		}
		for _, value := range orderedChildren(page.table.Spec, "row_detail") {
			alias := aliases[resolveResourceRef(page.table, refString(value["component"]), "react_component")]
			slot := "rowDetail"
			if stringValue(value["presentation"]) == "panel" {
				slot = "detailPanel"
			}
			fmt.Fprintf(&b, "  %s: %s,\n", slot, alias)
		}
		b.WriteString("});\n\n")
	}

	fmt.Fprintf(&b, "const queryKey = [\"scenery\", \"table_page\", %s] as const;\n\n", strconv.Quote(page.table.Address))
	if page.stats != nil {
		fmt.Fprintf(&b, "const statsQueryKey = [\"scenery\", \"table_page\", %s, \"stats\"] as const;\n\n", strconv.Quote(page.table.Address))
	}
	method := reactClientMethod(page, bindings)
	writeReactPageOpen(&b, goName(page.table.Name), goName(target.Name))
	if len(page.dialogs) > 0 {
		b.WriteString("  const queryClient = useQueryClient();\n")
	}
	if page.stats != nil {
		writeReactTableStatsState(&b, page, bindings)
	}
	for _, dialog := range page.dialogs {
		writeReactTableDialogState(&b, page, dialog, bindings, resources, rowType)
	}
	writeReactLoad(&b, "query: TablePageQuery, signal?: AbortSignal", "TablePageResult<"+rowType+">", func(b *strings.Builder) {
		fmt.Fprintf(b, "    const outcome = await client.%s({\n", method)
		shape := resolveOperationInputShape(resources, page.operation)
		if (page.paginated && len(stringValues(page.crud.Spec["list"].(map[string]any)["search"])) > 0) ||
			(!page.paginated && shape.Fields["search"].Name != "") {
			b.WriteString("      search: query.search,\n")
		}
		for _, filter := range orderedChildren(page.table.Spec, "filter") {
			field := stringValue(filter["name"])
			fieldType := fields[field]["type"]
			if !page.paginated {
				fieldType = shape.Fields[field].Type
			}
			values := enumWireValues(resources, page.operation.Module, fieldType)
			if len(values) > 0 {
				fmt.Fprintf(b, "      %s: Array.isArray(query.filters[%s]) ? query.filters[%s].filter((value): value is %s => %s) : undefined,\n", tsName(field), strconv.Quote(field), strconv.Quote(field), reactTableFilterValueType(fieldType), reactLiteralPredicate("value", values))
			} else if unwrapReactType(typeExpression(fieldType)) == "string" {
				fmt.Fprintf(b, "      %s: Array.isArray(query.filters[%s]) ? query.filters[%s] : undefined,\n", tsName(field), strconv.Quote(field), strconv.Quote(field))
			} else {
				fmt.Fprintf(b, "      %sFrom: typeof query.filters[%s] === \"string\" ? dateTime(query.filters[%s]) : undefined,\n", tsName(field), strconv.Quote(field+"_from"), strconv.Quote(field+"_from"))
				fmt.Fprintf(b, "      %sTo: typeof query.filters[%s] === \"string\" ? dateTime(query.filters[%s]) : undefined,\n", tsName(field), strconv.Quote(field+"_to"), strconv.Quote(field+"_to"))
			}
		}
		var sorts []string
		if page.paginated {
			sorts = stringValues(page.crud.Spec["list"].(map[string]any)["sorts"])
		} else {
			sorts = enumWireValues(resources, page.operation.Module, shape.Fields["sort"].Type)
		}
		if len(sorts) > 0 {
			fmt.Fprintf(b, "      sort: query.sort !== undefined && (%s) ? query.sort : undefined,\n", reactLiteralPredicate("query.sort", sorts))
			b.WriteString("      direction: query.direction,\n")
		}
		if page.paginated {
			b.WriteString("      cursor: query.cursor,\n      limit: BigInt(query.limit),\n")
		}
		b.WriteString("    }, { signal });\n")
	}, reactTableResultExpression(page))
	fmt.Fprintf(&b, "  return <><Page title=%s fill", jsxStringExpression(stringValue(page.table.Spec["title"])))
	if len(orderedChildren(page.table.Spec, "toolbar")) > 0 || len(headerTableDialogs(page.dialogs)) > 0 {
		b.WriteString(" actions={<>\n")
		if len(orderedChildren(page.table.Spec, "toolbar")) > 0 {
			b.WriteString("    <slots.toolbar />\n")
		}
		headerDialogs := headerTableDialogs(page.dialogs)
		primaryIndex := primaryDialogIndex(headerDialogs)
		for index, dialog := range headerDialogs {
			fmt.Fprintf(&b, "    <Button label=%s", strconv.Quote(stringValue(dialog.action.Spec["label"])))
			if icon := stringValue(dialog.action.Spec["icon"]); icon != "" {
				fmt.Fprintf(&b, " icon={<Icon icon=%s size=\"sm\" />}", strconv.Quote(icon))
			}
			fmt.Fprintf(&b, " onClick={() => %s} size=\"sm\" variant=%s />\n",
				reactDialogOpenExpression(dialog, ""), strconv.Quote(map[bool]string{true: "primary", false: "secondary"}[index == primaryIndex]))
		}
		b.WriteString("  </>}")
	}
	b.WriteString(">")
	if page.stats != nil {
		b.WriteString("\n  <QueryState {...queryStateProps(statsState, \"statistics\")} retry={() => void statsQuery.refetch()}>\n")
		b.WriteString("    {statsState.kind === \"result\" ? <StatGrid columns={")
		fmt.Fprintf(&b, "%d", len(orderedChildren(page.stats.spec.Spec, "tile")))
		b.WriteString("}>\n")
		for _, tile := range orderedChildren(page.stats.spec.Spec, "tile") {
			fmt.Fprintf(&b, "      <StatTile label=%s value={statsState.value.%s} />\n", jsxStringExpression(stringValue(tile["label"])), tsName(stringValue(tile["name"])))
		}
		b.WriteString("    </StatGrid> : null}\n  </QueryState>\n")
	}
	fmt.Fprintf(&b, "<QueryTable<%s> resource=%s fill", rowType, jsxStringExpression(stringValue(page.table.Spec["title"])))
	if description := stringValue(page.table.Spec["description"]); description != "" {
		fmt.Fprintf(&b, " description=%s", jsxStringExpression(description))
	}
	b.WriteString(" columns={[\n")
	for _, column := range orderedChildren(page.table.Spec, "column") {
		field := stringValue(column["name"])
		label := defaultString(stringValue(column["label"]), humanLabel(field))
		appearance := defaultString(stringValue(column["appearance"]), "auto")
		fmt.Fprintf(&b, "    { field: %s, label: %s, appearance: %s", strconv.Quote(tsName(field)), strconv.Quote(label), strconv.Quote(appearance))
		if column["component"] != nil {
			fmt.Fprintf(&b, ", component: slots.cells.%s", tsName(field))
		}
		if column["hidden"] == true {
			b.WriteString(", hidden: true")
		}
		if column["export"] == false {
			b.WriteString(", export: false")
		}
		if statusMap := resolveReferencedStatusMap(resources, page.table, column["status_map"]); statusMap.Address != "" {
			fmt.Fprintf(&b, ", statusMap: %s", reactStatusMapName(statusMap))
		}
		b.WriteString(" },\n")
	}
	b.WriteString("  ]} filters={[\n")
	for _, filter := range orderedChildren(page.table.Spec, "filter") {
		field := stringValue(filter["name"])
		label := defaultString(stringValue(filter["label"]), humanLabel(field))
		values := enumWireValues(resources, page.record.Module, fields[field]["type"])
		statusMap := resolveReferencedStatusMap(resources, page.table, filter["status_map"])
		if len(values) > 0 || statusMap.Address != "" {
			options := reactFilterOptions(values, statusMap)
			fmt.Fprintf(&b, "    { field: %s, label: %s, kind: \"enum\", options: [%s]", strconv.Quote(field), strconv.Quote(label), options)
		} else {
			fmt.Fprintf(&b, "    { field: %s, label: %s, kind: \"datetime\"", strconv.Quote(field), strconv.Quote(label))
		}
		if filter["component"] != nil {
			fmt.Fprintf(&b, ", component: slots.filters.%s", tsName(field))
		}
		if filter["pinned"] == true {
			b.WriteString(", pinned: true")
		}
		b.WriteString(" },\n")
	}
	b.WriteString("  ]}")
	if groups := orderedChildren(page.table.Spec, "group"); len(groups) > 0 {
		b.WriteString(" groups={[\n")
		for _, group := range groups {
			field := stringValue(group["name"])
			fmt.Fprintf(&b, "    { field: %s, label: %s", strconv.Quote(field), strconv.Quote(defaultString(stringValue(group["label"]), humanLabel(field))))
			if order := stringValues(group["order"]); len(order) > 0 {
				fmt.Fprintf(&b, ", order: [%s]", quotedList(order))
			}
			if group["default"] == true {
				b.WriteString(", default: true")
			}
			b.WriteString(" },\n")
		}
		b.WriteString("  ]}")
	}
	b.WriteString(" sorts={[\n")
	for _, sortSpec := range orderedChildren(page.table.Spec, "sort") {
		field := stringValue(sortSpec["name"])
		fmt.Fprintf(&b, "    { field: %s, label: %s", strconv.Quote(field), strconv.Quote(defaultString(stringValue(sortSpec["label"]), humanLabel(field))))
		if direction := stringValue(sortSpec["default"]); direction != "" {
			fmt.Fprintf(&b, ", default: %s", strconv.Quote(direction))
		}
		b.WriteString(" },\n")
	}
	b.WriteString("  ]}")
	if (page.paginated && len(stringValues(page.crud.Spec["list"].(map[string]any)["search"])) > 0) ||
		(!page.paginated && resolveOperationInputShape(resources, page.operation).Fields["search"].Name != "") {
		b.WriteString(" searchable")
	}
	if rowLink := stringValue(page.table.Spec["row_link"]); rowLink != "" {
		fmt.Fprintf(&b, " rowLink={(row) => %s}", renderReactRowLink(rowLink))
	}
	if len(orderedChildren(page.table.Spec, "empty")) > 0 {
		b.WriteString(" empty={slots.empty}")
	}
	if len(orderedChildren(page.table.Spec, "row_detail")) > 0 {
		rowDetail := orderedChildren(page.table.Spec, "row_detail")[0]
		if stringValue(rowDetail["presentation"]) == "panel" {
			b.WriteString(" detailPanel={slots.detailPanel}")
			if width, valid := integerValue(rowDetail["panel_width"]); valid {
				fmt.Fprintf(&b, " detailPanelWidth={%d}", width)
			}
		} else {
			b.WriteString(" rowDetail={slots.rowDetail}")
		}
	}
	if dialog := rowTableDialog(page.dialogs); dialog != nil {
		fmt.Fprintf(&b, " rowDetailAction={(row) => <Button label=%s onClick={() => %s} size=\"sm\" variant=\"secondary\" />}",
			strconv.Quote(defaultString(stringValue(dialog.dialog.Spec["title"]), "Edit")), reactDialogOpenExpression(*dialog, "row"))
	}
	if dialog := primaryTableDialog(page.dialogs); dialog != nil {
		fmt.Fprintf(&b, " emptyAction={<Button label=%s", strconv.Quote(stringValue(dialog.action.Spec["label"])))
		if icon := stringValue(dialog.action.Spec["icon"]); icon != "" {
			fmt.Fprintf(&b, " icon={<Icon icon=%s size=\"sm\" />}", strconv.Quote(icon))
		}
		fmt.Fprintf(&b, " onClick={() => %s} size=\"sm\" variant=\"primary\" />}", reactDialogOpenExpression(*dialog, ""))
	}
	if children := orderedChildren(page.table.Spec, "export"); len(children) > 0 {
		fileName := defaultString(stringValue(children[0]["file_name"]), page.table.Name+".csv")
		fmt.Fprintf(&b, " exportAction={{ fileName: %s, label: %s", strconv.Quote(fileName), strconv.Quote(defaultString(stringValue(children[0]["label"]), "Export")))
		if icon := stringValue(children[0]["icon"]); icon != "" {
			fmt.Fprintf(&b, ", icon: <Icon icon=%s size=\"sm\" />", strconv.Quote(icon))
		}
		b.WriteString(" }}")
	}
	pageSize, _ := integerValue(page.table.Spec["page_size"])
	if !page.paginated {
		b.WriteString(" paginated={false}")
	}
	if page.table.Spec["hide_header"] == true {
		b.WriteString(" hideHeader")
	}
	fmt.Fprintf(&b, " pageSize={%d} queryKey={queryKey} load={load} /></Page>\n", pageSize)
	for _, dialog := range page.dialogs {
		writeReactTableDialogJSX(&b, dialog, resources)
	}
	b.WriteString("</>;\n}\n")
	return b.String(), nil
}

func writeReactTableStatsState(b *strings.Builder, page reactTablePage, bindings []Resource) {
	stats := page.stats
	if stats == nil {
		return
	}
	method := reactOperationClientMethod(stats.operation, stats.binding, bindings)
	resultType := goName(stats.record.Name)
	b.WriteString("  const statsQuery = useQuery({\n")
	b.WriteString("    queryKey: statsQueryKey,\n")
	b.WriteString("    queryFn: async () => {\n")
	fmt.Fprintf(b, "      const outcome = await client.%s({});\n", method)
	fmt.Fprintf(b, "      if (outcome.kind === \"result\") return { kind: \"result\", value: outcome.value } as const satisfies { readonly kind: \"result\"; readonly value: %s };\n", resultType)
	b.WriteString("      return { kind: \"error\", name: outcome.name, problem: outcome.problem } as const;\n")
	b.WriteString("    },\n")
	b.WriteString("  });\n")
	fmt.Fprintf(b, "  const statsState = requestStateFromQuery<{ readonly value: %s }>(statsQuery);\n", resultType)
}

func writeReactTableDialogState(b *strings.Builder, page reactTablePage, dialog reactTableDialog, bindings []Resource, resources map[string]Resource, rowType string) {
	name := goName(dialog.dialog.Name)
	fields := reactDialogFields(dialog)
	fmt.Fprintf(b, "  const [is%sOpen, set%sOpen] = useState(false);\n", name, name)
	fmt.Fprintf(b, "  const [%sProblem, set%sProblem] = useState<{ readonly code: string; readonly message: string; readonly path?: string }>();\n", tsName(dialog.dialog.Name), name)
	fmt.Fprintf(b, "  const [%sValues, set%sValues] = useState({\n", tsName(dialog.dialog.Name), name)
	for _, field := range fields {
		fmt.Fprintf(b, "    %s: %s,\n", tsName(stringValue(field["name"])), strconv.Quote(reactDialogInitialValue(dialog, field, resources)))
	}
	b.WriteString("  });\n")
	if dialog.seedFromRow {
		fmt.Fprintf(b, "  const open%s = (row?: %s) => {\n", name, rowType)
		fmt.Fprintf(b, "    set%sValues({\n", name)
		for _, field := range fields {
			fieldName := stringValue(field["name"])
			fmt.Fprintf(b, "      %s: row?.%s ?? %s,\n", tsName(fieldName), tsName(fieldName), strconv.Quote(reactDialogInitialValue(dialog, field, resources)))
		}
		b.WriteString("    });\n")
		fmt.Fprintf(b, "    set%sProblem(undefined);\n", name)
		fmt.Fprintf(b, "    set%sOpen(true);\n", name)
		b.WriteString("  };\n")
	}
	method := reactOperationClientMethod(dialog.operation, dialog.binding, bindings)
	fmt.Fprintf(b, "  const %sMutation = useMutation({\n", tsName(dialog.dialog.Name))
	b.WriteString("    mutationFn: async () => {\n")
	fmt.Fprintf(b, "      const outcome = await client.%s({\n", method)
	for _, field := range fields {
		fieldName := stringValue(field["name"])
		fmt.Fprintf(b, "        %s: %s,\n", tsName(fieldName), reactDialogSubmitValue(dialog, field, tsName(dialog.dialog.Name)+"Values."+tsName(fieldName), resources))
	}
	b.WriteString("      });\n")
	b.WriteString("      if (outcome.kind === \"result\") return outcome.value;\n")
	fmt.Fprintf(b, "      set%sProblem(outcome.problem);\n", name)
	b.WriteString("      throw outcome.problem;\n")
	b.WriteString("    },\n")
	b.WriteString("    onError: (error) => {\n")
	b.WriteString("      const failure = error as { readonly code?: unknown; readonly message?: unknown; readonly path?: unknown };\n")
	fmt.Fprintf(b, "      set%sProblem({\n", name)
	b.WriteString("        code: typeof failure.code === \"string\" ? failure.code : \"request_failed\",\n")
	b.WriteString("        message: typeof failure.message === \"string\" ? failure.message : \"Request failed.\",\n")
	b.WriteString("        path: typeof failure.path === \"string\" ? failure.path : undefined,\n")
	b.WriteString("      });\n")
	b.WriteString("    },\n")
	b.WriteString("    onSuccess: async () => {\n")
	b.WriteString("      await Promise.all([\n")
	b.WriteString("        queryClient.invalidateQueries({ queryKey }),\n")
	if page.stats != nil {
		b.WriteString("        queryClient.invalidateQueries({ queryKey: statsQueryKey }),\n")
	}
	b.WriteString("      ]);\n")
	fmt.Fprintf(b, "      set%sProblem(undefined);\n", name)
	fmt.Fprintf(b, "      set%sOpen(false);\n", name)
	b.WriteString("    },\n")
	b.WriteString("  });\n")
}

func writeReactTableDialogJSX(b *strings.Builder, dialog reactTableDialog, resources map[string]Resource) {
	name := goName(dialog.dialog.Name)
	valueName := tsName(dialog.dialog.Name) + "Values"
	mutationName := tsName(dialog.dialog.Name) + "Mutation"
	fmt.Fprintf(b, "{is%sOpen ? <FormDialog title=%s", name, jsxStringExpression(stringValue(dialog.dialog.Spec["title"])))
	if description := stringValue(dialog.dialog.Spec["description"]); description != "" {
		fmt.Fprintf(b, " subtitle=%s", jsxStringExpression(description))
	}
	fmt.Fprintf(b, " onOpenChange={set%sOpen} onSubmit={() => %s.mutate()} footer={<>\n", name, mutationName)
	fmt.Fprintf(b, "  <Button label=\"Cancel\" onClick={() => set%sOpen(false)} variant=\"secondary\" />\n", name)
	fmt.Fprintf(b, "  <Button isLoading={%s.isPending} label=%s type=\"submit\" variant=\"primary\" />\n", mutationName, strconv.Quote(defaultString(stringValue(dialog.dialog.Spec["submit_label"]), defaultString(stringValue(dialog.action.Spec["label"]), stringValue(dialog.dialog.Spec["title"])))))
	b.WriteString("</>}>\n")
	for _, field := range reactDialogFields(dialog) {
		fieldName := stringValue(field["name"])
		label := defaultString(stringValue(field["label"]), humanLabel(fieldName))
		control := defaultString(stringValue(field["control"]), "auto")
		expression := unwrapReactType(typeExpression(field["type"]))
		statusMap := resolveReferencedStatusMap(resources, dialog.dialog, field["status_map"])
		enumValues := enumWireValues(resources, dialog.input.Module, field["type"])
		if control == "select" || control == "auto" && (len(enumValues) > 0 || statusMap.Address != "") {
			options := reactFilterOptions(enumValues, statusMap)
			fmt.Fprintf(b, "  <SelectField label=%s onChange={(event) => set%sValues((values) => ({ ...values, %s: event.target.value }))} options={[%s]} value={%s.%s} />\n",
				strconv.Quote(label), name, tsName(fieldName), options, valueName, tsName(fieldName))
		} else if control == "textarea" {
			fmt.Fprintf(b, "  <TextAreaField label=%s onChange={(event) => set%sValues((values) => ({ ...values, %s: event.target.value }))} placeholder=%s value={%s.%s} />\n",
				strconv.Quote(label), name, tsName(fieldName), strconv.Quote(stringValue(field["placeholder"])), valueName, tsName(fieldName))
		} else {
			inputType := "text"
			if expression == "date" {
				inputType = "date"
			}
			fmt.Fprintf(b, "  <TextField label=%s onChange={(event) => set%sValues((values) => ({ ...values, %s: event.target.value }))} placeholder=%s type=%s value={%s.%s} />\n",
				strconv.Quote(label), name, tsName(fieldName), strconv.Quote(stringValue(field["placeholder"])), strconv.Quote(inputType), valueName, tsName(fieldName))
		}
	}
	fmt.Fprintf(b, "  <FormProblem problem={%sProblem} />\n", tsName(dialog.dialog.Name))
	b.WriteString("</FormDialog> : null}\n")
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
		for key, value := range overrides[stringValue(original["name"])] {
			field[key] = value
		}
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

func referencedReactStatusMaps(resources map[string]Resource, page reactTablePage) []string {
	set := map[string]bool{}
	add := func(owner Resource, value any) {
		if statusMap := resolveReferencedStatusMap(resources, owner, value); statusMap.Address != "" {
			set[reactStatusMapName(statusMap)] = true
		}
	}
	for _, kind := range []string{"column", "filter"} {
		for _, child := range orderedChildren(page.table.Spec, kind) {
			add(page.table, child["status_map"])
		}
	}
	for _, dialog := range page.dialogs {
		for _, field := range orderedChildren(dialog.dialog.Spec, "field") {
			add(dialog.dialog, field["status_map"])
		}
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
		fieldType := field["type"]
		if !page.paginated {
			fieldType = shape.Fields[name].Type
		}
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
	if page.paginated {
		return `{ kind: "result", items: outcome.value.items, nextCursor: outcome.value.nextCursor }`
	}
	return fmt.Sprintf(`{ kind: "result", items: outcome.value.%s }`, tsName(page.itemsField))
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
