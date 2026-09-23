---
name: scenery
description: Build, run, debug, and validate applications using Scenery's .scn graph, Go runtime, and generated clients.
---

# scenery

Scenery runs one supervised runtime per canonical app root. `.scenery.json`
marks that root; `app.scn` and package-local `package.scn` declare the application
graph. Go implements generated contracts. Source, effective, and expanded graph
views answer different questions; choose the view the task needs.

Read the app's root and applicable child `AGENTS.md` files. App-specific paths,
environment names, validation profiles, and product invariants belong there.
This skill supports the requested task; a reference does not expand its scope
or grant permission for external actions or subagents.

## Route by Task

Use one relevant route and read only its linked section. Follow additional
references when the task crosses their boundary, not as a startup checklist.

| Task | Start with | Read for that change |
|---|---|---|
| Understand an app, route, or operation | `scenery inspect app`, `inspect routes`, or `inspect endpoints`, with `-o json` | [Application model](docs/agent-guide.md#current-application-model) |
| Edit declarations or Go contracts | `scenery check -o json`; generate missing contracts before raw Go tools | [Native change loop](docs/agent-guide.md#native-change-loop) |
| Start or diagnose a runtime | `scenery ps -o json`; `scenery doctor -o json` for environment failures; bounded `scenery logs -o jsonl --limit 200` | [Runtime command choice](docs/agent-guide.md#runtime-command-choice) |
| Generate TypeScript or React UI | Regenerate the declared client target; customize app-owned slots | [Client integration](docs/agent-guide.md#typescript-client-integration) |
| Declare or debug an assistant | `scenery inspect assistants -o json` | [Assistant change loop](docs/agent-guide.md#assistant-change-loop) |
| Change storage, SQL, migrations, or snapshots | Inspect the selected resource and retained owner before mutation | [Storage and databases](docs/agent-guide.md#storage-and-databases) |
| Plan a semantic mutation | Inspect schemas and capabilities, then review the issued revision-bound plan | [Diagnostics and semantic changes](docs/agent-guide.md#diagnostics-and-semantic-changes) |
| Build or swap a declared Go library | Use its generated facade | [Declared Go libraries](docs/agent-guide.md#declared-go-libraries) |
| Run an app-local code task | `scenery task list -o json` | [Code tasks](docs/app-development-cookbook.md#app-local-code-tasks) |
| Deploy an authorized change | Inspect the configured environment and deployment status | [Deployment](docs/agent-guide.md#runtime-command-choice) |
| Validate app work | Select app-owned profiles and the acceptance scenario | [Application validation](docs/agent-guide.md#application-validation-and-completion) |
| Change Scenery itself | Read its root and applicable child instructions | [Repository workflow](docs/agent-guide.md#working-in-the-scenery-repository) |

Documentation paths are relative to the Scenery checkout. Use bundled references
when available, otherwise a checkout matching the selected binary. Get exact
syntax without a checkout with `scenery help <command> -o json`; do not guess
an unavailable procedure. Practical examples live in the
[cookbook](docs/app-development-cookbook.md), including the independent
[webhook inbox](examples/webhook-inbox/README.md).

## Source and Runtime Ownership

- Declare application identities and behavior in `.scn`; Go comments and package
  initialization register nothing. Keep the singular current specification,
  compiler, runtime, and machine protocol; do not add compatibility aliases.
- Edit authored files. Generated contracts, library facades, TypeScript clients,
  and private composition are outputs. Preserve declared managed roots and the
  existing Go module; never synthesize editor modules or manage root workfiles.
  See [generated artifacts](docs/agent-guide.md#generated-and-cache-artifacts).
- Pin `scenery.sh` in the app's `go.mod`, run `scenery framework use -o json`, and
  use the reported worktree-local executable. Explicit co-development selects
  `framework use --source <checkout>`; it freezes source bytes. Do not commit
  its local replacement or infer runtime parity from a checkout SHA.
- Use `scenery up` for the live loop and another worktree for another code copy.
  Discover URLs through `scenery ps -o json`. An incompatible owner is not
  permission to replace a live runtime. Installation does not migrate apps/data.
- Prefer `-o json` and `-o jsonl`. Check schema/spec revisions and producer
  identity, branch on stable `SCNxxxx` diagnostics, and resolve opaque source
  IDs through the source map. Never guess substrate ports or owner records.
  An internal `SCN9xxx` diagnostic withholds its cause; read it with
  `scenery inspect report <report-token> -o json`.

## Authorization and Data

Continue authorized local edits, builds, checks, and corrections without asking
for the same permission again. Follow the user's and app's actual delegation,
deployment, installation, and destructive-action boundaries.

Retained databases and storage belong to their verified canonical worktree.
Branch switches and Git/worktree removal do not authorize deleting data.
Never adopt mismatched resources or replace missing/corrupt state with empty
data. App code uses public capabilities and authenticated tenant context.
Before deletion, migration, restore, or retained-state repair, read the
[owning workflow](docs/agent-guide.md#storage-and-databases) and its linked
runbook. Preview first; apply only the authorized selector/revision. Keep
plan approvals and caller identity in trusted execution context.

## Completion

Complete the requested behavior, its selected validation, and corrections caused
by the change. Runtime/UI tasks require the requested live scenario and current
served identity, not only compilation. Report commands, results, and unresolved
coverage; planned, skipped, or warning-only checks are not successful proof.

Use the app's frontend checks and browser acceptance for its pages. Scenery has
no dashboard; app-owned developer tooling uses the generated `dev-runtime.ts`
client for the development runtime RPC. Repository validation
uses `go run ./scripts/verify --summary --write` or the quick mode selected by
the root matrix; it is not an installed app command. Reuse successful checks
for unchanged inputs and keep Go's test cache enabled.
Do not run `go install ./cmd/scenery` unless the human explicitly asks.
