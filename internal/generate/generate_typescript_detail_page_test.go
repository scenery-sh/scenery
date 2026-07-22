package generate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/tscheck"
)

func TestRenderReactDetailPageUsesTypedParamsSharedContentAndRelatedTable(t *testing.T) {
	root := filepath.Join("..", "compiler", "testdata", "house")
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile house fixture: %v diagnostics=%#v", err, result.Diagnostics)
	}
	var target Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Address == "app/typescript_client/public_api" {
			target = resource
			break
		}
	}
	target.Spec = cloneMapValue(target.Spec)
	target.Spec["react"] = map[string]any{"tsconfig": "tsconfig.json"}
	resources := append([]Resource(nil), result.Manifest.Resources...)
	resources = append(resources, result.FrameworkResources...)
	bindings := publicHTTPBindings(resources, target)
	tablePages := selectedReactTablePages(result.Manifest.Resources, bindings)
	details := selectedReactDetailPages(result.Manifest.Resources, bindings, tablePages)
	if len(details) != 1 {
		t.Fatalf("detail pages = %#v", details)
	}
	source, err := renderReactDetailPage(result, target, filepath.Join(result.Root, "clients", "generated", "public_api", "react"), details[0], bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"export type SceneDetailParams",
		"readonly sceneId: string",
		"export function SceneDetailContent",
		"client.readScene({",
		"id: params.sceneId",
		`const detailQueryKeyPrefix = ["scenery", "detail_page", "house/detail_page/scene_detail"] as const`,
		"queryClient.invalidateQueries({ queryKey: detailQueryKey })",
		`<DetailSection title={"Overview"}>`,
		`<DetailField label={"Scene ID"}>{detailValue(state.data.id)}</DetailField>`,
		`{state.data.name !== undefined && state.data.name !== null && String(state.data.name) !== "" ? <DetailField label={"Name"}>{detailValue(state.data.name)}</DetailField> : null}`,
		"<StatusBadge status={String(state.data.status)} map={HouseSceneStatusStatusMap} />",
		"<DetailActions data={state.data} params={params} onMutated={onMutated} onClose={onClose} />",
		`<SceneEventsPage client={client} injectedInput={eventsInput} queryKeySuffix={eventsQueryKeySuffix} />`,
		"await onMutated()",
		"export function SceneDetailPage",
		"export function SceneDetailDialog",
		"readonly sceneId: string",
		"<SceneDetailContent client={client} onClose={onClose} params={{ sceneId }} />",
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated detail page missing %q:\n%s", fragment, source)
		}
	}
	for _, forbidden := range []string{"event.target.value", "as any", "as unknown as"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("generated detail page contains forbidden %q:\n%s", forbidden, source)
		}
	}

	routes := appendReactDetailRoutePages(nil, details)
	routeSource := renderReactRoutes(result, routes)
	for _, fragment := range []string{
		`path: "/house/scenes/$scene_id"`,
		`params: ["scene_id"]`,
		`sceneId: params?.["scene_id"] ?? ""`,
		`origin: "generated"`,
	} {
		if !strings.Contains(routeSource, fragment) {
			t.Errorf("generated detail route missing %q:\n%s", fragment, routeSource)
		}
	}
}

func TestGeneratedDetailPageCompilesWithManagedTypeScriptChecker(t *testing.T) {
	binary := os.Getenv("SCENERY_TSGO_BINARY")
	if binary == "" {
		t.Skip("SCENERY_TSGO_BINARY is not set")
	}
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	appPath := filepath.Join(root, testAppFilename)
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	appSource = []byte(strings.Replace(string(appSource), `  output_root = "clients/generated/public_api"
}`, `  output_root = "clients/generated/public_api"

  react {
    tsconfig = "tsconfig.json"
  }
}`, 1))
	if err := os.WriteFile(appPath, appSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", testPackageFilename)
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = []byte(strings.Replace(string(packageSource), `path         = "/house/scenes/{scene_id}"`, `path         = "/house/scenes/{id}"`, 1))
	packageSource = []byte(strings.Replace(string(packageSource), `
  param "scene_id" {
    input = "id"
  }
`, "\n", 1))
	packageSource = []byte(strings.Replace(string(packageSource), `
  table "events" {
    label = "Events"
    page  = table_page.scene_events
    param = "scene_id"
    input = "scene_id"
  }
`, "\n", 1))
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	tsconfig := []byte(`{"compilerOptions":{"strict":true,"jsx":"react-jsx","module":"NodeNext","moduleResolution":"NodeNext","target":"ES2022","lib":["ES2022","DOM"],"skipLibCheck":true},"include":["clients/generated/public_api/react/**/*.ts","clients/generated/public_api/react/**/*.tsx","house/**/*.tsx"]}`)
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), tsconfig, 0o644); err != nil {
		t.Fatal(err)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	nodeModules := os.Getenv("SCENERY_REACT_NODE_MODULES")
	if nodeModules == "" {
		nodeModules = filepath.Join(repoRoot, "apps", "console", "node_modules")
	}
	if _, err := os.Stat(filepath.Join(nodeModules, "@tanstack", "react-router")); err != nil {
		t.Skip("SCENERY_REACT_NODE_MODULES does not provide @tanstack/react-router")
	}
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile detail fixture: %v diagnostics=%#v", err, result.Diagnostics)
	}
	var target Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Address == "app/typescript_client/public_api" {
			target = resource
			break
		}
	}
	files, err := renderTypeScriptTarget(result, target)
	if err != nil {
		t.Fatal(err)
	}
	detailSource := generatedSourceWithSuffix(files, "/scene_detail.generated.tsx")
	for _, fragment := range []string{"export type SceneDetailParams", "readonly id: string", "id: params.id"} {
		if !strings.Contains(detailSource, fragment) {
			t.Fatalf("implicit-param detail output missing %q:\n%s", fragment, detailSource)
		}
	}
	if strings.Contains(detailSource, "DetailRelated") {
		t.Fatalf("detail output without related tables imports or renders DetailRelated:\n%s", detailSource)
	}
	staged := make([]tscheck.File, 0, len(files))
	for _, file := range files {
		if !file.Remove {
			staged = append(staged, tscheck.File{Path: file.Path, Bytes: file.Bytes})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := tscheck.Check(ctx, binary, root, filepath.Join(root, "clients", "generated", "public_api"), "tsconfig.json", staged); err != nil {
		t.Fatal(err)
	}
}

func TestDetailRelatedSuppressesNestedPageChrome(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "ui", "components", "DetailPage.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, fragment := range []string{
		`import { WorkspaceEmbeddedPageProvider } from "./workspace-context.js"`,
		`<WorkspaceEmbeddedPageProvider actionsHost={null}>`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("DetailRelated missing %q:\n%s", fragment, source)
		}
	}
}
