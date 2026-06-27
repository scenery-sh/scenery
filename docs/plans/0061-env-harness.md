# ENV Harness

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Stop accidental environment-variable growth in scenery. AI agents and contributors have been adding new `SCENERY_*`, tool, and app-facing environment names as a convenient escape hatch. That makes behavior harder to reason about, harder to test, and harder for target apps to operate. The intended policy is the opposite: if a setting is real product configuration, it belongs in `.scenery.json`, an explicit CLI flag, or a checked-in manifest. Environment variables should exist only for secrets, process identity injected by scenery, test-only controls, explicitly requested escape hatches, and compatibility names that are deliberately being retired.

After this work, `scenery harness self --json --write` must make ENV drift visible and, by default, fail when production code introduces a new unapproved environment variable. Adding a new environment variable should require an explicit registry entry, a documented rationale, tests, and a Decision Log entry in the relevant ExecPlan. The easiest path for future agents should be to avoid ENV variables entirely.

## Progress

- [x] 2026-06-01: Created this ExecPlan draft as `docs/plans/0061-env-harness.md`; add it to `docs/plans/active.md` before implementation begins.
- [x] 2026-06-01: Added `docs/environment.registry.json` and migrated the existing `docs/environment.md` table into registry entries with direction, category, stability, secret flags, allowed scopes, rationale, and docs metadata.
- [x] 2026-06-01: Replaced the warning-only ENV drift check with registry-backed strict errors for unregistered runtime env usage, test-only env usage in production code, undocumented registered runtime env usage, and direct production `os.Getenv`/`LookupEnv`/`Environ`/`Setenv`/`Unsetenv` calls outside `internal/envpolicy`.
- [x] 2026-06-01: Added `internal/envpolicy` for registry loading, matching, scanning, redaction, and process-environment wrappers, then migrated production env reads/writes through that package.
- [x] 2026-06-01: Updated docs, schemas, tests, and quick self-harness validation for the registry-backed policy.
- [x] 2026-06-01: Full validation passed: focused env/harness tests, `go test ./cmd/scenery`, `go test ./...`, `go install ./cmd/scenery`, `scenery inspect docs --json`, `scenery harness self --json --write`, and `git diff --check`.

## Surprises & Discoveries

- 2026-06-01: `cmd/scenery/harness_drift.go` already has a first-pass environment scanner. It scans Go files for `SCENERY_` tokens, skips architecture-generated/cache dirs, classifies `_test.go` and `testdata/` as test scope, and warns when runtime variables are not documented in `docs/environment.md`, `docs/local-contract.md`, `docs/grafana.md`, or `SKILL.md`. The next step is to make this registry-backed and fail-closed for new runtime variables, not merely warning-based.
- 2026-06-01: The same file currently records live `SCENERY_*` values in the toolchain preflight report through `sortedSceneryEnv(os.Environ())`. This is useful for local diagnostics but should be reviewed for redaction and separated from source-contract enforcement.
- 2026-06-01: `docs/environment.md` states the desired direction already: prefer `.scenery.json` for stable app configuration and reserve env vars for local overrides, secrets, process identity, or explicit escape hatches. The harness should enforce that policy rather than relying on prose.
- 2026-06-01: Recent hardening PRs added or documented several env surfaces around Grafana and Temporal. PRs #15 and #17 describe new Grafana environment variables and hardening, and PRs #14 and #16 describe Temporal production/runtime configuration changes. Those are exactly the areas where future work should prefer typed config or managed manifests unless an env escape hatch is deliberately approved.
- 2026-06-01: The broader scanner initially misclassified Temporal span-kind constants such as `TEMPORAL_WORKFLOW` as process env names. The implementation now treats `TEMPORAL_*`, `VITE_*`, `SYNC_*`, and OTEL names as exact approved names unless the registry uses a deliberate prefix family.
- 2026-06-01: `SCENERY_TEST_WATCH_SETTLE_DELAY_MS` and related watch timing overrides are test-named process-level escape hatches read by production dev watcher code so integration tests can shorten debounce/poll timing. They are registry-approved as `test_escape_hatch` instead of `test_only`, while pure `SCENERY_TEST_*` and `SCENERY_INTEGRATION_*` names remain disallowed in production code.

## Decision Log

- Decision: Create a strict ENV harness instead of relying on documentation review.
  Rationale: Documentation-only guidance is too easy for agents to satisfy by adding another row to `docs/environment.md`. The desired behavior is a policy gate: a new production env name is exceptional and must be reviewed as such.
  Date/Author: 2026-06-01 / OpenAI assistant

