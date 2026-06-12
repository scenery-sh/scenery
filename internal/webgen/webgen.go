package webgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

type Bundle struct {
	Frontend     string
	FrontendRoot string
	GeneratedDir string
	Files        []File
}

type File struct {
	Path     string
	Contents string
}

type componentFile struct {
	AbsPath string
}

var tsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func Build(appRoot string, appModel *model.App, frontends map[string]appcfg.FrontendConfig) ([]Bundle, error) {
	if appModel == nil || len(appModel.Views) == 0 || len(appModel.Entities) == 0 || len(frontends) == 0 {
		return nil, nil
	}
	entities := entitiesByName(appModel.Entities)
	views := collectionViews(appModel.Views)
	if len(views) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(frontends))
	for name := range frontends {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var bundles []Bundle
	for _, name := range names {
		rootRel := frontendRootRel(name, frontends[name])
		rootAbs := rootRel
		if !filepath.IsAbs(rootAbs) {
			rootAbs = filepath.Join(appRoot, filepath.FromSlash(rootRel))
		}
		rootAbs = filepath.Clean(rootAbs)
		generatedDir := filepath.ToSlash(filepath.Join(".scenery", "gen", "web", name))
		files, err := renderBundle(appRoot, name, rootAbs, generatedDir, entities, views)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, Bundle{
			Frontend:     name,
			FrontendRoot: filepath.ToSlash(rootRel),
			GeneratedDir: generatedDir,
			Files:        files,
		})
	}
	return bundles, nil
}

