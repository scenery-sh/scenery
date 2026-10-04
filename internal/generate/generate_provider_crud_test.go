package generate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGeneratedProviderCRUDArtifactsInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)

	rootSourcePath := filepath.Join(root, testAppFilename)
	rootSource, err := os.ReadFile(rootSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http",`, `"scenery.runtime-http",
    "scenery.data",`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `gateway = http_gateway.public_api`, `gateway  = http_gateway.public_api
    database = data_source.house_database`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `  output_root = "clients/generated/public_api"
}`, `  output_root = "clients/generated/public_api"
  react {
    tsconfig = "tsconfig.json"
  }
}`, 1))
	rootSource = append(rootSource, []byte(`

provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
}

data_source "house_database" {
  provider  = provider.postgres
  lifecycle = "external"
  require_capabilities = ["sql.query/v1", "sql.transaction/v1"]
  config = { database = "house" }
}
`)...)
	if err := os.WriteFile(rootSourcePath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(root, "house", testPackageFilename)
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`

input "database" { type = resource_ref("data_source") }

record "scene_row" {
  field "id" { type = uuid }
  field "tenant_id" { type = string }
  field "name" { type = enum.scene_name }
  field "kind" { type = string }
  field "created_at" { type = datetime }
}

enum "scene_name" {
  value "roof" { wire_value = "roof \"quoted\" \\ path" }
  value "wall" {}
}

entity "scene" {
  type        = record.scene_row
  data_source = var.database
  mapping {
    relation = "scenes"
  }
  field "id" {
    column      = "id"
    primary_key = true
    default {
      strategy = "uuid_v7"
    }
  }
  field "tenant_id" {
    column    = "tenant_id"
    tenant_key = true
    immutable = true
  }
  field "name" { column = "name" }
  field "kind" { column = "kind" }
  field "created_at" { column = "created_at" }
}

crud "scene_api" {
  entity         = entity.scene
  implementation = std.crud.entity
  actions        = ["list", "get", "create", "update", "delete"]
  execution {
    mode    = "direct"
    timeout = "15s"
  }
  list {
    filters       = ["name", "created_at", "kind"]
    search        = ["tenant_id"]
    sorts         = ["name"]
    default_sort  = { field = "name", direction = "asc" }
    max_page_size = 25
  }
  http {
    path           = "/scenes"
    codec_profile  = std.codec.http_json_v1
    gateway        = var.gateway
    authentication = std.authentication.none
    authorization  = std.authorization.public
    pipeline       = std.pipeline.empty
  }
}

react_component "scene_name_cell" {
  module = "scene-name-cell.tsx"
  export = "SceneNameCell"
}

react_component "scene_name_filter" {
  module = "scene-name-filter.tsx"
  export = "SceneNameFilter"
}

react_component "scene_date_filter" {
  module = "scene-date-filter.tsx"
  export = "SceneDateFilter"
}

react_component "scene_toolbar" {
  module = "scene-toolbar.tsx"
  export = "SceneToolbar"
}

react_component "scene_detail" {
  module = "scene-detail.tsx"
  export = "SceneDetail"
}

status_map "scene_name" {
  status "roof" {
    label   = "Roof"
    variant = "green"
  }
  status "wall" {
    label   = "Wall"
    variant = "neutral"
  }
}

react_component "scene_summary_content" {
  module = "scene-summary.tsx"
  export = "SceneSummary"
}

react_component "scene_summary_actions" {
  module = "scene-summary.tsx"
  export = "SceneSummaryActions"
}

operation "scene_summary" {
  service = service.house
  input   = std.type.unit

  handler {
    method = "SceneSummary"
  }

  result "success" {
    type = record.scene_row
  }
}

execution "scene_summary_direct" {
  operation = operation.scene_summary
  mode      = "direct"
  timeout   = "15s"
}