- Decision: Use a checked-in machine-readable registry as the source of truth and generate or validate human docs from it.
  Rationale: Free-text docs cannot reliably distinguish injected, user-input, internal, test-only, secret, compatibility, and deprecated variables. A registry gives the harness stable metadata and gives agents a concrete file to inspect before proposing a new env.
  Date/Author: 2026-06-01 / OpenAI assistant

- Decision: Treat CLI flags, `.scenery.json`, and `scenery.toolchain.json` as the preferred homes for configurable behavior.
  Rationale: These surfaces are explicit, inspectable, schema-testable, and reviewable. Environment variables should not become an unbounded parallel configuration system.
  Date/Author: 2026-06-01 / OpenAI assistant

- Decision: Keep test-only env variables allowed but make the prefix and scope explicit.
  Rationale: Tests sometimes need process-level controls. Those names should be visibly separate from runtime contract names, preferably `SCENERY_TEST_*` or package-local test helpers, and should not appear in app-facing docs as supported knobs.
  Date/Author: 2026-06-01 / OpenAI assistant

- Decision: Keep the registry under `docs/environment.registry.json` and validate it with `docs/schemas/scenery.environment.registry.v1.schema.json`.
  Rationale: This keeps the human env reference and machine-readable contract side by side, discoverable through `docs/knowledge.json`, and enforceable by `scenery harness self`.
  Date/Author: 2026-06-01 / OpenAI assistant

- Decision: Route production process-environment access through `internal/envpolicy` wrappers instead of leaving scattered direct `os.Getenv` calls.
  Rationale: A tiny wrapper layer gives the harness one approved boundary to enforce without turning env handling into a configuration framework.
  Date/Author: 2026-06-01 / OpenAI assistant

## Outcomes & Retrospective

Completed on 2026-06-01.

Shipped:

- `docs/environment.registry.json` as the machine-readable source of truth for approved env names, prefix/glob families, allowed scopes, direction, category, stability, secret status, rationale, preferred surface, and docs links.
- `docs/schemas/scenery.environment.registry.v1.schema.json` plus self-harness schema validation for the registry.
- `internal/envpolicy` for registry loading, matching, source scanning, secret-value redaction, and the approved process-environment access boundary.
- Registry-backed `scenery harness self` drift checks that fail on unregistered production env usage, test-only env usage in production code, undocumented registered runtime env usage, and direct production `os.Getenv`/`os.LookupEnv`/`os.Environ`/`os.Setenv`/`os.Unsetenv` outside `internal/envpolicy`.
- Production env access routed through `internal/envpolicy`; a repo scan now finds direct `os.*env` calls only in `internal/envpolicy` itself.
- Docs updates in `AGENTS.md`, `SKILL.md`, `docs/environment.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `docs/index.md`, and `docs/knowledge.json`.

Validation:

- `go test ./cmd/scenery -run 'TestHarness.*Env|TestEnvPolicy|TestHarnessSelf'` passed.
- `go test ./internal/envpolicy` passed.
- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `go install ./cmd/scenery` passed.
- `scenery inspect docs --json` passed with 25 documents and zero missing or stale docs.
- `scenery harness self --json --write` passed. Relevant summaries: contract drift checks `diagnostics=0`, env vars `168`; Go tests `packages=43`; schema validation `errors=0`, validated `12`.
- `git diff --check` passed.

Retrospective:

The registry made current env sprawl visible without needing a large configuration redesign. A few scanner false positives were useful calibration points: generated/client constants and Temporal span-kind names should not become env contract entries just because they are uppercase. The small `envpolicy` boundary is intentionally boring; its value is that future agents now have a narrow place to look and a harness gate that makes new env variables exceptional.

## Context and Orientation

Start with these files:

```text
cmd/scenery/harness_drift.go
cmd/scenery/harness_self.go
cmd/scenery/harness_schema.go
cmd/scenery/harness_*_test.go
docs/environment.md
docs/local-contract.md
docs/agent-guide.md
AGENTS.md
SKILL.md
PLANS.md
docs/plans/active.md
scenery.toolchain.json
```

Current behavior to preserve where useful:

- `scenery harness self --json --write` already runs contract drift checks and emits a `scenery.harness.drift.v1` report.
- `buildHarnessEnvVarReport` scans Go source for `SCENERY_` tokens and records whether each runtime variable is documented.
- Test-only variables are recognized when found in `_test.go`, `testdata/`, `SCENERY_TEST_*`, or `SCENERY_INTEGRATION_*` contexts.
- Environment docs currently classify many variables informally with a `Direction` column such as `user input`, `injected`, `internal`, or mixed forms.

Terms used in this plan:

- **Environment registry** means a checked-in machine-readable file that enumerates approved environment variable names and metadata. Suggested path: `docs/environment.registry.json` or `internal/env/registry.json` if implementation packages should embed it.
- **Runtime variable** means a name read or written by production code outside tests and fixtures.
- **Injected variable** means a name set by scenery for child processes, frontends, dashboards, workers, or managed sidecars.
- **User-input variable** means a supported external knob a user may set.
- **Escape hatch** means a user-input variable kept only because a typed config/flag cannot yet cover the case. Escape hatches should include a sunset policy or a reason they cannot be replaced.
- **Test-only variable** means a name used only by tests, fixtures, or integration harnesses.
- **Compatibility variable** means a name retained for migration from old behavior. Compatibility variables must name their replacement or removal condition.

## Milestones

### Milestone 1: Inventory and classification

Build a complete inventory of environment variables used by source, docs, scripts, tests, generated fixtures, and UI code.

Acceptance:

- The scanner covers at least `.go`, `.sh`, `.mjs`, `.js`, `.ts`, `.tsx`, `.md`, `.json`, `.hcl`, `.sql`, and fixture files where env names can define public contract.
- The scan distinguishes reads, writes/injections, docs-only mentions, test-only mentions, fixture-only mentions, and generated examples when practical.
- The initial report lists every discovered name with path references and scope.
- Existing `SCENERY_*`, standard external names such as `DATABASE_URL`, `TEMPORAL_ADDRESS`, `OTEL_EXPORTER_*`, and app-defined auth env examples are accounted for rather than ignored because they are not prefixed with `SCENERY_`.

Implementation notes:

- Extend `extractSceneryEnvTokens` into a more general token scanner, but avoid parsing secrets or values. The harness only needs names and file references.
- Keep the first implementation simple and deterministic: string/token scanning is enough if it is well-tested and has low false negatives.
- Avoid shelling out to grep. The harness should work on every supported developer machine using Go code.

### Milestone 2: Machine-readable registry

Create the registry and migrate the existing documented variables into it.

Suggested schema fields:

```json
{
  "name": "SCENERY_DEV_GRAFANA",
  "scope": "runtime",
  "direction": "user_input",
  "category": "observability.grafana",
  "stability": "dev_escape_hatch",
  "secret": false,
  "allowed_in": ["code", "docs", "tests"],
  "owner": "scenery dev platform",
  "rationale": "Disable or require dev Grafana while the managed sidecar is optional.",
  "preferred_surface": ".scenery.json dev observability config when promoted beyond dev-only behavior",
  "replacement": "",
  "sunset": "",
  "docs": ["docs/environment.md"]
}
```

Acceptance:

- Every current production env name has exactly one registry entry.
- Every registry entry has direction, category, stability, secret flag, owner, rationale, and docs fields.
- The registry can represent prefix families such as `SCENERY_FRONTEND_<NAME>_ADDR`, `SCENERY_VICTORIA_METRICS_*`, `GF_*`, or `OTEL_EXPORTER_*` without forcing an infinite list of generated concrete names.
- Test-only names either live in a separate `test_only` section or are explicitly tagged so the production docs do not imply support.
- `docs/environment.md` is updated to say the registry is the machine-readable source of truth, while the Markdown file remains the human reference.

### Milestone 3: Strict self-harness enforcement

Make `scenery harness self` fail when a production env name appears outside the registry or violates its allowed scope.

Acceptance:

- New unregistered runtime env usage is an error, not a warning.
- Registered-but-undocumented user-input and injected variables are errors unless their registry entry is intentionally `internal` or `test_only`.
- Test-only variables used by production code are errors.
- Secret-bearing variables may be named but never have values captured in harness JSON, snapshots, logs, or diagnostics.
- The drift report identifies file paths and suggested actions: remove the env, move the configuration to `.scenery.json` or a CLI flag, or add a registry entry with rationale if explicitly approved.
- Existing warnings are tightened only after the baseline registry is complete, so the implementation can land in staged commits without making the repo red halfway through.

### Milestone 4: Centralized env access layer

Add a small package or internal helper that makes env reads/writes discoverable and testable.

Possible paths:

```text
internal/envpolicy/registry.go
internal/envpolicy/lookup.go
internal/envpolicy/report.go
```

Acceptance:

- Production code does not call `os.Getenv`, `os.LookupEnv`, or inspect `os.Environ` directly except in the envpolicy package and carefully named process-launch boundaries.
- Child-process env injection uses registry-backed helper functions that know whether a variable is injected, user-input, internal, secret, or test-only.
- Existing functions that intentionally preserve process env, such as child command setup, call a helper that redacts sensitive names in diagnostics.
- Unit tests cover allowed lookup, disallowed lookup, test-only lookup, prefix-family matching, redaction, and deterministic report ordering.

Do not overbuild this into a runtime dependency graph. It should be a guardrail and small helper layer, not a full configuration framework.

### Milestone 5: Replace questionable ENV controls with typed surfaces

Audit current user-input env variables and decide which should become `.scenery.json`, CLI flags, or managed manifest fields.

High-value candidates:

- Tool paths and downloads should prefer `scenery.toolchain.json`, `SCENERY_TOOLCHAIN_DIR`, and explicit per-tool override only when truly necessary.
- Grafana/Victoria ports, reuse, versions, downloads, and public URLs should be evaluated for `.scenery.json dev.observability` or managed toolchain fields.
- Temporal production connection settings should prefer typed app config for non-secret values and env only for secrets such as API keys and certificate paths when no better secret source exists.
- Frontend and app URL injection should be mostly internal/injected, with explicit config for stable behavior.
- Release-gate and harness-only controls should be test/tooling scope, not runtime docs.

Acceptance:

- The audit produces a table in this ExecPlan or a follow-up plan that classifies each env variable as keep, replace, deprecate, compatibility-only, or test-only.
- No env is removed without an explicit migration path when current code or docs advertise it as user input.
- New configurable behavior added during this work uses `.scenery.json`, CLI flags, or manifests unless this plan’s Decision Log records why env is required.

Current audit:

| Class | Decision | Notes |
| --- | --- | --- |
| App identity and routing injection | keep | Injected variables such as `SCENERY_APP_ID`, `SCENERY_LISTEN_ADDR`, session IDs, routed API/sync URLs, and Temporal task-queue/build metadata are process identity, not user configuration. |
| Secrets and service URLs | keep | Secret or credential-bearing variables such as `DATABASE_URL`, `SCENERY_AUTH_JWT_SECRET`, and `TEMPORAL_API_KEY` stay env-backed and are marked secret for harness redaction. |
| Managed toolchain controls | keep for now | `SCENERY_TOOLCHAIN_DIR`, `SCENERY_TOOLCHAIN_DOWNLOAD`, and explicit per-tool binary/download overrides stay registered escape hatches because plan 0059 owns the typed managed-toolchain surface. |
| Grafana/Victoria controls | keep for now | Local dev sidecar knobs remain registered `dev_escape_hatch` variables; future promotion should prefer `.scenery.json dev.observability` or managed manifests. |
| Local proxy/frontends | compatibility/dev escape hatches | Legacy proxy variables and `SCENERY_FRONTEND_<NAME>_ADDR` remain registered for explicit manual debugging while agent routing is preferred. |
| Test-only controls | restrict | `SCENERY_TEST_*` and `SCENERY_INTEGRATION_*` are allowed only in tests/docs unless a concrete registry entry documents a process-level exception such as the `SCENERY_TEST_WATCH_*_MS` timing overrides. |

## Plan of Work

Start by extending the existing harness drift code rather than adding a second checker. The current code already participates in `scenery harness self`, emits JSON, and has the right diagnostic shape. Change it from "find undocumented SCENERY tokens" to "compare discovered env names against a registry and fail on policy violations."

Introduce the registry with the current baseline first, even if some entries initially have blunt categories. Then tighten enforcement in a second step. This avoids making unrelated feature work fail while the inventory is incomplete. Once the baseline is green, add tests that prove a fake new env in a temporary repo fails self-harness with a clear diagnostic.

For production code, prefer removing env knobs over registering them. The registry is not permission to keep adding configuration. Its job is to make every surviving env an explicit exception.

## Concrete Steps

1. Add focused tests around the existing scanner in `cmd/scenery/harness_drift_test.go` if they do not already exist. Cover production source, test source, docs-only mentions, prefix families, and non-`SCENERY_` names such as `DATABASE_URL` and `TEMPORAL_ADDRESS`.
2. Create the first registry file. Use a path that is easy for agents to discover from `docs/environment.md` and `docs/agent-guide.md`.
3. Add Go types for registry loading, validation, prefix-family matching, and deterministic sorting.
4. Extend the source scanner to collect file references and more file extensions. Keep ignore rules shared with existing architecture skip logic.
5. Change `harnessEnvVarFinding` to include registry status, direction, stability, category, files, and policy violations. If this changes the JSON contract, update schemas and tests in the same change.
6. Keep current undocumented env findings as warnings until all existing variables have registry entries.
7. Flip production unregistered variables to errors and add a fixture/self-harness test that injects a fake `SCENERY_NEW_ESCAPE_HATCH` usage into a temporary repo and expects failure.
8. Add redaction tests for live environment capture in toolchain preflight. Values for names marked `secret: true`, matching secret-like suffixes, or explicitly listed prefixes must be omitted or replaced with `<redacted>`.
9. Update `docs/environment.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `AGENTS.md`, and `SKILL.md` so agents see the rule before touching code.
10. Run the validation commands below and record results in Progress and Outcomes.