func renderBundle(appRoot, frontendName, frontendRootAbs, generatedDir string, entities map[string]*model.Entity, views []*model.View) ([]File, error) {
	slots, err := resolveSlotComponents(frontendRootAbs, views)
	if err != nil {
		return nil, fmt.Errorf("generate web frontend %q: %w", frontendName, err)
	}
	files := []File{
		{Path: filepath.ToSlash(filepath.Join(generatedDir, "package.json")), Contents: renderPackageJSON(frontendName)},
		{Path: filepath.ToSlash(filepath.Join(generatedDir, "models.ts")), Contents: renderModels(entities)},
		{Path: filepath.ToSlash(filepath.Join(generatedDir, "shapes.ts")), Contents: renderShapes(entities)},
		{Path: filepath.ToSlash(filepath.Join(generatedDir, "collections.ts")), Contents: renderCollections(entities, views)},
		{Path: filepath.ToSlash(filepath.Join(generatedDir, "runtime.ts")), Contents: renderRuntime(entities, views)},
	}
	routes, err := renderRoutes(appRoot, generatedDir, entities, views, slots)
	if err != nil {
		return nil, err
	}
	files = append(files,
		File{Path: filepath.ToSlash(filepath.Join(generatedDir, "routes.tsx")), Contents: routes},
		File{Path: filepath.ToSlash(filepath.Join(generatedDir, "index.ts")), Contents: renderIndex()},
	)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func entitiesByName(entities []*model.Entity) map[string]*model.Entity {
	out := make(map[string]*model.Entity, len(entities))
	for _, entity := range entities {
		if entity != nil && entity.Name != "" {
			out[entity.Name] = entity
		}
	}
	return out
}

func collectionViews(views []*model.View) []*model.View {
	out := make([]*model.View, 0, len(views))
	for _, view := range views {
		if view != nil && view.Kind == "collection" {
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package.RelDir != out[j].Package.RelDir {
			return out[i].Package.RelDir < out[j].Package.RelDir
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func frontendRootRel(name string, frontend appcfg.FrontendConfig) string {
	root := strings.TrimSpace(frontend.Root)
	if root == "" {
		root = filepath.ToSlash(filepath.Join("apps", name))
	}
	return filepath.ToSlash(root)
}

func resolveSlotComponents(frontendRoot string, views []*model.View) (map[string]componentFile, error) {
	needed := map[string]bool{}
	for _, view := range views {
		for _, slot := range view.Slots {
			if strings.TrimSpace(slot.Name) != "" {
				needed[slot.Name] = true
			}
		}
	}
	resolved := map[string]componentFile{}
	if len(needed) == 0 {
		return resolved, nil
	}
	err := filepath.WalkDir(frontendRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".scenery", "node_modules", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		base := d.Name()
		for name := range needed {
			if base == name+".ts" || base == name+".tsx" {
				resolved[name] = componentFile{AbsPath: path}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var missing []string
	for name := range needed {
		if _, ok := resolved[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing page slot component(s) under %s: %s", filepath.ToSlash(frontendRoot), strings.Join(missing, ", "))
	}
	return resolved, nil
}

func renderPackageJSON(frontendName string) string {
	return "{\n" +
		"  \"name\": \"@scenery/generated-" + safePackageSegment(frontendName) + "\",\n" +
		"  \"private\": true,\n" +
		"  \"type\": \"module\",\n" +
		"  \"exports\": {\n" +
		"    \".\": \"./index.ts\",\n" +
		"    \"./models\": \"./models.ts\",\n" +
		"    \"./shapes\": \"./shapes.ts\",\n" +
		"    \"./collections\": \"./collections.ts\",\n" +
		"    \"./runtime\": \"./runtime.ts\",\n" +
		"    \"./routes\": \"./routes.tsx\"\n" +
		"  }\n" +
		"}\n"
}

func renderModels(entities map[string]*model.Entity) string {
	var b strings.Builder
	writeHeader(&b)
	names := sortedEntityNames(entities)
	for _, name := range names {
		entity := entities[name]
		for _, field := range storedFields(entity) {
			if len(field.EnumValues) == 0 {
				continue
			}
			fmt.Fprintf(&b, "export type %s = %s\n\n", enumTypeName(entity, field), tsUnion(field.EnumValues))
		}
		fmt.Fprintf(&b, "export interface %sRow {\n", entity.Name)
		for _, field := range storedFields(entity) {
			fmt.Fprintf(&b, "  %s: %s\n", tsProperty(field.Column), tsType(entity, field))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "export interface %sCreate {\n", entity.Name)
		for _, field := range createFields(entity) {
			fmt.Fprintf(&b, "  %s: %s\n", tsProperty(field.Column), tsType(entity, field))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "export interface %sPatch {\n", entity.Name)
		for _, field := range patchFields(entity) {
			fmt.Fprintf(&b, "  %s?: %s\n", tsProperty(field.Column), tsType(entity, field))
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func renderShapes(entities map[string]*model.Entity) string {
	var b strings.Builder
	writeHeader(&b)
	names := sortedEntityNames(entities)
	if len(names) > 0 {
		imports := make([]string, 0, len(names))
		for _, name := range names {
			imports = append(imports, name+"Row")
		}
		fmt.Fprintf(&b, "import type { %s } from \"./models\"\n\n", strings.Join(imports, ", "))
	}
	b.WriteString("export interface ElectricShapeDefinition<Row> {\n")
	b.WriteString("  table: string\n")
	b.WriteString("  primaryKey: keyof Row & string\n")
	b.WriteString("  columns: readonly (keyof Row & string)[]\n")
	b.WriteString("  url: (baseURL: string) => string\n")
	b.WriteString("}\n\n")
	b.WriteString("function shapeURL(baseURL: string, table: string): string {\n")
	b.WriteString("  const base = baseURL.replace(/\\/+$/, \"\")\n")
	b.WriteString("  return `${base}/v1/shape?table=${encodeURIComponent(table)}`\n")
	b.WriteString("}\n\n")
	for _, name := range names {
		entity := entities[name]
		fmt.Fprintf(&b, "export const %sShape = {\n", lowerFirst(entity.Name))
		fmt.Fprintf(&b, "  table: %s,\n", strconv.Quote(entity.Table))
		fmt.Fprintf(&b, "  primaryKey: %s,\n", strconv.Quote(idColumn(entity)))
		fmt.Fprintf(&b, "  columns: [%s],\n", quotedList(fieldColumns(storedFields(entity))))
		fmt.Fprintf(&b, "  url: (baseURL: string) => shapeURL(baseURL, %s),\n", strconv.Quote(entity.Table))
		fmt.Fprintf(&b, "} as const satisfies ElectricShapeDefinition<%sRow>\n\n", entity.Name)
	}
	b.WriteString("export const electricShapes = {\n")
	for _, name := range names {
		entity := entities[name]
		fmt.Fprintf(&b, "  %s: %sShape,\n", lowerFirst(entity.Name), lowerFirst(entity.Name))
	}
	b.WriteString("} as const\n")
	return b.String()
}

func renderCollections(entities map[string]*model.Entity, views []*model.View) string {
	var b strings.Builder
	writeHeader(&b)
	importedRows := map[string]bool{}
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			importedRows[entity.Name+"Row"] = true
		}
	}
	if len(importedRows) > 0 {
		fmt.Fprintf(&b, "import type { %s } from \"./models\"\n", strings.Join(sortedKeys(importedRows), ", "))
	}
	b.WriteString("import type { ElectricShapeDefinition } from \"./shapes\"\n")
	shapeNames := map[string]bool{}
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			shapeNames[lowerFirst(entity.Name)+"Shape"] = true
		}
	}
	if len(shapeNames) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./shapes\"\n", strings.Join(sortedKeys(shapeNames), ", "))
	}
	b.WriteString("\n")
	b.WriteString("export interface CollectionColumn<Row> {\n")
	b.WriteString("  source: string\n")
	b.WriteString("  field: keyof Row & string\n")
	b.WriteString("  label: string\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface TanStackDBCollectionDefinition<Row> {\n")
	b.WriteString("  id: string\n")
	b.WriteString("  entity: string\n")
	b.WriteString("  route: string\n")
	b.WriteString("  title: string\n")
	b.WriteString("  shape: ElectricShapeDefinition<Row>\n")
	b.WriteString("  columns: readonly CollectionColumn<Row>[]\n")
	b.WriteString("  getKey: (row: Row) => string\n")
	b.WriteString("  materialize: (rows: Iterable<Row>) => Row[]\n")
	b.WriteString("}\n\n")
	b.WriteString("export function materializeRows<Row>(rows: Iterable<Row>): Row[] {\n")
	b.WriteString("  return Array.from(rows)\n")
	b.WriteString("}\n\n")
	for _, view := range views {
		entity := entities[view.Entity]
		if entity == nil {
			continue
		}
		rowType := entity.Name + "Row"
		fmt.Fprintf(&b, "export const %sColumns = [\n", lowerFirst(view.Name))
		for _, column := range view.Columns {
			field, ok := entityFieldByName(entity, column)
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "  { source: %s, field: %s, label: %s },\n", strconv.Quote(field.Name), strconv.Quote(field.Column), strconv.Quote(field.Name))
		}
		fmt.Fprintf(&b, "] as const satisfies readonly CollectionColumn<%s>[]\n\n", rowType)
		fmt.Fprintf(&b, "export const %sCollection = {\n", lowerFirst(view.Name))
		fmt.Fprintf(&b, "  id: %s,\n", strconv.Quote(view.Name))
		fmt.Fprintf(&b, "  entity: %s,\n", strconv.Quote(entity.Name))
		fmt.Fprintf(&b, "  route: %s,\n", strconv.Quote(view.Route))
		fmt.Fprintf(&b, "  title: %s,\n", strconv.Quote(view.Title))
		fmt.Fprintf(&b, "  shape: %sShape,\n", lowerFirst(entity.Name))
		fmt.Fprintf(&b, "  columns: %sColumns,\n", lowerFirst(view.Name))
		fmt.Fprintf(&b, "  getKey: (row: %s) => String(row[%s]),\n", rowType, strconv.Quote(idColumn(entity)))
		b.WriteString("  materialize: materializeRows,\n")
		fmt.Fprintf(&b, "} as const satisfies TanStackDBCollectionDefinition<%s>\n\n", rowType)
	}
	b.WriteString("export const collections = [\n")
	for _, view := range views {
		if entities[view.Entity] != nil {
			fmt.Fprintf(&b, "  %sCollection,\n", lowerFirst(view.Name))
		}
	}
	b.WriteString("] as const\n")
	return b.String()
}

func renderRuntime(entities map[string]*model.Entity, views []*model.View) string {
	var b strings.Builder
	writeHeader(&b)
	importedRows := map[string]bool{}
	importedCollections := map[string]bool{}
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			importedRows[entity.Name+"Row"] = true
			importedCollections[lowerFirst(view.Name)+"Collection"] = true
		}
	}
	if len(importedRows) > 0 {
		fmt.Fprintf(&b, "import type { %s } from \"./models\"\n", strings.Join(sortedKeys(importedRows), ", "))
	}
	if len(importedCollections) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./collections\"\n", strings.Join(sortedKeys(importedCollections), ", "))
	}
	b.WriteString("import type { TanStackDBCollectionDefinition } from \"./collections\"\n\n")
	b.WriteString("export type RuntimeRows<Row> = Iterable<Row> | readonly Row[] | (() => Iterable<Row> | readonly Row[])\n\n")
	b.WriteString("export interface ElectricRuntimeConfig {\n")
	b.WriteString("  baseURL: string\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface CollectionRuntime<Row> {\n")
	b.WriteString("  id: string\n")
	b.WriteString("  entity: string\n")
	b.WriteString("  route: string\n")
	b.WriteString("  title: string\n")
	b.WriteString("  shapeURL: string\n")
	b.WriteString("  definition: TanStackDBCollectionDefinition<Row>\n")
	b.WriteString("  rows: () => readonly Row[]\n")
	b.WriteString("  materialize: () => Row[]\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface GeneratedRuntimeRowSources {\n")
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			fmt.Fprintf(&b, "  %s?: RuntimeRows<%sRow>\n", lowerFirst(view.Name), entity.Name)
		}
	}
	b.WriteString("}\n\n")
	b.WriteString("export interface GeneratedRuntimeOptions {\n")
	b.WriteString("  electric: ElectricRuntimeConfig\n")
	b.WriteString("  rows?: GeneratedRuntimeRowSources\n")
	b.WriteString("}\n\n")
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			fmt.Fprintf(&b, "export type %sRuntime = CollectionRuntime<%sRow>\n", view.Name, entity.Name)
		}
	}
	if len(views) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("function resolveRows<Row>(source: RuntimeRows<Row> | undefined): readonly Row[] {\n")
	b.WriteString("  const value = typeof source === \"function\" ? source() : source\n")
	b.WriteString("  return value ? Array.from(value) : []\n")
	b.WriteString("}\n\n")
	for _, view := range views {
		entity := entities[view.Entity]
		if entity == nil {
			continue
		}
		fmt.Fprintf(&b, "export function create%sRuntime(options: GeneratedRuntimeOptions): %sRuntime {\n", view.Name, view.Name)
		fmt.Fprintf(&b, "  const definition = %sCollection\n", lowerFirst(view.Name))
		fmt.Fprintf(&b, "  const rows = () => resolveRows(options.rows?.%s)\n", lowerFirst(view.Name))
		b.WriteString("  return {\n")
		b.WriteString("    id: definition.id,\n")
		b.WriteString("    entity: definition.entity,\n")
		b.WriteString("    route: definition.route,\n")
		b.WriteString("    title: definition.title,\n")
		b.WriteString("    shapeURL: definition.shape.url(options.electric.baseURL),\n")
		b.WriteString("    definition,\n")
		b.WriteString("    rows,\n")
		b.WriteString("    materialize: () => definition.materialize(rows()),\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
	}
	b.WriteString("export interface GeneratedRuntime {\n")
	b.WriteString("  collections: {\n")
	for _, view := range views {
		if entities[view.Entity] != nil {
			fmt.Fprintf(&b, "    %s: %sRuntime\n", lowerFirst(view.Name), view.Name)
		}
	}
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("export function createGeneratedRuntime(options: GeneratedRuntimeOptions): GeneratedRuntime {\n")
	b.WriteString("  return {\n")
	b.WriteString("    collections: {\n")
	for _, view := range views {
		if entities[view.Entity] != nil {
			fmt.Fprintf(&b, "      %s: create%sRuntime(options),\n", lowerFirst(view.Name), view.Name)
		}
	}
	b.WriteString("    },\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

func renderRoutes(appRoot, generatedDir string, entities map[string]*model.Entity, views []*model.View, slots map[string]componentFile) (string, error) {
	var b strings.Builder
	writeHeader(&b)
	routeAbs := filepath.Join(appRoot, filepath.FromSlash(generatedDir), "routes.tsx")
	importedRows := map[string]bool{}
	importedCollections := map[string]bool{}
	for _, view := range views {
		if entity := entities[view.Entity]; entity != nil {
			importedRows[entity.Name+"Row"] = true
			importedCollections[lowerFirst(view.Name)+"Collection"] = true
		}
	}
	if len(importedRows) > 0 {
		fmt.Fprintf(&b, "import type { %s } from \"./models\"\n", strings.Join(sortedKeys(importedRows), ", "))
	}
	if len(importedCollections) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./collections\"\n", strings.Join(sortedKeys(importedCollections), ", "))
	}
	b.WriteString("import type { CollectionPageRoute, ComponentSlot } from \"@scenery/layout-kit\"\n")
	b.WriteString("import { createCollectionPage } from \"@scenery/layout-kit\"\n")
	b.WriteString("import type { GeneratedRuntime } from \"./runtime\"\n")
	slotNames := sortedComponentNames(slots)
	for _, name := range slotNames {
		importPath, err := relativeTSImport(routeAbs, slots[name].AbsPath)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "import { %s as %sSlot } from %s\n", name, name, strconv.Quote(importPath))
	}
	b.WriteString("\n")
	for _, view := range views {
		entity := entities[view.Entity]
		if entity == nil {
			continue
		}
		rowType := entity.Name + "Row"
		slotType := "Record<string, never>"
		if len(view.Slots) > 0 {
			var names []string
			for _, slot := range view.Slots {
				names = append(names, slot.Name)
			}
			sort.Strings(names)
			fmt.Fprintf(&b, "const %sSlots = {\n", lowerFirst(view.Name))
			for _, name := range names {
				fmt.Fprintf(&b, "  %s: %sSlot,\n", tsProperty(name), name)
			}
			fmt.Fprintf(&b, "} satisfies Record<%s, ComponentSlot<%s>>\n\n", tsUnion(names), rowType)
			slotType = "typeof " + lowerFirst(view.Name) + "Slots"
		}
		fmt.Fprintf(&b, "export function %sPage(props: { rows?: readonly %s[]; runtime?: GeneratedRuntime[\"collections\"][%s] } = {}) {\n", view.Name, rowType, strconv.Quote(lowerFirst(view.Name)))
		fmt.Fprintf(&b, "  return createCollectionPage<%s, %s>({\n", rowType, slotType)
		fmt.Fprintf(&b, "    collection: %sCollection,\n", lowerFirst(view.Name))
		b.WriteString("    rows: props.runtime?.rows() ?? props.rows ?? [],\n")
		if len(view.Slots) > 0 {
			fmt.Fprintf(&b, "    slots: %sSlots,\n", lowerFirst(view.Name))
		} else {
			b.WriteString("    slots: {},\n")
		}
		b.WriteString("  })\n")
		b.WriteString("}\n\n")
	}
	b.WriteString("export function createGeneratedRoutes(runtime?: GeneratedRuntime): readonly CollectionPageRoute<any>[] {\n")
	b.WriteString("  return [\n")
	for _, view := range views {
		if entities[view.Entity] == nil {
			continue
		}
		fmt.Fprintf(&b, "    { id: %s, kind: \"collection\", path: %s, title: %s, entity: %s, collection: %s, component: (props) => %sPage({ ...props, runtime: runtime?.collections.%s }), generated: true },\n", strconv.Quote(view.Name), strconv.Quote(view.Route), strconv.Quote(view.Title), strconv.Quote(view.Entity), strconv.Quote(view.Name), view.Name, lowerFirst(view.Name))
	}
	b.WriteString("  ] as const satisfies readonly CollectionPageRoute<any>[]\n")
	b.WriteString("}\n\n")
	b.WriteString("export function registerGeneratedRoutes(register: (route: CollectionPageRoute<any>) => void, runtime?: GeneratedRuntime): void {\n")
	b.WriteString("  for (const route of createGeneratedRoutes(runtime)) {\n")
	b.WriteString("    register(route)\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("export const generatedRoutes = createGeneratedRoutes()\n")
	return b.String(), nil
}

