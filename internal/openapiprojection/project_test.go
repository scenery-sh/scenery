package openapiprojection_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/openapiprojection"
)

func compileFixtureApp(t *testing.T, rel string) *compiler.Result {
	t.Helper()
	root := filepath.Join("..", "..", filepath.FromSlash(rel))
	res, err := compiler.Compile(root)
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	if !res.Valid() {
		t.Fatalf("fixture %s invalid: %+v", rel, res.Diagnostics)
	}
	return res
}

func compileTempApp(t *testing.T, files map[string]string) *compiler.Result {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := compiler.Compile(root)
	if err != nil {
		t.Fatalf("compile temp app: %v", err)
	}
	if !res.Valid() {
		t.Fatalf("temp app invalid: %+v", res.Diagnostics)
	}
	return res
}

func assertNeutral(t *testing.T, label string, data []byte) {
	t.Helper()
	if strings.Contains(strings.ToLower(string(data)), "scenery") {
		t.Fatalf("%s leaked framework branding ('scenery') into OpenAPI output:\n%s", label, string(data))
	}
}

func TestProjectCommittedFixturesNeutralAndDeterministic(t *testing.T) {
	t.Parallel()
	fixtures := []string{
		"internal/compiler/testdata/house",
		"internal/compiler/testdata/native",
		"examples/webhook-inbox",
	}
	for _, rel := range fixtures {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			t.Parallel()
			compiled := compileFixtureApp(t, rel)
			docsA, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{})
			if err != nil {
				t.Fatalf("Project first run: %v", err)
			}
			docsB, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{})
			if err != nil {
				t.Fatalf("Project second run: %v", err)
			}
			if len(docsA) == 0 {
				t.Fatalf("expected at least one HTTP gateway document for %s", rel)
			}
			for i := range docsA {
				jsonA, err := openapiprojection.EncodeJSON(docsA[i].Document)
				if err != nil {
					t.Fatal(err)
				}
				jsonB, err := openapiprojection.EncodeJSON(docsB[i].Document)
				if err != nil {
					t.Fatal(err)
				}
				if string(jsonA) != string(jsonB) {
					t.Fatalf("JSON output is non-deterministic for %s", docsA[i].GatewayAddress)
				}
				assertNeutral(t, rel+" JSON", jsonA)

				yamlA, err := openapiprojection.EncodeYAML(docsA[i].Document)
				if err != nil {
					t.Fatal(err)
				}
				yamlB, err := openapiprojection.EncodeYAML(docsB[i].Document)
				if err != nil {
					t.Fatal(err)
				}
				if string(yamlA) != string(yamlB) {
					t.Fatalf("YAML output is non-deterministic for %s", docsA[i].GatewayAddress)
				}
				assertNeutral(t, rel+" YAML", yamlA)

				var fromJSON, fromYAML map[string]any
				if err := json.Unmarshal(jsonA, &fromJSON); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if err := yaml.Unmarshal(yamlA, &fromYAML); err != nil {
					t.Fatalf("invalid YAML: %v", err)
				}
				if fromJSON["openapi"] != "3.1.1" || fromYAML["openapi"] != "3.1.1" {
					t.Fatalf("expected openapi 3.1.1, got json=%v yaml=%v", fromJSON["openapi"], fromYAML["openapi"])
				}
				if fromJSON["jsonSchemaDialect"] != openapiprojection.JSONSchemaDialect {
					t.Fatalf("unexpected jsonSchemaDialect: %v", fromJSON["jsonSchemaDialect"])
				}
				if docsA[i].OpenAPIRevision != compiled.OpenAPIRevisions[docsA[i].GatewayAddress] {
					t.Fatalf("openapi revision mismatch: got %q, want %q", docsA[i].OpenAPIRevision, compiled.OpenAPIRevisions[docsA[i].GatewayAddress])
				}
			}
		})
	}
}