binding "scene_summary_http" {
  gateway   = var.gateway
  operation = operation.scene_summary
  execution = execution.scene_summary_direct
  protocol  = "http"
  delivery  = "call"

  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  http {
    method        = "GET"
    path          = "/scene-summary"
    codec_profile = std.codec.http_json_v1

    response "success" {
      when   = result.success
      status = 200

      body {
        codec = "json"
        from  = result.success
      }
    }
  }
}

binding "scene_summary_internal" {
  operation = operation.scene_summary
  execution = execution.scene_summary_direct
  protocol  = "internal"
  delivery  = "call"

  exposure       = "application"
  authentication = std.authentication.inherit
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  internal {
    visibility = "application"
    principal  = "inherit"
  }
}

content_page "scene_summary" {
  path             = "/scene-summary"
  source           = binding.scene_summary_http
  title            = "Scene summary"
  aria_label       = "Scene summary content"
  max_width        = 960
  nav_group        = "UI"
  nav_order        = 30
  nav_label        = "Summary"
  nav_icon         = "report"
  nav_active_paths = ["/scene-summary", "/scene-summary/detail"]

  search "mail" {
    type = string
  }

  search "google_connected" {
    type = string
  }

  search "view" {
    type = enum.scene_name
  }

  content {
    component = react_component.scene_summary_content
  }

  actions {
    component = react_component.scene_summary_actions
  }
}

record "scene_metrics" {
  field "total" { type = int32 }
  field "matching" { type = int32 }
  field "filtered" { type = int32 }
}

operation "scene_metrics" {
  service = service.house
  input   = std.type.unit

  handler { method = "SceneMetrics" }
  result "success" { type = record.scene_metrics }
}

execution "scene_metrics_direct" {
  operation = operation.scene_metrics
  mode      = "direct"
  timeout   = "15s"
}

binding "scene_metrics_http" {
  gateway        = var.gateway
  operation      = operation.scene_metrics
  execution      = execution.scene_metrics_direct
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  http {
    method        = "GET"
    path          = "/scene-metrics"
    codec_profile = std.codec.http_json_v1
    response "success" {
      when   = result.success
      status = 200
      body {
        codec = "json"
        from  = result.success
      }
    }
  }
}

record "scene_quick_create_input" {
  field "name" { type = enum.scene_name }
}

operation "scene_quick_create" {
  service = service.house
  input   = record.scene_quick_create_input

  handler { method = "SceneQuickCreate" }
  result "success" { type = record.scene_row }
}

execution "scene_quick_create_direct" {
  operation = operation.scene_quick_create
  mode      = "direct"
  timeout   = "15s"
}

binding "scene_quick_create_http" {
  gateway        = var.gateway
  operation      = operation.scene_quick_create
  execution      = execution.scene_quick_create_direct
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  http {
    method        = "POST"
    path          = "/scene-quick-create"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "json"
      to    = operation.scene_quick_create.input
    }
    response "success" {
      when   = result.success
      status = 200
      body {
        codec = "json"
        from  = result.success
      }
    }
  }
}

form_dialog "scene_quick_create" {
  source       = binding.scene_quick_create_http
  title        = "Create scene"
  submit_label = "Create"
  field "name" {
    label       = "Name"
    placeholder = "Scene name"
  }
}

