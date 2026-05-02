Yes. That is the right boundary.

I would define the product like this:

onlava dev
  Development experience.
  Interactive, forgiving, feature-rich.
onlava run
  Production/runtime experience.
  Headless, deterministic, minimal, safe.
onlava build
  Deployment artifact creation.
  Produces the thing you actually ship.

The important nuance: onlava run can be production-grade, but onlava build should still be the preferred production deployment primitive. In other words, production should usually be:

onlava build
./dist/my-app

or inside a container:

RUN onlava build --out /app/server
CMD ["/app/server"]

But onlava run should be safe enough that this is also reasonable for simpler deployments:

onlava run

provided it does not start dev-only systems.

Recommended command split

onlava dev

This should become what current onlava run mostly is today.

It can include:

file watching
automatic rebuild/restart
dashboard
API explorer
traces UI
DB Studio
local HTTPS proxy
frontend proxy
MCP server
local Pub/Sub controls
local cron controls
pretty logs
.env and .env.local loading
relaxed local-only defaults

Example:

onlava dev
onlava dev --dashboard
onlava dev --proxy
onlava dev --db-studio
onlava dev --frontend http://localhost:5173

This command may be magical. That is fine. Developers expect convenience here.

onlava run

This should run the application, not the development platform.

It should not include by default:

dashboard
DB Studio
local HTTPS proxy
trust-store installation
MCP
frontend proxy
file watching
admin UI
pprof
debug endpoints
open WebSocket origins
relaxed credentialed CORS

It should include:

one deterministic app startup
production-like config loading
strict secret validation
structured logs
graceful shutdown
health/readiness support
stable exit codes
signal handling
PORT/listen support
no mutation of local machine trust stores

Example:

onlava run
onlava run --listen :8080
onlava run --env production
onlava run --log-format json

I would avoid this:

onlava run --dashboard
onlava run --watch
onlava run --db-studio

Those should be onlava dev concerns.

What I would do to the current implementation

The current behavior should effectively be renamed:

current onlava run  ->  onlava dev
new onlava run      ->  headless runtime command

That means the current dashboard/proxy/supervisor startup path should move behind onlava dev.

From the previous audit, these are the key implementation changes:

cmd/onlava/watch.go
  likely becomes the basis for onlava dev, not production onlava run
cmd/onlava/dev_supervisor.go
  should only be used by onlava dev
runtimeapp/app.go
  should not cause generated production app binaries to start dev services
runtime/app.go
  should not automatically start standalone dev services in production mode
internal/localproxy
  should only be reachable through onlava dev --proxy or similar
internal/dbstudio
  should only be reachable through onlava dev / dashboard
runtime/server.go
  should not mount dev/admin/pprof endpoints on the public app router by default

My preferred final CLI contract

I would make the stable contract something like this:

onlava dev

Starts the full local developer environment.

onlava run

Runs the app in production-like mode from the current project. It may compile once, then run. No dashboard. No proxy. No watching.

onlava build

Builds a deployable artifact.

onlava check --json

Validates the app without running it.

onlava inspect routes --json
onlava inspect services --json
onlava inspect config --json

Machine-readable introspection.

onlava logs --jsonl

Structured log stream for automation.

onlava test

Runs onlava-aware tests.

onlava gen client

Generates clients.

Should onlava run be used directly in production?

I would support it, but not make it the only recommended path.

There are two valid production models.

Model A: build artifact, then run binary

Best for serious deployments.

onlava build --out dist/server
dist/server

This is the cleanest production story because the runtime machine does not need the full onlava CLI, source tree, dashboard assets, or build toolchain.

Model B: onlava run in production

Acceptable for simple platforms if onlava run is truly headless.

onlava run --listen :8080

But this only works if onlava run has these guarantees:

does not start dev services
does not install certificates
does not open dashboard ports
does not watch files
does not require Bun/UI assets
does not mutate source tree unexpectedly
does not copy .env into build caches
fails fast on missing required secrets
honors SIGTERM/SIGINT correctly
exits with meaningful non-zero codes

If those guarantees are not true, then onlava run should not be marketed as production-ready yet.

Suggested modes

I would avoid relying only on environment variables, but support them.

onlava dev
# implies ONLAVA_MODE=development
onlava run
# implies ONLAVA_MODE=production-like or runtime
onlava run --env production
# strict production behavior
onlava run --env staging
# production behavior, staging config

Environment variables can still override behavior:

ONLAVA_ENV=production onlava run
ONLAVA_LOG_FORMAT=json onlava run
PORT=8080 onlava run

But command choice should carry the main meaning.

Strong recommendation

Do not make onlava run mean “dev server with production option.”

Make it mean:

run the app

And make onlava dev mean:

run the app plus the local development platform

That gives you a much cleaner first production-ready release. It also makes the feature freeze easier:

Stable:
  onlava run
  onlava build
  onlava check
  onlava inspect
  onlava logs
  onlava test
  core runtime
Beta/dev:
  onlava dev dashboard
  DB Studio
  local HTTPS proxy
  MCP
  Pub/Sub UI
  cron UI

So yes: onlava dev for development including dashboard, DB Studio, proxy, etc.; onlava run for production-like app execution; onlava build for the artifact you deploy.