func TestProjectComprehensiveHTTPFeatures(t *testing.T) {
	t.Parallel()
	compiled := compileTempApp(t, map[string]string{
		"app.scn": `
workspace {}

application "orders_demo" {}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/api"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

authentication "session" {
  provider = std.provider.standard_auth
  scheme   = "session"
}

module "orders" {
  source = "./orders"
  inputs = {
    gateway = http_gateway.public_api
    auth    = authentication.session
  }
}
`,
		"orders/package.scn": `
package "orders" {
  go_contract {
    import_path = "example.test/orders-demo/orders"
  }
}

input "gateway" {
  type = resource_ref("http_gateway")
}

input "auth" {
  type = resource_ref("authentication")
}

service "orders" {
  runtime = "go"
  implementation {
    constructor = "NewService"
  }
}

enum "status" {
  value "pending" {}
  value "shipped" { wire_value = "SHIPPED" }
}

record "tree_node" {
  field "id" {
    type        = uuid
    description = "Unique node identifier."
  }
  field "amount" { type = decimal }
  field "count" { type = int64 }
  field "small" {
    type    = int32
    minimum = 1
    maximum = 100
  }
  field "note" { type = optional(nullable(string)) }
  field "tags" { type = set(string) }
  field "children" { type = optional(list(record.tree_node)) }
}

record "event_created" {
  field "order_id" { type = uuid }
}

record "event_done" {
  field "total" { type = decimal }
}

union "order_event" {
  discriminator   = "kind"
  open            = true
  unknown_variant = "preserve"
  variant "created" { type = record.event_created }
  variant "done" { type = record.event_done }
}

record "create_input" {
  field "org_id" { type = string }
  field "tenant_from_ctx" { type = string }
  field "filter" { type = optional(list(enum.status)) }
  field "meta" { type = optional(record.event_created) }
  field "trace_hdr" { type = optional(string) }
  field "session_hint" { type = optional(string) }
  field "status" { type = enum.status }
  field "tree" { type = record.tree_node }
}

record "create_output" {
  field "id" { type = uuid }
  field "etag" { type = string }
  field "session_cookie" { type = optional(string) }
}

operation "create_order" {
  service = service.orders
  input   = record.create_input
  handler { method = "CreateOrder" }
  result "created" { type = record.create_output }
  error "conflict" { type = std.type.problem }
}

execution "create_order_direct" {
  operation = operation.create_order
  mode      = "direct"
  timeout   = "30s"
}

binding "create_order_http" {
  operation      = operation.create_order
  execution      = execution.create_order_direct
  gateway        = var.gateway
  protocol       = "http"
  delivery       = "call"
  authentication = var.auth
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "POST"
    path          = "/orgs/{org_id}/orders"
    codec_profile = std.codec.http_json_v1
    path_parameter "org_id" { to = operation.create_order.input.org_id }
    context "tenant" {
      from = principal.tenant_id
      to   = operation.create_order.input.tenant_from_ctx
    }
    query_parameter "filter" {
      to       = operation.create_order.input.filter
      encoding = "comma"
    }
    query_parameter "meta" {
      to       = operation.create_order.input.meta
      encoding = "json"
    }
    header "x-trace-id" { to = operation.create_order.input.trace_hdr }
    cookie "hint" { to = operation.create_order.input.session_hint }
    body {
      codec   = "json"
      to      = operation.create_order.input
      include = [operation.create_order.input.status, operation.create_order.input.tree]
    }
    response "created" {
      when   = result.created
      status = 201
      body {
        codec = "json"
        from  = result.created.id
      }
      header "etag" { from = result.created.etag }
      cookie "order_session" { from = result.created.session_cookie }
    }
    response "conflict" {
      when   = error.conflict
      status = 409
      body {
        codec = "problem_json"
        from  = error.conflict
      }
    }
  }
}

record "upload_input" {
  field "title" { type = string }
  field "attachment" { type = bytes }
}

operation "upload_receipt" {
  service = service.orders
  input   = record.upload_input
  handler { method = "UploadReceipt" }
  result "ok" { type = std.type.unit }
}

execution "upload_receipt_direct" {
  operation = operation.upload_receipt
  mode      = "direct"
  timeout   = "30s"
}

binding "upload_receipt_http" {
  operation      = operation.upload_receipt
  execution      = execution.upload_receipt_direct
  gateway        = var.gateway
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "POST"
    path          = "/receipts"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "multipart"
      to    = operation.upload_receipt.input
      part "title" {
        kind = "text"
        to   = operation.upload_receipt.input.title
      }
      part "file" {
        kind        = "file"
        to          = operation.upload_receipt.input.attachment
        media_types = ["application/pdf", "image/png"]
      }
    }
    response "ok" {
      when   = result.ok
      status = 204
    }
  }
}

record "stream_input" {
  field "channel" { type = string }
}

operation "watch_orders" {
  service = service.orders
  input   = record.stream_input
  handler { method = "WatchOrders" }
  result "event" { type = union.order_event }
}

execution "watch_orders_direct" {
  operation = operation.watch_orders
  mode      = "direct"
  timeout   = "5m"
}

binding "watch_orders_http" {
  operation      = operation.watch_orders
  execution      = execution.watch_orders_direct
  gateway        = var.gateway
  protocol       = "http"
  delivery       = "stream_out"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "GET"
    path          = "/events/{channel...}"
    codec_profile = std.codec.http_json_v1
    path_tail "channel" { to = operation.watch_orders.input.channel }
    response "event" {
      when   = result.event
      status = 200
      body {
        codec = "sse"
        from  = result.event
      }
    }
  }
}
`,
	})

	docs, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	rawJSON, err := openapiprojection.EncodeJSON(docs[0].Document)
	if err != nil {
		t.Fatal(err)
	}
	assertNeutral(t, "comprehensive JSON", rawJSON)

	var doc map[string]any
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	secSchemes := components["securitySchemes"].(map[string]any)

	// 1. Security schemes use neutral names and app_access cookie.
	if secSchemes["bearerAuth"] == nil || secSchemes["sessionCookie"] == nil {
		t.Fatalf("expected bearerAuth and sessionCookie security schemes, got %+v", secSchemes)
	}
	cookieScheme := secSchemes["sessionCookie"].(map[string]any)
	if cookieScheme["name"] != "app_access" {
		t.Fatalf("sessionCookie name = %v, want app_access", cookieScheme["name"])
	}

	// 2. POST /api/orgs/{org_id}/orders: context mapping excluded, query comma/json preserved, include filter applied, headers + Set-Cookie emitted.
	createPath := paths["/api/orgs/{org_id}/orders"].(map[string]any)
	createOp := createPath["post"].(map[string]any)
	if createOp["operationId"] != "createOrder" {
		t.Fatalf("operationId = %v, want createOrder", createOp["operationId"])
	}
	if _, hasSecurity := createOp["security"]; !hasSecurity {
		t.Fatalf("expected security requirement on authenticated operation")
	}
	params := createOp["parameters"].([]any)
	paramByName := map[string]map[string]any{}
	for _, raw := range params {
		p := raw.(map[string]any)
		paramByName[p["name"].(string)] = p
	}
	if _, leaked := paramByName["tenant"]; leaked {
		t.Fatalf("server-populated context mapping must not be exposed as an OpenAPI parameter")
	}
	if _, leaked := paramByName["tenant_from_ctx"]; leaked {
		t.Fatalf("server-populated context field must not be exposed as an OpenAPI parameter")
	}
	if filterParam := paramByName["filter"]; filterParam["style"] != "form" || filterParam["explode"] != false {
		t.Fatalf("comma query param mismatch: %+v", filterParam)
	}
	if metaParam := paramByName["meta"]; metaParam["content"] == nil {
		t.Fatalf("json query param should use content[application/json]: %+v", metaParam)
	}
	reqBody := createOp["requestBody"].(map[string]any)
	reqSchema := reqBody["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	reqProps := reqSchema["properties"].(map[string]any)
	if len(reqProps) != 2 || reqProps["status"] == nil || reqProps["tree"] == nil {
		t.Fatalf("body include filter failed: properties = %+v", reqProps)
	}
	responses := createOp["responses"].(map[string]any)
	resp201 := responses["201"].(map[string]any)
	resp201Headers := resp201["headers"].(map[string]any)
	if resp201Headers["etag"] == nil || resp201Headers["Set-Cookie"] == nil {
		t.Fatalf("expected etag and Set-Cookie headers on 201, got %+v", resp201Headers)
	}
	for _, code := range []string{"400", "401", "403", "406", "409", "415", "429", "500"} {
		if responses[code] == nil {
			t.Fatalf("missing expected response %s in %+v", code, responses)
		}
	}

	// 3. Recursive record + lossless scalar encodings + closed enum wire values.
	treeNode := schemas["TreeNode"].(map[string]any)
	treeProps := treeNode["properties"].(map[string]any)
	if countProp := treeProps["count"].(map[string]any); countProp["type"] != "string" || countProp["format"] != "int64" {
		t.Fatalf("int64 schema mismatch: %+v", countProp)
	}
	if amountProp := treeProps["amount"].(map[string]any); amountProp["type"] != "string" || amountProp["format"] != "decimal" {
		t.Fatalf("decimal schema mismatch: %+v", amountProp)
	}
	if smallProp := treeProps["small"].(map[string]any); smallProp["type"] != "integer" || smallProp["minimum"] != float64(1) || smallProp["maximum"] != float64(100) {
		t.Fatalf("int32 constraints mismatch: %+v", smallProp)
	}
	if noteProp := treeProps["note"].(map[string]any); !reflect.DeepEqual(noteProp["type"], []any{"string", "null"}) {
		t.Fatalf("nullable string mismatch: %+v", noteProp)
	}
	childrenProp := treeProps["children"].(map[string]any)
	if childrenProp["items"].(map[string]any)["$ref"] != "#/components/schemas/TreeNode" {
		t.Fatalf("recursive record ref mismatch: %+v", childrenProp)
	}
	statusEnum := schemas["Status"].(map[string]any)
	if !reflect.DeepEqual(statusEnum["enum"], []any{"pending", "SHIPPED"}) {
		t.Fatalf("enum wire values mismatch: %+v", statusEnum)
	}

	// 4. Multipart upload + 204 No Content without body.
	uploadOp := paths["/api/receipts"].(map[string]any)["post"].(map[string]any)
	if _, hasSec := uploadOp["security"]; hasSec {
		t.Fatalf("unauthenticated operation should not declare security")
	}
	mpMedia := uploadOp["requestBody"].(map[string]any)["content"].(map[string]any)["multipart/form-data"].(map[string]any)
	mpEncoding := mpMedia["encoding"].(map[string]any)
	if mpEncoding["file"].(map[string]any)["contentType"] != "application/pdf, image/png" {
		t.Fatalf("multipart encoding mismatch: %+v", mpEncoding)
	}
	resp204 := uploadOp["responses"].(map[string]any)["204"].(map[string]any)
	if resp204["content"] != nil {
		t.Fatalf("204 response should not emit content: %+v", resp204)
	}

	// 5. SSE stream_out + path_tail template rewrite + open tagged union with discriminator.
	ssePath, ok := paths["/api/events/{channel}"].(map[string]any)
	if !ok {
		t.Fatalf("expected path_tail template /api/events/{channel}, got paths %+v", paths)
	}
	sseOp := ssePath["get"].(map[string]any)
	sseContent := sseOp["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	sseMedia := sseContent["text/event-stream"].(map[string]any)
	if sseMedia["schema"].(map[string]any)["$ref"] != "#/components/schemas/OrderEvent" {
		t.Fatalf("SSE event schema mismatch: %+v", sseMedia)
	}
	orderEvent := schemas["OrderEvent"].(map[string]any)
	if orderEvent["discriminator"].(map[string]any)["propertyName"] != "kind" {
		t.Fatalf("union discriminator mismatch: %+v", orderEvent)
	}
	if len(orderEvent["oneOf"].([]any)) != 3 {
		t.Fatalf("expected 3 oneOf branches for open union, got %+v", orderEvent["oneOf"])
	}
}

func TestProjectGatewaySelectionAndErrors(t *testing.T) {
	t.Parallel()
	compiled := compileFixtureApp(t, "internal/compiler/testdata/house")
	docs, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{
		Gateway: "public_api",
	})
	if err != nil {
		t.Fatalf("select public_api: %v", err)
	}
	if len(docs) != 1 || docs[0].GatewayName != "public_api" {
		t.Fatalf("unexpected gateway selection: %+v", docs)
	}

	if _, err := openapiprojection.Project(compiled.Manifest, nil, nil, nil, openapiprojection.Options{Gateway: "missing_gw"}); err == nil || !strings.Contains(err.Error(), `http gateway "missing_gw" not found`) {
		t.Fatalf("expected not found error for missing_gw, got %v", err)
	}
}

func TestProjectStreamEnqueueAndFrameworkBindings(t *testing.T) {
	t.Parallel()
	// 1. Verify durable enqueue delivery + ExecutionReceipt on examples/webhook-inbox.
	inbox := compileFixtureApp(t, "examples/webhook-inbox")
	inboxDocs, err := openapiprojection.Project(inbox.Manifest, inbox.FrameworkResources, inbox.HTTPSurfaceRevisions, inbox.OpenAPIRevisions, openapiprojection.Options{})
	if err != nil {
		t.Fatalf("Project webhook-inbox: %v", err)
	}
	inboxSchemas := inboxDocs[0].Document["components"].(map[string]any)["schemas"].(map[string]any)
	if inboxSchemas["ExecutionReceipt"] == nil {
		t.Fatalf("expected ExecutionReceipt schema in webhook-inbox OpenAPI components, got %+v", inboxSchemas)
	}
	foundEnqueue := false
	for _, rawPathItem := range inboxDocs[0].Document["paths"].(map[string]any) {
		for _, rawOp := range rawPathItem.(map[string]any) {
			responses := rawOp.(map[string]any)["responses"].(map[string]any)
			if resp202, ok := responses["202"].(map[string]any); ok {
				schema := resp202["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
				if schema["$ref"] == "#/components/schemas/ExecutionReceipt" {
					foundEnqueue = true
				}
			}
		}
	}
	if !foundEnqueue {
		t.Fatalf("expected 202 ExecutionReceipt response in webhook-inbox OpenAPI paths")
	}

	// 2. Verify binary stream delivery and --include-framework (typed + opaque callback).
	compiled := compileTempApp(t, map[string]string{
		".scenery.json": `{"name":"jobs_demo","envs":{"local":{"default":true}},"auth":{"enabled":true,"google_oauth":{"enabled":false},"connections":[{"id":"google","preset":"google"}]}}`,
		"app.scn": `
workspace {}

application "jobs_demo" {}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

authentication "session" {
  provider = std.provider.standard_auth
  scheme   = "session"
}

module "jobs" {
  source = "./jobs"
  inputs = {
    gateway = http_gateway.public_api
  }
}
`,
		"jobs/package.scn": `
package "jobs" {
  go_contract {
    import_path = "example.test/jobs-demo/jobs"
  }
}

input "gateway" {
  type = resource_ref("http_gateway")
}

service "jobs" {
  runtime = "go"
  implementation {
    constructor = "NewService"
  }
}

record "export_input" {
  field "report_id" { type = string }
}

operation "download_report" {
  service = service.jobs
  input   = record.export_input
  handler { method = "DownloadReport" }
  result "ok" { type = bytes }
}

execution "download_report_direct" {
  operation = operation.download_report
  mode      = "direct"
  timeout   = "30s"
}

binding "download_report_http" {
  operation      = operation.download_report
  execution      = execution.download_report_direct
  gateway        = var.gateway
  protocol       = "http"
  delivery       = "stream"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "GET"
    path          = "/reports/{report_id}"
    codec_profile = std.codec.http_json_v1
    path_parameter "report_id" { to = operation.download_report.input.report_id }
    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec                = "bytes"
        from                 = result.ok
        produced_media_types = ["application/pdf"]
      }
    }
  }
}
`,
	})

	// Without IncludeFramework: only authored bindings appear.
	authored, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{})
	if err != nil {
		t.Fatalf("Project authored: %v", err)
	}
	authoredPaths := authored[0].Document["paths"].(map[string]any)
	if authoredPaths["/auth/connections/{provider}/start"] != nil {
		t.Fatalf("framework routes should be excluded unless IncludeFramework is set")
	}

	// Verify stream (bytes) projection.
	streamResp := authoredPaths["/reports/{report_id}"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)
	pdfSchema := streamResp["content"].(map[string]any)["application/pdf"].(map[string]any)["schema"].(map[string]any)
	if pdfSchema["type"] != "string" || pdfSchema["contentMediaType"] != "application/pdf" {
		t.Fatalf("stream bytes schema mismatch: %+v", pdfSchema)
	}

	// With IncludeFramework: standard-auth connection bindings (typed + opaque callback) are projected cleanly and stay neutral.
	withFramework, err := openapiprojection.Project(compiled.Manifest, compiled.FrameworkResources, compiled.HTTPSurfaceRevisions, compiled.OpenAPIRevisions, openapiprojection.Options{
		IncludeFramework: true,
	})
	if err != nil {
		t.Fatalf("Project with framework: %v", err)
	}
	encoded, err := openapiprojection.EncodeJSON(withFramework[0].Document)
	if err != nil {
		t.Fatal(err)
	}
	assertNeutral(t, "with-framework JSON", encoded)
	fwPaths := withFramework[0].Document["paths"].(map[string]any)
	if fwPaths["/auth/connections/{provider}/start"] == nil || fwPaths["/auth/connections/google/callback"] == nil {
		t.Fatalf("expected framework auth connection & opaque callback routes, got %+v", fwPaths)
	}
}