func renderIndex() string {
	return `// Code generated by scenery generate data; DO NOT EDIT.

export * from "./models"
export * from "./shapes"
export * from "./collections"
export * from "./runtime"
export * from "./routes"
`
}

func writeHeader(b *strings.Builder) {
	b.WriteString("// Code generated by scenery generate data; DO NOT EDIT.\n\n")
	b.WriteString("/* eslint-disable */\n\n")
}

func storedFields(entity *model.Entity) []model.EntityField {
	if entity == nil {
		return nil
	}
	out := make([]model.EntityField, 0, len(entity.Fields))
	for _, field := range entity.Fields {
		if field.Kind != model.EntityFieldComputed {
			out = append(out, field)
		}
	}
	return out
}

func patchFields(entity *model.Entity) []model.EntityField {
	var out []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range storedFields(entity) {
		if strings.EqualFold(field.Name, "id") {
			continue
		}
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		out = append(out, field)
	}
	return out
}

func createFields(entity *model.Entity) []model.EntityField {
	var out []model.EntityField
	tenantField := entity.TenantField()
	for _, field := range storedFields(entity) {
		if tenantField != nil && strings.EqualFold(field.Name, tenantField.Name) {
			continue
		}
		out = append(out, field)
	}
	return out
}

func sortedEntityNames(entities map[string]*model.Entity) []string {
	names := make([]string, 0, len(entities))
	for name := range entities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func fieldColumns(fields []model.EntityField) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Column)
	}
	return out
}

