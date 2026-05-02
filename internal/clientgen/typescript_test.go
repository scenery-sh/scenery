package clientgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "onlava.com/internal/app"
	"onlava.com/internal/parse"
)

func TestGenerateTypeScriptIncludesStructuredRequestHandling(t *testing.T) {
	appRoot := filepath.Join(appcfg.RepoRoot(), "testdata", "apps", "basic")
	model, err := parse.App(appRoot, "basicapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	out, err := GenerateTypeScript(model, TypeScriptOptions{AppSlug: "basicapp"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`export namespace service {`,
		`public async Echo(name: string, params: EchoRequest, options?: CallParameters): Promise<EchoResponse> {`,
		`public async EchoWithMeta(name: string, params: EchoRequest, options?: CallParameters): Promise<APIResponse<EchoResponse>> {`,
		`this.EchoWithMeta = this.EchoWithMeta.bind(this)`,
		`title: encodeQueryValue(params.Title),`,
		`"X-Echo": encodeHeaderValue(params.Header),`,
		`body: encodeQueryValue(params.body),`,
		`transport?: OnlavaTransport`,
		`export type CallParameters = Omit<RequestInit, "method" | "body" | "headers"> & {`,
		`export interface APIResponse<T> {`,
		`export type OnlavaTransport = "auto" | "json" | "binary" | "binary-strict" | "wire-json" | "wire-json-strict"`,
		`const ONLAVA_WIRE_SCHEMA_HASH = `,
		`const resp = await this.baseClient.callTypedEndpoint({ endpointID: "service.Echo"`,
		`const resp = await this.baseClient.callTypedEndpointWithMeta({ endpointID: "service.Echo"`,
		`wirePath: "/_wire/service.Echo"`,
		"path: `/echo/${encodeURIComponent(String(name))}`",
		`payload: params`,
		`payloadJSON: JSON.stringify(params)`,
		`jsonBody: undefined`,
		`params: mergeCallParameters(options, { query, headers })`,
		`return await decodeTypedAPIResponse(resp) as APIResponse<EchoResponse>`,
		`public async Raw(rest: string, method: string, body?: RequestInit["body"], options?: CallParameters): Promise<globalThis.Response> {`,
		"return await this.baseClient.callAPI(method, `/raw/${encodePathWildcard(String(rest))}`, body, options)",
		`export interface EchoResponse {`,
		`message: string`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"protobuf", "grpc", "connect"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("generated client should not expose %q\n%s", forbidden, got)
		}
	}
}

func TestGenerateTypeScriptIncludesNamedAliases(t *testing.T) {
	appRoot := t.TempDir()
	writeFile := func(rel, data string) {
		path := filepath.Join(appRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/clientapp\n\ngo 1.26.0\n\nrequire onlava.com v0.0.0\n\nreplace onlava.com => "+appcfg.RepoRoot()+"\n")
	writeFile(".onlava.json", `{"name":"clientapp"}`)
	writeFile("point/point.go", `package point

type Point3 struct {
	X int `+"`json:\"x\"`"+`
	Y int `+"`json:\"y\"`"+`
	Z int `+"`json:\"z\"`"+`
}
`)
	writeFile("maps/api.go", `package maps

import (
	"context"

	"example.com/clientapp/point"
)

type TaskStatus string

type Response struct {
	Status TaskStatus `+"`json:\"status\"`"+`
	Point  point.Point3 `+"`json:\"point\"`"+`
}

//onlava:api public
func Get(ctx context.Context) (*Response, error) {
	return &Response{}, nil
}
`)

	model, err := parse.App(appRoot, "clientapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	out, err := GenerateTypeScript(model, TypeScriptOptions{AppSlug: "clientapp"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`export type TaskStatus = string`,
		`status: TaskStatus`,
		`export namespace point {`,
		`export interface Point3 {`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q", want)
		}
	}
}
