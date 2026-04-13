Summary
Build a new Pulse-native Go-only local runtime that makes pulse run start a single HTTP server for Pulse services and preserve the most common Encore API behavior, with strict Pulse naming and no compatibility layer for Encore syntax.

Use the existing encore tree only as a reference corpus and fixture source. Do not reuse Encore’s daemon/cloud/infra layers; implement a smaller Pulse-native parser, codegen, and runtime.

Public Surface
CLI: pulse run [--port <n>] [--listen <addr>], default listen 127.0.0.1:4000.
App root marker: pulse.app, JSON, required for pulse run. Phase 1 only needs "name"; no cloud/linking fields.
Source directives:
//pulse:api public|auth|private [raw] [path=/...] [method=GET,POST]
//pulse:service
//pulse:authhandler
Public Go packages:
pulse.dev with Meta() and CurrentRequest()
pulse.dev/beta/auth with UserID() and Data()
pulse.dev/beta/errs with coded errors and HTTP status mapping
Struct tag surface for typed endpoints/auth params:
request decoding: json, header, query, qs, cookie
Pulse tags: pulse:"optional" and pulse:"httpstatus"
Implementation Changes
Create a new Go module rooted at pulse.dev with three main areas:
cmd/pulse for the CLI
internal parser/build pipeline for service discovery, directive parsing, codegen, and app launch
runtime/public packages under pulse.dev/...
App discovery and service model:
pulse run walks upward to pulse.app, then loads the Go module from that root.
Service discovery follows Encore-style rules: a service is defined by Pulse APIs, a Pulse service struct, or a Pulse auth handler; nested services are invalid; service names come from the root package name and must be unique.
Parser and compatibility slice:
Support typed handlers with Encore-style signatures: func(context.Context, [path params...], [payload]) ([resp], error).
Support raw handlers: func(http.ResponseWriter, *http.Request).
Support //pulse:service methods plus optional init<Type>() (*Type, error) service initialization.
Support //pulse:authhandler as either a package function or service method; allow both token-string auth and structured auth params from header/query/qs/cookie tags; allow optional auth-data return struct.
Preserve Encore route defaults:
default path /<service>.<Endpoint>
typed endpoint default methods GET,POST when no payload, POST when payload exists
raw endpoint default method wildcard
Build/codegen strategy:
pulse run parses the app with go/packages/AST, builds an app model, generates a transient build workspace, compiles it, and runs the resulting binary.
Generated files include:
endpoint descriptors and registration
service-struct wrappers
auth-handler registration
internal API-call helpers
a synthetic main
Rewriting is part of phase 1: endpoint-to-endpoint calls are rewritten to generated internal call helpers so same-service and cross-service calls honor routing, auth context, and private access rules instead of bypassing the runtime through direct Go calls.
Runtime behavior:
Start one local HTTP server with separate public and private routing tables.
Mount public and auth endpoints on external HTTP.
Keep private endpoints internal-only and callable via generated in-process service calls.
Do not support service-to-service calls to raw endpoints in phase 1; fail at generation time with a clear error, matching Encore’s limitation.
Decode typed requests from path params, headers, query strings, cookies, and JSON body; encode typed responses as JSON.
Honor pulse:"httpstatus" on response structs.
Run the auth handler for external auth requests, then expose auth state through pulse.dev/beta/auth.
Expose enough request metadata through pulse.CurrentRequest() for migrated common cases, especially raw endpoint path params, method, path, service, endpoint, and payload metadata.
Map pulse.dev/beta/errs codes to HTTP responses and return full structured errors for in-process internal calls.
Test Plan
Port a small Pulse-named fixture set from Encore’s Go e2e/parser coverage and use it as the acceptance suite for phase 1.
Add parser/unit tests for:
directive parsing and validation
service discovery
typed/raw handler signatures
auth-handler signatures
route defaulting and path-param validation
Add golden/codegen tests for:
service-struct wrappers
internal API-call helper generation
auth-handler registration
synthetic main generation
Add integration tests that run pulse run against sample apps and verify:
public typed endpoints on default and custom paths
auth endpoints with string-token and structured auth params
auth.UserID() and auth.Data()
service struct initialization and internal private calls
raw endpoint routing plus pulse.CurrentRequest().PathParams
pulse:"httpstatus" and coded error responses
Gate phase 1 on go test ./... and a lint/format pass for the new Pulse code.
Assumptions and Defaults
Go only in phase 1. TypeScript is out of scope.
Strict Pulse rename only. No encore.app, no encore.dev/..., and no //encore:* support in this milestone.
No infra generation, DB management/migrations, Pub/Sub, cron, middleware, dashboard, cloud features, namespaces, or live-reload/watch mode.
No automatic source migration command in phase 1; migrated apps are expected to adopt Pulse syntax directly.
Single-process local runtime only. No remote service hosting or distributed local orchestration in this phase.