## Validation and Acceptance

Run from the repository root:

```sh
go test ./cmd/scenery -run 'TestHarness.*Env|TestEnvPolicy|TestHarnessSelf'
go test ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Also run these negative checks during implementation, either as unit tests or manual proof recorded in this plan:

```sh
# In a temporary copy/fixture, add a production reference to SCENERY_FAKE_NEW_ENV.
scenery harness self --repo-root <fixture> --json
# Expected: ok false, diagnostic names SCENERY_FAKE_NEW_ENV and suggests removing it or adding a registry entry with rationale.

# In a temporary copy/fixture, add SCENERY_TEST_ONLY_EXAMPLE in production code.
scenery harness self --repo-root <fixture> --json
# Expected: ok false, diagnostic says test-only env used by production code.

# Set a secret-like env while running self-harness.
SCENERY_AUTH_JWT_SECRET=example scenery harness self --json
# Expected: output contains the name only or a redacted value, never the raw value.
```

Acceptance for the whole plan:

- The self-harness fails closed for new production env names.
- Existing approved env names are visible in a registry with rationale and category.
- Docs make `.scenery.json`, CLI flags, and manifests the default answer for configurability.
- Agents have a clear rule: do not add an env variable unless the user explicitly asks for one or an active ExecPlan records the exception.
- Harness JSON remains deterministic and schema-validated.

## Idempotence and Recovery

The registry migration should be safe to resume. If a partial registry exists, rerun the scanner and fill missing entries. Avoid auto-generating rationales that look authoritative; unknown rationale should block strict enforcement until a human-readable rationale is written.

If strict enforcement makes the repo red because the initial inventory missed a legitimate name, add the missing registry entry with evidence and a Decision Log note. If it exposes an accidental env variable, remove the env usage instead of registering it.

If docs and registry drift, prefer registry as machine-readable source of truth after Milestone 2 and update docs in the same change. During Milestone 1, docs remain the source for the existing warning-only behavior.

If a command emits secret values into harness output, treat that as a bug. Patch redaction first, then continue the broader harness work.

## Artifacts and Notes

Expected artifacts after implementation:

```text
docs/environment.registry.json
cmd/scenery/harness_drift.go
cmd/scenery/harness_drift_test.go
cmd/scenery/harness_schema.go
docs/schemas/scenery.harness.drift.v1.schema.json
docs/environment.md
docs/local-contract.md
docs/agent-guide.md
AGENTS.md
SKILL.md
```

Possible follow-up artifacts:

```text
internal/envpolicy/registry.go
internal/envpolicy/lookup.go
internal/envpolicy/registry_test.go
```

Avoid adding generated snapshots by hand. Regenerate harness outputs with `scenery harness self --json --write` when the implementation changes the snapshot contract.

## Interfaces and Dependencies

This work touches scenery’s development and agent-facing contracts, but it should not add external dependencies. Use the Go standard library for scanning, JSON loading, sorting, and path handling.

Interfaces affected:

- `scenery harness self --json --write`: stricter drift diagnostics and possibly expanded `scenery.harness.drift.v1` env fields.
- `docs/environment.md`: human reference generated from or validated against the registry.
- `docs/local-contract.md` and `docs/schemas/`: machine-readable contract changes if drift JSON fields change.
- `AGENTS.md`, `SKILL.md`, and `docs/agent-guide.md`: agent instructions must make ENV additions exceptional.

Compatibility expectation:

Existing supported env variables should keep working until a separate migration plan removes them. This ExecPlan is primarily a guardrail against new env growth, plus an audit that identifies candidates for future removal or replacement.
