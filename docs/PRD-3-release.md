My verdict: do not freeze the current feature set as-is. Freeze a smaller, boring, reliable v0. onlava is close to having a strong local-first developer runtime, but right now the app runtime, dev supervisor, dashboard, local HTTPS proxy, DB Studio, Temporal workers, cron, and MCP are interwoven. That is the main production-readiness risk.

I could not run the full Go test suite here because go.mod requires Go 1.26.0 and this container has Go 1.23.2; Go attempted to auto-download 1.26.0, but network/DNS is blocked. So the findings below are from static source audit, not a green test run.

What you should focus on first

1. Freeze a narrower v0 contract

The best first production-ready onlava should be:

Stable v0:
  .onlava.json
  onlava run
  onlava build
  onlava check --json
  onlava inspect ... --json
  onlava logs --jsonl
  onlava test
  onlava gen client
  typed/raw HTTP endpoints
  auth handler
  service struct initialization
  private/internal calls
  secrets from env/.env
  basic traces/logs

Everything else should either be explicitly beta or moved behind onlava dev:

Beta / dev-only:
  dashboard
  DB Studio
  local HTTPS proxy
  trust-store installation
  Temporal worker tooling
  cron UI
  MCP server
  psql helper
The current docs/local-contract.md already tries to freeze a local contract, which is good. But the implementation is larger than the contract and some docs disagree with code. For example, docs/local-contract.md lists the current CLI grammar without onlava psql, while cmd/onlava/main.go exposes psql in the actual usage text.

2. Split onlava run from onlava dev

This is the single highest-leverage rework.

Right now onlava run creates a dev supervisor, starts the dashboard, starts DB Studio, and starts the local proxy path:

* cmd/onlava/watch.go:36-79 calls newDevSupervisor, supervisor.Start, and then rebuilds/restarts the app.
* cmd/onlava/dev_supervisor.go:193-210 starts dashboard, DB Studio, and local HTTPS proxy.
* internal/codegen/generator.go:185-193 generates app mains that import _ "github.com/pbrazdil/onlava/runtimeapp".
* runtimeapp/app.go:12-18 imports internal DB Studio and local proxy packages and registers standalone dev behavior.
* runtime/app.go:55-72 can start standalone dev services when the generated app binary is run directly.

That means an app binary can carry dev-platform behavior. For production-ready v0, generated app binaries should run the app and nothing else.

I would redraw the command boundary like this:

onlava run
  deterministic app build/watch/supervise
  no dashboard by default
  no proxy by default
  no DB Studio by default
  safe JSON events for agents
onlava dev
  dashboard
  API explorer
  traces UI
  DB Studio
  local HTTPS proxy
  frontend proxy
  MCP

You can still keep onlava run --dashboard, but the default should be headless and predictable.

3. Fix release/build reproducibility before anything else

There is a concrete release blocker:

* ui/embed.go:5-8 embeds dist with //go:embed dist.
* There is no ui/dist directory in the uploaded source tree.
* cmd/onlava/dashboard.go:27 imports github.com/pbrazdil/onlava/ui.
* cmd/onlava/dashboard.go:250-257 calls fs.Sub(uidist.Dist, "dist").

That means a normal Go build of the CLI should fail unless the UI has already been built into ui/dist. Either commit the built dashboard assets, generate them as part of release packaging, or move the UI embed behind a build step/tag. Do not ship until go install ./cmd/onlava works from a clean checkout.

Also, go.mod:3 requires Go 1.26.0. That may be intentional, but it raises the bar for users and CI. Make this explicit in install docs and CI. At minimum, your release checklist should include:

go test ./...
go test -race ./... where practical
go install ./cmd/onlava from clean checkout
onlava check --json on fixtures
onlava run --json on fixtures
onlava build on fixtures
bun install / bun run typecheck / bun run test / bun run build for ui

Things that need to be redone or reworked

1. Move dev/admin endpoints off the public app router

runtime/server.go mounts dev/admin/platform endpoints on the same public router as user APIs:

* /__onlava/config at runtime/server.go:67-79
* /platform.Stats at runtime/server.go:105-114
* /debug/pprof/* at runtime/server.go:116-140

platform.Stats and pprof are not obviously protected. CORS also reflects arbitrary origins and allows credentials in runtime/server.go:153-160.

For v0, the app listener should serve only user APIs. Dev/admin operations should be on a supervisor-owned local listener, local socket, or CLI-only path:

app listener:
  user API routes only
admin/dev listener:
  /v0/status
  /v0/routes
  /v0/traces
  /v0/pprof/* only when explicitly enabled

This matters a lot if users run with --listen 0.0.0.0:....

2. Make the local HTTPS proxy opt-in

The local proxy is powerful and should not be default behavior.

Current code:

* internal/localproxy/proxy.go:63-65 defaults ONLAVA_LOCAL_PROXY to enabled.
* internal/localproxy/proxy.go:75-77 defaults trust-store installation to not skipped.
* internal/localproxy/proxy.go:136-157 starts embedded Caddy.
* internal/localproxy/proxy.go:250-283 configures Caddy PKI and InstallTrust.
* cmd/onlava/dev_supervisor.go:778-805 starts the local proxy unless ONLAVA_LOCAL_PROXY=0.

For a production-ready first release, change this to:

default:
  no local HTTPS proxy
  no trust-store install
  no frontend reverse proxy
explicit:
  onlava dev --proxy
  onlava dev --proxy --trust

Trust-store mutation is especially sensitive. Users should never be surprised by it.

3. Keep onlava-native syntax strict

The repo should expose one app model: `.onlava.json`, `github.com/pbrazdil/onlava/...` imports, and `//onlava:` directives. Migration tooling, if added later, should be explicit and separate from the runtime/parser path.

4. Reconsider source rewriting and direct-call magic

Codegen mutates user endpoint declarations:

* internal/codegen/generator.go:103-109 renames endpoint declarations.
* internal/codegen/generator.go:528-548 emits wrappers with the original function names.
* Those wrappers call onlavaruntime.CallEndpoint, so a normal-looking Go call can behave like an onlava runtime call.

That is powerful, but it is also surprising. It makes debugging harder because source code, rewritten build code, stack traces, and runtime behavior diverge.

For v0, I would prefer:

// Direct Go call means normal Go call.
resp, err := Foo(ctx, req)
// onlava RPC semantics use an explicit generated client.
resp, err := clients.MyService.Foo(ctx, req)

If you keep the rewrite model, make it inspectable:

onlava inspect rewrites --json
.onlava/gen/rewrite-map.json
onlava diff generated

Do not silently freeze this behavior without making it part of the public contract.

5. Fix .env and secrets handling

There are several inconsistencies:

* runtime/secrets.go:176-180 only loads .env.
* cmd/onlava/dev_supervisor.go:505-529 also loads .env for child process env.
* internal/dbstudio/dbstudio.go has its own .env parser.
* cmd/onlava/dev_supervisor.go validates .env.local, but the main runtime loaders do not appear to load .env.local.
* internal/codegen/generator.go:88-99 emits early secret population.
* internal/codegen/generator.go:561-565 also emits secret population inside registration init.

This should be centralized. Define one precedence rule, for example:

process env wins
.env.local next, dev-only
.env next
missing required secret behavior depends on mode

Then use one parser/loader everywhere: runtime, supervisor, DB Studio, tests.

Also, decide whether missing secrets should be warnings or hard errors. For local dev, warnings are fine. For production builds/runs, missing required secrets should probably fail early.

6. Stop copying arbitrary project files into the build workspace

internal/build/build.go:447-472 walks the app root, and internal/build/build.go:1029-1031 says every file is a source file:

func isSourceFile(rel string) bool {
    return true
}

Dot directories are skipped, but dot files are not. That means a root .env file can be copied into the build workspace/cache. This is risky because it can persist secrets in generated/build directories.

For production readiness, only copy what the build needs:

include:
  go.mod
  go.sum
  Go source files
  known assets required by the app
  generated onlava files
exclude:
  .env
  .env.*
  .git
  .onlava runtime state unless explicitly needed
  node_modules
  frontend source unless required
  docs
  local caches
  editor files

Add tests for this explicitly.

7. Fix response encoding semantics

runtime/encode.go:50-77 manually splits struct responses into headers/status/body. For body fields it does:

body[jsonName(field)] = fieldValue.Interface()

runtime/decode.go:269-279 returns the JSON tag name directly. This means json:"-" becomes a body key named "-", and omitempty is not honored in the same way normal encoding/json would honor it.

That is a correctness issue for a framework. Users will expect standard Go JSON semantics.

Before freezing, add tests for:

json:"-"
json:",omitempty"
embedded structs
pointer fields
header fields
onlava:"httpstatus"
custom marshalers

Then either fully honor encoding/json behavior or clearly document onlava’s custom response-shaping semantics.

Other issues I found

Dashboard/UI is not release-clean yet

The UI exists, but the shipped tree is inconsistent:

* ui/embed.go expects ui/dist, but ui/dist is missing.
* ui/package.json uses Bun, but Bun is not available in this environment.
* ui/src/components/layout.tsx:16-24 contains “ghost” nav items like Infra, Flow, and Snippets.
* ui/src/components/layout.tsx owns dashboard theme class wiring.
* ui/src/components/layout.tsx:208-215 has a “Cloud Dashboard” link placeholder.
* cmd/onlava/dashboard.go:30-32 accepts all WebSocket origins.
* cmd/onlava/dashboard.go:56-61 exposes GraphQL, WebSocket, dev report, SSE, and message endpoints from the dashboard server.

For v0, make the dashboard clearly dev-only. Do not let it define the stable product surface.

Watch mode ignores build-affecting files

cmd/onlava/watch.go:299-311 watches only:

.onlava.json
.go
.cpp
.h

It ignores go.mod, go.sum, .env, .env.local, and other config/build-affecting files. That may be acceptable if documented, but most users expect changes to go.mod or .env to affect the running app.

Either expand watch inputs or document the limitation.

CLI and docs need one source of truth

The actual CLI in cmd/onlava/main.go:34-52 includes:

run
build
psql
check
inspect
admin
logs
test
gen

But docs/local-contract.md:75-89 omits psql. AGENTS.md also describes a stricter Phase 1 than the actual implementation. Before release, create one canonical contract document and make the CLI usage, JSON schemas, docs, and tests match it.

Release bundle has local/macOS artifacts

The uploaded source contains .DS_Store and __MACOSX artifacts. Clean those out before release packaging.

Also verify licenses for bundled fonts under ui/public/assets/fonts. Do not ship proprietary fonts unless you have explicit redistribution rights.

What I would freeze

I would freeze this as the stable v0 surface:

Config:
  .onlava.json with name/id
  observability filters if already reliable
  no proxy config required for normal app runtime
Directives:
  //onlava:api
  //onlava:service
  //onlava:authhandler
  //onlava:middleware only if middleware ordering/semantics are fully tested
Runtime:
  typed endpoints
  raw endpoints
  public/auth/private access
  auth.UserID()
  auth.Data()
  onlava.CurrentRequest()
  service struct init/shutdown
  errs package
  rlog package
  secrets from env/.env
CLI:
  onlava run --json
  onlava build
  onlava check --json
  onlava inspect app|routes|services|build|paths --json
  onlava logs --jsonl
  onlava test
  onlava gen client --lang typescript
Generated artifacts:
  .onlava/gen/app.json
  .onlava/gen/routes.json
  .onlava/gen/services.json
  .onlava/gen/manifest.json
  .onlava/build/latest.json

I would not freeze these yet:

dashboard as stable API
DB Studio
local HTTPS proxy
trust-store installation
MCP
Temporal worker orchestration unless its lifecycle/backpressure/retry semantics are fully specified
cron unless scheduling/missed-run semantics are fully specified
source rewrite/direct-call behavior unless documented as public contract

Production-readiness checklist

Before cutting the first production-ready release, I would require these to pass:

Build/release:
  clean checkout can run go install ./cmd/onlava
  no missing ui/dist embed failure
  no .DS_Store / __MACOSX in release archive
  version command exists: onlava version --json
  docs state required Go version and Bun/UI build requirements
Tests:
  go test ./...
  fixture integration tests for typed endpoint, raw endpoint, auth, private call, service struct, secrets, errors
  JSON schema tests for run/check/inspect/logs/admin outputs
  UI typecheck/test/build if dashboard is shipped
  golden tests for generated files
Security/safety:
  no pprof on public app router by default
  no admin/dev endpoints on public app router
  no arbitrary-origin credentialed CORS unless local-only and justified
  no default trust-store installation
  request body size limits
  no copying .env into build cache
Contract:
  one canonical local contract doc
  CLI usage matches docs
  docs match implementation
  onlava-native syntax and imports documented explicitly
  stable vs beta features labeled clearly

The highest-priority fixes

If I had to reduce this to the top five:

1. Fix the release build blocker: ui/dist missing while ui/embed.go embeds it.
2. Split onlava run and onlava dev: make onlava run headless and deterministic.
3. Move dev/admin/pprof endpoints off the app router.
4. Make local HTTPS proxy and trust-store installation opt-in.
5. Keep onlava-native syntax and imports strict before freezing APIs.

The core idea is solid. The risky part is not lack of features; it is that too many features are currently considered normal runtime behavior. Freeze the smallest useful local runtime, mark the rest as dev/beta, and make the release boring, buildable, testable, and inspectable.