func entityFieldByName(entity *model.Entity, name string) (model.EntityField, bool) {
	for _, field := range entity.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return model.EntityField{}, false
}

func tsType(entity *model.Entity, field model.EntityField) string {
	if len(field.EnumValues) > 0 {
		return enumTypeName(entity, field)
	}
	switch normalizeTypeExpr(field.TypeExpr) {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return "number"
	case "time.Time":
		return "string"
	default:
		return "unknown"
	}
}

func normalizeTypeExpr(expr string) string {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "*")
	return expr
}

func enumTypeName(entity *model.Entity, field model.EntityField) string {
	return entity.Name + field.Name
}

func tsUnion(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Quote(value))
	}
	return strings.Join(parts, " | ")
}

func tsProperty(name string) string {
	if tsIdent.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return strings.Join(quoted, ", ")
}

func idColumn(entity *model.Entity) string {
	for _, field := range storedFields(entity) {
		if strings.EqualFold(field.Name, "id") {
			return field.Column
		}
	}
	return "id"
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedComponentNames(values map[string]componentFile) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func relativeTSImport(fromFileAbs, targetAbs string) (string, error) {
	rel, err := filepath.Rel(filepath.Dir(fromFileAbs), targetAbs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	rel = strings.TrimSuffix(strings.TrimSuffix(rel, ".tsx"), ".ts")
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel, nil
}

func safePackageSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "web"
	}
	return out
}