table_page "scenes" {
  path        = "/scenes"
  source      = crud.scene_api
  title       = "Scenes \"quoted\" \\ path"
  description = "Description \"quoted\" \\ path"
  column "id" { label = "ID \"quoted\" \\ path" }
  column "name" {
    label      = "Name \"quoted\" \\ path"
    appearance = "badge"
    component  = react_component.scene_name_cell
    status_map = status_map.scene_name
  }
  filter "name" {
    label  = "Filter \"quoted\" \\ path"
    pinned = true
  }
  filter "created_at" {
    label = "Created"
    preset "today" {
      label = "Today"
      range = "today"
    }
    preset "month" {
      label = "Month to date"
      range = "month_to_date"
    }
  }
  predicate "kind" { value = "default" }
  sort "name" {
    label   = "Sort \"quoted\" \\ path"
    default = "asc"
  }
  toolbar {
    component = react_component.scene_toolbar
  }
  row_detail {
    component = react_component.scene_detail
    dialog    = form_dialog.scene_quick_create
  }
  export {
    label     = "Export scenes"
    icon      = "arrowDown"
    file_name = "scenes.csv"
  }
  stats {
    source = binding.scene_metrics_http
    tile "total" {
      label          = "Total"
      appearance     = "money"
      sub            = "matching"
      sub_appearance = "count"
      sub_label      = "scenes"
		icon           = "calendar"
      filter         = "name"
      value          = "wall"
    }
    tile "matching" {
      label  = "All"
      filter = "name"
      clear  = true
    }
    tile "filtered" {
      label  = "Premium"
      filter = "kind"
      value  = "premium"
    }
  }
  action "create" {
    label   = "Create scene"
    icon    = "wrench"
    dialog  = form_dialog.scene_quick_create
    primary = true
  }
  row_link  = "/scenes/{id}"
  page_size = 20
}

