# Agent HTTPS Ingress

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series describes the local agent as the single machine-local router that owns `https://console.scenery.localhost` and `https://<route>.<session>.scenery.localhost`. The current agent router is global and session-aware, but it only serves HTTP. The older local proxy owns HTTPS when explicitly enabled, which keeps the HTTPS story tied to the compatibility proxy instead of the agent.

After this work, the agent can run its router in TLS mode on a configurable address, generate HTTPS session routes, and use the existing scenery local CA/trust machinery for dynamic `*.scenery.localhost` style names, including two-label session hosts such as `api.<session>.scenery.localhost`.

## Progress

* [x] 2026-05-27: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-27: Expose reusable local CA helpers from the local proxy package without adding dependencies.
* [x] 2026-05-27: Add agent router TLS options and CLI flags.
* [x] 2026-05-27: Generate HTTPS route URLs when the agent router runs with TLS.
* [x] 2026-05-27: Add on-demand leaf certificate generation for routed agent hostnames.
* [x] 2026-05-27: Make HTTPS the default for newly started agents, with `--router-http` as an explicit HTTP opt-out. This default was later reversed; current contract lives in `docs/local-contract.md`.
* [x] 2026-05-27: Update local contract docs and tests.
* [x] 2026-05-27: Run focused tests and full repository tests.
* [x] 2026-05-27: Run binary install, self harness, and diff checks.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-27: A static wildcard certificate is not enough for agent route hosts. `*.scenery.localhost` does not cover `api.<session>.scenery.localhost`, so the agent now uses SNI-based on-demand leaf certificates signed by the existing local CA.

* 2026-05-27: The agent control API remains Unix-socket-only. TLS mode applies only to the public router listener and route URL generation.

## Decision Log

* Decision: Make agent TLS an explicit router mode first, then flip newly started agents to HTTPS by default after ONLV uses agent-routed URLs everywhere.
  Rationale: Existing local workflows and tests used the high-port HTTP agent router. Staging the change kept the first TLS implementation small; after API, frontend, sync, Grafana, and Temporal routes became agent-owned, keeping HTTP as the default would preserve the wrong end state. `--router-http` remains available for local debugging. This default was later reversed; current contract lives in `docs/local-contract.md`.
  Date/Author: 2026-05-27 / Codex

* Decision: Use on-demand per-host leaf certificates rather than one static wildcard certificate.
  Rationale: `*.scenery.localhost` does not cover agent session hosts like `api.<session>.scenery.localhost`. The agent needs SNI-based certificate generation signed by the local CA.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

Completed on 2026-05-27.

Shipped outcome:

* Exported a small local CA helper surface from `internal/localproxy` for loading/creating the CA, checking/installing trust, and generating in-memory leaf certificates.
* Added agent router TLS mode via `scenery agent --router-tls`, then made TLS the default for newly started agents. This default was later reversed; current contract lives in `docs/local-contract.md`.
* Added `scenery agent --trust` and `SCENERY_AGENT_TRUST=1` to attempt local CA trust installation while keeping router startup tolerant of trust-install failures.
* Added router scheme tracking in agent state/health and session route generation so default agents emit `https://...scenery.localhost` routes.
* Added SNI-based on-demand TLS certificates for agent-routed `*.scenery.localhost` hosts, including two-label session hosts such as `api.<session>.scenery.localhost`.
* Updated CLI usage, README, local contract docs, and tests.

Validation:

```sh
go test ./internal/localproxy ./internal/agent ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

All validation commands passed. The self harness wrote `.scenery/harness/self-latest.json` and reported existing review-due and large-file warnings, but no errors.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-scenery-agent-mvp.md
internal/agent/server.go
internal/agent/router.go
internal/agent/session.go
internal/localproxy/cert.go
internal/localproxy/trust.go
cmd/scenery/agent.go
docs/local-contract.md
```

Current state:

* `internal/agent` listens on one TCP router address but serves plain HTTP.
* `internal/agent/session.go` hardcodes `http://` route URLs.
* `internal/localproxy` already owns local CA creation, leaf certificate creation, and OS trust installation, but those helpers are package-private and oriented around static local proxy subjects.

## Milestones

Milestone 1 extracts local CA helpers that can be reused by the agent and old proxy.

Milestone 2 adds an explicit TLS mode to `scenery agent` and `localagent.RunOptions`.

Milestone 3 teaches session route generation to use the active router scheme.

Milestone 4 verifies TLS serving and dynamic certificates for agent-routed hostnames.

## Plan of Work

Prefer a small extension of the existing standard-library local proxy CA code. The agent should not introduce a new certificate dependency or a separate trust store. The router can stay single-port: either HTTP or HTTPS depending on explicit configuration. Control API traffic remains on the Unix socket and is unaffected.

## Concrete Steps

1. Export a minimal local CA type and helper functions from `internal/localproxy` for loading/creating the CA, checking/installing trust, and generating a leaf certificate for requested DNS names.
2. Add `SCENERY_AGENT_TRUST=1`, `scenery agent --router-tls`, and `scenery agent --trust`.
3. Store the router scheme in the agent registry/session generation path so newly registered sessions get `https://` URLs when TLS is active.
4. Use `tls.Config.GetCertificate` in the agent router to issue and cache per-SNI certificates for `*.scenery.localhost` and `*.*.scenery.localhost` style routed hosts.
5. Add tests for route URL scheme selection and HTTPS router serving.
6. Update docs and mark the plan complete after validation.

## Validation and Acceptance

Expected validation:

```sh
go test ./internal/localproxy ./internal/agent ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Observable behavior:

* `scenery agent --router-tls --router-listen 127.0.0.1:0` serves the routed HTTP handler over TLS.
* Sessions registered with a TLS agent receive `https://...scenery.localhost` route URLs.
* The agent can generate a valid leaf certificate for `api.<session>.scenery.localhost`.
* `scenery agent --trust` attempts to install the existing scenery local CA.

## Idempotence and Recovery

CA files must remain stable across agent restarts. Leaf certificates may be generated on demand and cached in memory. If trust installation fails, the agent should continue serving TLS and report/log the trust failure instead of making the router unusable.

## Artifacts and Notes

Expected changed artifacts:

```text
internal/localproxy/cert.go
internal/agent/server.go
internal/agent/session.go
internal/agent/types.go
cmd/scenery/agent.go
docs/local-contract.md
docs/plans/0044-agent-https-ingress.md
```

## Interfaces and Dependencies

No new external dependencies expected.

New explicit controls:

```text
scenery agent --router-tls [--trust]
SCENERY_AGENT_TRUST=1
```
