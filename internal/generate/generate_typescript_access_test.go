package generate

import (
	"strings"
	"testing"
)

func TestRenderReactRoutesCarriesAccessMetadataAndMatcher(t *testing.T) {
	page := Resource{Address: "app/content_page/projects", Module: "app", Name: "projects", Kind: "scenery.content-page", Spec: map[string]any{
		"path": "/projects/{project_id}", "application_key": " MicroGRID ", "access_key": "projects.Read",
	}}
	source := renderReactRoutes(&Result{Manifest: &Manifest{}}, []reactRoutePage{{resource: page, component: "ProjectsPage", params: []reactRouteParam{{routeName: "project_id", propName: "project_id"}}}})
	for _, fragment := range []string{
		`export type SceneryAccessMetadata`,
		`applicationKey: " MicroGRID "`,
		`accessKey: "projects.Read"`,
		`export function matchSceneryRoute(`,
		`segment.startsWith("$")`,
		`path.split(/[?#]/, 1)`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated routes missing %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactAccessAdapterDefinesSingularResolverContract(t *testing.T) {
	source := renderReactAccessAdapter()
	for _, fragment := range []string{
		`export type SceneryAccessTarget =`,
		`readonly kind: "workspace-tab"`,
		`export type SceneryAccessResult =`,
		`export function resolveSceneryWorkspaceAccess`,
		`export function SceneryRouteAccessBoundary`,
		`route.component as ComponentType<{ readonly params?: SceneryRouteParams }>`,
		`result.status === "allowed"`,
		`context.accessPending ?? null`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated access adapter missing %q:\n%s", fragment, source)
		}
	}
}

func TestRenderReactAppAdapterGatesRoutesBeforeComponentInvocation(t *testing.T) {
	source := renderReactAppAdapter()
	for _, fragment := range []string{
		`readonly resolveAccess?: SceneryAccessResolver`,
		`readonly navigationFilter?: (`,
		`route: SceneryRouteDescriptor`,
		`currentRoute: SceneryRouteDescriptor | undefined`,
		`<SceneryRoutedAccessBoundary route={route} />`,
		`<SceneryRouteAccessBoundary`,
		`return { router, App: SceneryApp, routes }`,
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("generated app adapter missing %q:\n%s", fragment, source)
		}
	}
	for _, forbidden := range []string{
		`navigationFilter?: (routePath: string`,
		`contentGroup?: (path: string`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("generated app adapter retains old callback %q:\n%s", forbidden, source)
		}
	}
}