table_page "plain_scenes" {
  path      = "/plain-scenes"
  source    = crud.scene_api
  title     = "Plain scenes"
  page_size = 20
  column "name" {}
  column "id" {}
  sort "name" { default = "asc" }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"scene-name-cell.tsx":   `export function SceneNameCell(props: { readonly row: object; readonly value: string }) { return props.value; }`,
		"scene-name-filter.tsx": `export function SceneNameFilter(props: { readonly value?: string; readonly label: string; readonly onChange: (value: string | undefined) => void }) { return props.label; }`,
		"scene-date-filter.tsx": `export function SceneDateFilter(props: { readonly value?: { readonly from?: string; readonly to?: string }; readonly label: string; readonly onChange: (value: { readonly from?: string; readonly to?: string } | undefined) => void }) { return props.label; }`,
		"scene-detail.tsx":      `export function SceneDetail(props: { readonly row: object }) { return JSON.stringify(props.row); }`,
		"scene-summary.tsx":     `export function SceneSummary(props: { readonly state: unknown }) { return String(props.state); } export function SceneSummaryActions(props: { readonly state: unknown }) { return String(props.state); }`,
		"scene-toolbar.tsx":     `export function SceneToolbar() { return "Toolbar"; }`,
	} {
		if err := os.WriteFile(filepath.Join(root, "house", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	servicePath := filepath.Join(root, "house", "service.go")
	serviceSource, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	serviceSource = append(serviceSource, []byte(`

func (service *Service) SceneSummary(_ context.Context, _ housecontract.SceneSummaryInput) (housecontract.SceneSummaryOutcome, error) {
	return housecontract.SceneSummarySuccess{Value: housecontract.SceneRow{}}, nil
}

func (service *Service) SceneMetrics(_ context.Context, _ housecontract.SceneMetricsInput) (housecontract.SceneMetricsOutcome, error) {
	return housecontract.SceneMetricsSuccess{Value: housecontract.SceneMetrics{Total: 1, Matching: 1, Filtered: 1}}, nil
}

func (service *Service) SceneQuickCreate(_ context.Context, _ housecontract.SceneQuickCreateInput) (housecontract.SceneQuickCreateOutcome, error) {
	return housecontract.SceneQuickCreateSuccess{Value: housecontract.SceneRow{}}, nil
}
`)...)
	if err := os.WriteFile(servicePath, serviceSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true,"noUnusedLocals":true,"jsx":"react-jsx","module":"esnext","moduleResolution":"bundler","target":"es2022","lib":["es2022","dom"]},"include":["clients/generated/public_api/react/**/*.ts","clients/generated/public_api/react/**/*.tsx","house/**/*.tsx"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, ok := compiler.BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres provider unavailable")
	}
	lock := fmt.Sprintf("lock {}\nprovider \"postgres\" {\n  source = \"registry.scenery.dev/core/postgres\"\n  integrity = %q\n}\n", integrity)
	if err := os.WriteFile(filepath.Join(root, testAppLockFilename), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(root)
	if err != nil || !compiled.Valid() {
		t.Fatalf("compile generated CRUD list: %v diagnostics=%#v", err, compiled.Diagnostics)
	}
	goFiles, err := generateApplicationArtifacts(compiled, newResourceIndex(compiled.Manifest.Resources), newProjectionInput(compiled))
	if err != nil {
		t.Fatal(err)
	}
	var adapterSource string
	for _, file := range goFiles {
		if bytes.Contains(file.Bytes, []byte("type providerCRUDService struct")) {
			adapterSource = string(file.Bytes)
			break
		}
	}
	if adapterSource == "" {
		t.Fatal("generated provider CRUD adapter is missing")
	}
	for _, fragment := range []string{
		"type providerCRUDService struct",
		"datasource.InvokeCRUD",
		`scenerydb.Get(ctx, "house")`,
		`DefaultDirection: "asc"`,
		`MaxPageSize: 25`,
	} {
		if !strings.Contains(adapterSource, fragment) {
			t.Errorf("generated provider CRUD adapter missing %q:\n%s", fragment, adapterSource)
		}
	}
	var target Resource
	for _, resource := range compiled.Manifest.Resources {
		if resource.Address == "app/typescript_client/public_api" {
			target = resource
			break
		}
	}
	typeScriptFiles, err := renderTypeScriptTarget(compiled, target)
	if err != nil {
		t.Fatal(err)
	}
	secondTypeScriptFiles, err := renderTypeScriptTarget(compiled, target)
	if err != nil {
		t.Fatal(err)
	}
	firstContentPage := generatedSourceWithSuffix(typeScriptFiles, "/scene_summary.generated.tsx")
	secondContentPage := generatedSourceWithSuffix(secondTypeScriptFiles, "/scene_summary.generated.tsx")
	if firstContentPage == "" || firstContentPage != secondContentPage {
		var paths []string
		for _, file := range typeScriptFiles {
			paths = append(paths, filepath.ToSlash(file.Path))
		}
		t.Fatalf("content-page generation is not stable across consecutive renders: first=%q second=%q files=%v", firstContentPage, secondContentPage, paths)
	}
	var listBinding Resource
	for _, resource := range compiled.Manifest.Resources {
		if resource.Address == "house/binding/scene_api_list_http" {
			listBinding = resource
			break
		}
	}
	types := []byte(renderTSTypes(reachableResources(compiled.Manifest.Resources, []Resource{listBinding}), []Resource{listBinding}))
	for _, fragment := range []string{
		`export type SceneApiListSort = "name";`,
		`readonly name?: readonly SceneName[];`,
		`readonly search?: string;`,
		`readonly direction?: SceneApiListDirection;`,
		`readonly nextCursor?: string;`,
	} {
		if !strings.Contains(string(types), fragment) {
			t.Errorf("generated CRUD list TypeScript missing %q:\n%s", fragment, types)
		}
	}
	clientSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/client.ts"))
	for _, method := range []string{"sceneApiCreate", "sceneApiGet", "sceneApiUpdate", "sceneApiDelete"} {
		if strings.Contains(string(clientSource), method) {
			t.Errorf("React table-page projection exported unrelated CRUD method %q", method)
		}
	}
	pageSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/scenes.generated.tsx"))
	for _, fragment := range []string{
		"defineTablePageSlots<SceneRow",
		"actions={<>",
		"<ScenesToolbarSlot context={tableContext} />",
		"client?: PublicApiClient",
		"providedClient ?? defaultClient",
		"client.sceneApiList",
		"client.sceneMetrics",
		"client.sceneQuickCreate",
		"useMutation",
		"onError: (error) =>",
		"const openSceneQuickCreate = (row?: SceneRow)",
		`name: row?.name ?? "roof \"quoted\" \\ path"`,
		"<StatTile label={\"Total\"} value={formatStatValue(statsState.value.total, \"money\")} sub={formatStatValue(statsState.value.matching, \"count\") + \" scenes\"} icon={<Icon icon=\"calendar\" size=\"sm\" />} active={Array.isArray(tableContext?.query.filters[\"name\"])",
		`onClick={() => tableContext?.controls.setFilter("name"`,
		`active={!Array.isArray(tableContext?.query.filters["name"])} onClick={() => tableContext?.controls.clearFilter("name")}`,
		`kind: Array.isArray(query.filters["kind"]) ? query.filters["kind"] : ["default"]`,
		`{ field: "kind", label: "Kind", kind: "enum", options: [{ value: "premium", label: "Premium" },], hidden: true }`,
		`presets: [{ label: "Today", range: "today" },{ label: "Month to date", range: "month_to_date" },]`,
		"<FormDialog title={\"Create scene\"}",
		"queryClient.invalidateQueries({ queryKey: scopedQueryKey })",
		"value is SceneName",
		"Code generated by Scenery",
		`<Page title={"Scenes \"quoted\" \\ path"} fill actions={<>`,
		`<QueryTable<SceneRow> resource={"Scenes \"quoted\" \\ path"} resourceSingular={"Scene Row"}`,
		`queryKey={scopedQueryKey}`,
		`const load = useCallback(async (query: TablePageQuery, signal?: AbortSignal): Promise<TablePageResult<SceneRow>> => {`,
		`} as SceneApiListInput, { signal });`,
		`searchable`,
		`rowDetail={slots.rowDetail}`,
		`rowDetailAction={(row) => <Button label="Create scene" onClick={() => openSceneQuickCreate(row)} size="sm" variant="secondary" />}`,
		`emptyAction={<Button label="Create scene" icon={<Icon icon="wrench" size="sm" />} onClick={() => openSceneQuickCreate()} size="sm" variant="primary" />}`,
		`exportAction={{ fileName: "scenes.csv", label: "Export scenes", icon: <Icon icon="arrowDown" size="sm" /> }}`,
		`const queryKey = ["scenery", "table_page", "house/table_page/scenes"] as const;`,
		`title={"Scenes \"quoted\" \\ path"}`,
		`description={"Description \"quoted\" \\ path"}`,
		`label: "ID \"quoted\" \\ path"`,
		`label: "Name \"quoted\" \\ path"`,
		`label: "Filter \"quoted\" \\ path"`,
		`pinned: true`,
		`label: "Sort \"quoted\" \\ path"`,
		`options: [{ value: "roof \"quoted\" \\ path", label: "Roof \"quoted\" \\ Path" }, { value: "wall", label: "Wall" }]`,
	} {
		if !strings.Contains(string(pageSource), fragment) {
			t.Errorf("generated table page missing %q:\n%s", fragment, pageSource)
		}
	}
	statusMaps := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/status-maps.generated.ts"))
	for _, fragment := range []string{
		"SceneryStatusBadgeVariants",
		"satisfies Partial<Record<BadgeVariant, true>>",
		"export const HouseSceneNameStatusMap: StatusMap",
		`"roof": { label: "Roof", variant: "green" }`,
	} {
		if !strings.Contains(string(statusMaps), fragment) {
			t.Errorf("generated status map missing %q:\n%s", fragment, statusMaps)
		}
	}
	for _, forbidden := range []string{"as any", "as unknown as", "import(", "throw cause", "import { TablePage", "return <TablePage", "ui/pages/"} {
		if strings.Contains(string(pageSource), forbidden) {
			t.Errorf("generated table page contains forbidden %q:\n%s", forbidden, pageSource)
		}
	}
	plainPageSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/plain_scenes.generated.tsx"))
	for _, unusedImport := range []string{"dateTime", "TablePageCellProps", "TablePageFilterProps"} {
		if strings.Contains(string(plainPageSource), unusedImport) {
			t.Errorf("plain generated table page imports unused %q:\n%s", unusedImport, plainPageSource)
		}
	}
	plainSource := string(plainPageSource)
	if nameIndex, idIndex := strings.Index(plainSource, `{ field: "name"`), strings.Index(plainSource, `{ field: "id"`); nameIndex < 0 || idIndex < 0 || nameIndex > idIndex {
		t.Errorf("plain generated table page did not preserve authored column order:\n%s", plainSource)
	}
	if !strings.Contains(plainSource, `baseUrl: url(new URL("/api/", globalThis.location.origin).toString())`) {
		t.Errorf("plain generated table page does not target the browser API route:\n%s", plainSource)
	}
	contentPageSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/scene_summary.generated.tsx"))
	for _, fragment := range []string{
		"defineContentPageSlots<SceneRow>",
		"client.sceneSummary({})",
		`<Page title={"Scene summary"} ariaLabel={"Scene summary content"} maxWidth={960}`,
		"actions={<slots.actions {...slotProps} />}",
		"><slots.content {...slotProps} /></Page>",
	} {
		if !strings.Contains(string(contentPageSource), fragment) {
			t.Errorf("generated content page missing %q:\n%s", fragment, contentPageSource)
		}
	}
	routesSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/routes.generated.ts"))
	for _, fragment := range []string{
		`import type { PublicApiClient } from "../index.js";`,
		`export function createGeneratedRoutes(client?: PublicApiClient)`,
		`component: () => createElement(SceneSummaryPage as ComponentType<{ readonly client?: PublicApiClient }>, { client })`,
		`export type SceneSummarySearch`,
		`mail?: string`,
		`googleConnected?: string`,
		`googleConnected: typeof search["google_connected"] === "string"`,
		`view?: "roof \"quoted\" \\ path" | "wall"`,
		`validateSceneSummarySearch`,
		`navigation: { group: "UI", order: 30, label: "Summary", icon: "report", activePaths: ["/scene-summary", "/scene-summary/detail"], origin: "generated" }`,
		`export type SceneryRouteOrigin = NavigationOrigin;`,
		`export type SceneryAccessMetadata =`,
		`export function matchSceneryRoute(`,
	} {
		if !strings.Contains(string(routesSource), fragment) {
			t.Errorf("generated routes descriptor missing %q:\n%s", fragment, routesSource)
		}
	}
	appSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/app.generated.tsx"))
	for _, fragment := range []string{
		`import type { PublicApiClient } from "../index.js";`,
		`readonly client?: PublicApiClient`,
		`...createGeneratedRoutes(options.client)`,
		"createSceneryApp",
		"ClientAppShell",
		"navigationSections",
		`readonly useNavigationRevision?: () => unknown;`,
		"const navigationRevision = useNavigationRevision();",
		"currentRoute,\n          navigationRevision,",
		"_navigationRevision: unknown,",
		"SceneryRouteAccessBoundary",
		"SceneryAccessProvider",
		`return { router, App: SceneryApp, routes }`,
		`origin: descriptor.origin ?? origin`,
		"<Outlet />",
	} {
		if !strings.Contains(string(appSource), fragment) {
			t.Errorf("generated app adapter missing %q:\n%s", fragment, appSource)
		}
	}
	accessSource := []byte(generatedSourceWithSuffix(typeScriptFiles, "/react/access.generated.tsx"))
	for _, fragment := range []string{
		"export type SceneryAccessTarget",
		"export type SceneryAccessResult",
		"resolveSceneryWorkspaceAccess",
	} {
		if !strings.Contains(string(accessSource), fragment) {
			t.Errorf("generated access adapter missing %q:\n%s", fragment, accessSource)
		}
	}
	descriptorBytes := []byte(generatedSourceWithSuffix(typeScriptFiles, "/scenery.typescript-client-generated.json"))
	if !strings.Contains(string(descriptorBytes), `"react/scenery-ui"`) {
		t.Errorf("generated descriptor has no UI catalog root:\n%s", descriptorBytes)
	}
}
