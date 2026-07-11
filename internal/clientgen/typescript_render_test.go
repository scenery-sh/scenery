package clientgen

import (
	"strings"
	"testing"

	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
)

func TestTypeScriptClientWithMetaIncludesResponseMetadata(t *testing.T) {
	t.Parallel()

	service := &model.Service{Name: "tasks"}
	endpoint := &model.Endpoint{
		Service: service,
		Name:    "Create",
		Access:  runtimeapi.Auth,
		Path:    "/tasks",
		Methods: []string{"POST"},
	}
	service.Endpoints = []*model.Endpoint{endpoint}

	out, err := GenerateTypeScript(&model.App{
		Name:     "pulse",
		Services: []*model.Service{service},
	}, TypeScriptOptions{AppSlug: "pulse"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"export interface APIResponse<T> {\n    data: T\n    headers: Headers\n    status: number\n    response: Response\n}",
		"interface TypedEndpointResultWithMeta {\n    body: unknown\n    headers: Headers\n    status: number\n    response: Response\n}",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing response metadata %q\n%s", want, got)
		}
	}
}

func TestTypeScriptClientIncludesStorageHelpers(t *testing.T) {
	t.Parallel()

	out, err := GenerateTypeScript(&model.App{Name: "pulse"}, TypeScriptOptions{AppSlug: "pulse"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)
	for _, want := range []string{
		`public readonly storage: StorageClient`,
		`this.storage = new StorageClient(base)`,
		`export interface StorageObject`,
		`export interface StorageListPage`,
		`export class StorageClient`,
		`public store(name = "app"): StorageStoreClient`,
		`public async put(store: string, key: string, body: BodyInit, options?: StoragePutOptions, params?: CallParameters): Promise<StorageObject>`,
		`return await this.store(store).getBlob(key, options, params)`,
		`export class StorageStoreClient`,
		`return "/__scenery/storage/" + encodeURIComponent(this.name)`,
		`return this.storePath() + "/" + encodePathWildcard(key)`,
		`this.baseClient.callAPI("DELETE", this.objectPath(prefix), undefined, mergeCallParameters(params, { query: { recursive: "true" } }))`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q\n%s", want, got)
		}
	}
}

func TestTypeScriptClientGatesStandardAuthGoogleMethods(t *testing.T) {
	t.Parallel()

	out, err := GenerateTypeScript(&model.App{Name: "pulse"}, TypeScriptOptions{
		AppSlug:      "pulse",
		StandardAuth: true,
	})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	googleMethodFragments := []string{
		"public async DisconnectGoogleConnection",
		"public async GetGoogleConnection",
		"public async GoogleCallback",
		"public async GoogleConnectCallback",
		"public async GoogleConnectStart",
		"public async GoogleStart",
		"/auth/google/connection",
		"/auth/google/connect/start",
		"/auth/google/start",
	}
	for _, fragment := range googleMethodFragments {
		if strings.Contains(string(out), fragment) {
			t.Fatalf("disabled Google OAuth method %q leaked into generated client:\n%s", fragment, out)
		}
	}

	out, err = GenerateTypeScript(&model.App{Name: "pulse"}, TypeScriptOptions{
		AppSlug:            "pulse",
		StandardAuth:       true,
		StandardAuthGoogle: true,
	})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)
	for _, want := range append(googleMethodFragments, "GoogleConnectionResponse") {
		if !strings.Contains(got, want) {
			t.Fatalf("enabled Google OAuth client missing %q:\n%s", want, got)
		}
	}
}
