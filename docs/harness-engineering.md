# onlava Harness Engineering

onlava treats agent support as a runtime feature, not as prompt folklore.

The harness contract gives Codex and other agents a short feedback loop:

1. discover the app and its stable generated metadata
2. compile the generated runtime exactly like `onlava run` would
3. report diagnostics as structured JSON
4. expose inspect outputs and artifact paths without scraping terminal text
5. persist the latest harness result when requested

## Command

```text
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write]
```

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

Recommended agent loop:

```text
onlava harness --json --write
onlava harness self --json --write
```

The command runs:

- `onlava check --json`
- `onlava inspect app --json`
- `onlava inspect routes --json`
- `onlava inspect services --json`
- `onlava inspect endpoints --json`
- `onlava inspect wire --json`
- `onlava inspect build --json`
- `onlava inspect paths --json`
- `onlava inspect traces --json`
- `onlava inspect metrics --json`
- `onlava inspect docs --json`

`onlava inspect traces --json` and `onlava inspect metrics --json` are included
as beta diagnostic inputs for agents. Their schema versions are useful for
automation, but their rollup and backend-selection semantics are not stable v0
API yet; see [local-contract.md](local-contract.md).

## Output

JSON output conforms to:

- [onlava.harness.result.v1.schema.json](schemas/onlava.harness.result.v1.schema.json)

When `--write` is present, onlava writes:

```text
<app-root>/.onlava/harness/latest.json
```

That file is intentionally stable. Agents should use it as the latest local validation snapshot instead of guessing from cache directories or parsing human logs.

For the onlava repo itself, `onlava harness self --json --write` writes:

```text
<repo-root>/.onlava/harness/self-latest.json
```

The self harness validates the local onlava development loop:

- `go test ./cmd/onlava ./internal/devdash ./runtime`
- docs knowledge base integrity through `docs/knowledge.json`
- local markdown links and schema JSON syntax
- `onlava inspect docs --json`
- architecture checks for dependency policy, package boundaries, generated-file hygiene, and oversized source files
- dashboard UI typecheck and build
- DB Studio UI typecheck and build
- dashboard and DB Studio build freshness
- `go install ./cmd/onlava`
- installed `onlava` binary freshness against repo sources

## Design Rules

- Keep `AGENTS.md` short. It should point to source-of-truth docs instead of becoming an encyclopedia.
- Prefer stable JSON commands over terminal scraping.
- Prefer repo-local generated artifacts over hidden cache discovery.
- Put remediation text in diagnostics so agents know what to do next.
- Promote repeated review feedback into docs, schemas, or mechanical checks.

## Architecture Checks

`onlava harness self` includes a fast `architecture checks` step.

Hard failures:

- direct Go dependencies must be listed in the self-harness allowlist with a concrete rationale
- forbidden CLI/router/color framework imports are rejected in source
- packages outside `cmd/onlava` may not import `github.com/pbrazdil/onlava/cmd/onlava`
- required generated/vendored ignore markers must exist in `.gitignore` and `.gitattributes`
- non-generated source files over 2500 lines are rejected

Warnings:

- non-generated source files over 1000 lines
- cgo imports, because they require native build handling
- `.DS_Store` files found in the working tree
The dependency allowlist is intentionally small and lives in code next to the check. New direct dependencies should be rare and must include the reason they justify the added maintenance surface.

## Non-Goals

- The harness is not a CI replacement.
- It does not run external services by itself.
- It does not invent architecture rules. Add new checks only when the repo has a concrete invariant worth enforcing.
