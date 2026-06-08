# onlava Documentation Index

This is the human entry point for onlava's local knowledge base. The docs are also intended for AI agents, so prefer stable command contracts and JSON schemas over duplicated prose.

For agents, the machine-readable source of truth is [knowledge.json](knowledge.json). Validate it with:

```text
onlava inspect docs --json
onlava harness self --summary
```

## Agent Entry Points

- [Repo Agent Instructions](../AGENTS.md): mandatory repo-local operating rules for agents changing onlava itself.
- [Installable Skill](../SKILL.md): concise portable skill for agents using onlava in target apps.
- [Agent Guide](agent-guide.md): agent workflows, generated artifacts, and client-app integration guidance.

## Core Contracts

- [Architecture](../ARCHITECTURE.md): high-level repo map, boundaries, and architectural invariants.
- [Local Contract](local-contract.md): CLI grammar, stable JSON schemas, generated artifacts, and local runtime contracts.
- [Environment Reference](environment.md): onlava-owned env vars, app-injected env, and local override escape hatches.
- [App Development Cookbook](app-development-cookbook.md): practical recipes for building onlava apps.
- [Grafana Dev Integration](grafana.md): local Grafana provisioning and environment controls for `onlava up`.
- [Harness Engineering](harness-engineering.md): agent validation loop, harness outputs, and self-harness expectations.
- [Execution Plan Standard](../PLANS.md): required structure for long-running agent-executable implementation plans.

## Product Plans

- [Root Plan](../PLAN.md): current agent-first implementation plan inspired by OpenAI's harness engineering article.
- [Active Plans](plans/active.md): planned or in-progress work that agents should consider when editing the repo.
- [Completed Plans](plans/completed.md): shipped milestones and acceptance notes.
- [Tech Debt](tech-debt.md): known cleanup, risk, and follow-up items.

## Runbooks

- [Standard Auth Production Migration](runbooks/standard-auth-migration.md): operator checklist and SQL template for preserving existing users, tenants, memberships, password hashes, and sessions when moving an app to onlava standard auth.

## Schemas

JSON schemas live in [schemas/](schemas/). They are part of the local agent contract and must stay in sync with CLI output.

Start with:

- [onlava.config.v1](schemas/onlava.config.v1.schema.json)
- [onlava.check.result.v1](schemas/onlava.check.result.v1.schema.json)
- [onlava.environment.registry.v1](schemas/onlava.environment.registry.v1.schema.json)
- [onlava.harness.result.v1](schemas/onlava.harness.result.v1.schema.json)
- [onlava.inspect.validation.v1](schemas/onlava.inspect.validation.v1.schema.json)
- [onlava.validation.result.v1](schemas/onlava.validation.result.v1.schema.json)
- [onlava.harness.self.v1](schemas/onlava.harness.self.v1.schema.json)
- [onlava.harness.self.summary.v1](schemas/onlava.harness.self.summary.v1.schema.json)
- [onlava.inspect.docs.v1](schemas/onlava.inspect.docs.v1.schema.json)
- [onlava.inspect.temporal.v1](schemas/onlava.inspect.temporal.v1.schema.json)
- [onlava.worker.manifest.v1](schemas/onlava.worker.manifest.v1.schema.json)
- [onlava.docs.index.v1](schemas/onlava.docs.index.v1.schema.json)
- [onlava.version.v1](schemas/onlava.version.v1.schema.json)
