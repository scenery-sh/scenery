# Optional Generated UI

A `typescript_client` without a `react` block generates ordinary HTTP contracts,
types and a client. It emits no React pages, hooks or UI catalog. Go backends and
CRUD do not require a TypeScript target, Node, Bun or the page stack.

The canonical `.scn` graph remains the declaration surface for records, SQL,
CRUD, pages and renderers. Backend authorization is enforced by the server.
Choosing or hiding a page does not change that authorization. Presentation-only
edits preserve SQL requirements and backend CRUD projection; the canonical graph
revision still changes to identify the edited application.

## Select a UI projection

Add `react = { tsconfig = "tsconfig.json" }` to the desired TypeScript target.
The consuming application supplies its React, TanStack Query, Astryx and StyleX
dependencies. Scenery uses the exact managed TypeScript checker and the app's
declared configuration; missing checker/dependencies fail generation.

Generated pages retain typed app-owned component slots. Domain forms and
workflows belong in those slots. See [the cookbook](app-development-cookbook.md)
and [catalog contracts](ui-agent-contract.md) for page and component vocabulary.

## Publication and ownership

`internal/uigen` renders pages, routes, hooks and catalog bytes from validated
canonical resources. `internal/clientprojection` shares pure graph and naming
queries with ordinary client generation. Neither package owns a compiler,
registry or artifact transaction.

`internal/generate` renders common contracts, stages and typechecks selected UI,
then publishes one artifact set through `workspacetx`. A renderer/check failure
publishes no part of that set. Disabling UI retires only descriptor/digest-owned
files and preserves application-authored files. The authoring CLI can link UI
tooling; backend runtime and SDK dependency closures do not.

## Detail pages

Generated `detail_page` adapters own typed dynamic route parameters and one
shared content component used by routed and controlled-dialog wrappers. They
compose declared sections, generated form-dialog actions, related table pages
with exact input injection, and app-owned typed action slots. Mutations invalidate
the detail and every related query without moving domain workflows into generation.

## Validation

Run `go test ./internal/generate ./internal/uigen` and the cumulative
[generator matrix](harness-engineering.md#repository-validation-matrix).
`go run ./scripts/verify --probe ui --summary --write` prepares frozen managed
tooling and runs client/catalog checks. The named `core-separation` probe proves
backend build/runtime and transitive SDK/runtime import isolation without UI.
