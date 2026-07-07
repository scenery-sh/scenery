package clientgen

import (
	"strings"
	"testing"

	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
)

func TestTypeScriptClientIncludesTxidSyncObservationDiagnostics(t *testing.T) {
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
		`const CLIENT_APP_SLUG = "pulse"`,
		`export type Txid = number`,
		`txid: Txid | null`,
		`export class SyncObservationError extends Error`,
		`public readonly kind = "sync_observation_failure"`,
		`public readonly mutationCommitted = true`,
		`mutation_committed: true`,
		`export function txidFromHeaders(headers: Headers): Txid | null`,
		`export async function observeAPIResponseTxid<T>(response: APIResponse<T>, observer: TxidObserver, context?: SyncObservationContext): Promise<APIResponse<T>>`,
		`sync observation failed after committed API mutation`,
		`api_url?: string`,
		`sync_url?: string`,
		`sync_stream_id?: string`,
		`context.syncURL = apiURL.replace("://api.", "://sync.")`,
		`txid: txidFromHeaders(response.headers)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q\n%s", want, got)
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
	if strings.Contains(string(out), "GoogleStart") || strings.Contains(string(out), "/auth/google/start") {
		t.Fatalf("disabled Google OAuth methods leaked into generated client:\n%s", out)
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
	for _, want := range []string{"GoogleStart", "/auth/google/start", "GoogleCallback", "/auth/google/callback"} {
		if !strings.Contains(got, want) {
			t.Fatalf("enabled Google OAuth client missing %q:\n%s", want, got)
		}
	}
}
