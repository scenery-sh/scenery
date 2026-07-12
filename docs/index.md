# scenery Documentation Index

This is the human entry point for scenery's local knowledge base. The docs are also intended for AI agents, so prefer stable command contracts and JSON schemas over duplicated prose.

For agents, the machine-readable source of truth is [knowledge.json](knowledge.json). Validate it with:

```text
scenery inspect docs --json
scenery harness self --summary
```

## Agent Entry Points

- [Repo Agent Instructions](../AGENTS.md): mandatory repo-local operating rules for agents changing scenery itself.
- [Installable Skill](../SKILL.md): concise portable skill for agents using scenery in target apps.
- [DSL Reference](../DSL.md): human-readable map of app config, directives, tags, model/page, durable, and cron DSL surfaces.
- [Agent Guide](agent-guide.md): agent workflows, generated artifacts, and client-app integration guidance.

## Core Contracts

- [Architecture](../ARCHITECTURE.md): high-level repo map, boundaries, and architectural invariants.
- [Local Contract](local-contract.md): CLI grammar, stable JSON schemas, generated artifacts, and local runtime contracts.
- [Environment Reference](environment.md): scenery-owned env vars, app-injected env, and local override escape hatches.
- [App Development Cookbook](app-development-cookbook.md): practical recipes for building scenery apps, including single-server storage with offsite S3 replication.
- [Harness Engineering](harness-engineering.md): agent validation loop, harness outputs, and self-harness expectations.
- [Execution Plan Standard](../PLANS.md): required structure for long-running agent-executable implementation plans.

## vNext Normative Specifications

Language edition 2027 is an opt-in, profile-based contract. The current legacy DSL remains documented separately above. Start with the [Scenery Language Specification](specs/vnext/SCENERY_LANGUAGE_SPEC.md), then use its normative companions for the [Go ABI](specs/vnext/SCENERY_GO_IMPLEMENTATION_V1.md), [HTTP codec](specs/vnext/SCENERY_HTTP_CODEC_V1.md), [typed terminal path tails](specs/vnext/SCENERY_HTTP_PATH_TAIL_V1.md), [TypeScript client](specs/vnext/SCENERY_TYPESCRIPT_CLIENT_V1.md), [compatibility core](specs/vnext/SCENERY_COMPATIBILITY_CORE_V1.md), and [legacy bridge](specs/vnext/SCENERY_LEGACY_BRIDGE_V1.md).

## Product Plans

- [Root Plan](../PLAN.md): current agent-first implementation plan inspired by OpenAI's harness engineering article.
- [Active Plans](plans/active.md): planned or in-progress work that agents should consider when editing the repo.
- [Completed Plans](plans/completed.md): shipped milestones and acceptance notes.
- [Tech Debt](tech-debt.md): known cleanup, risk, and follow-up items.

## Runbooks

- [Standard Auth Production Migration](runbooks/standard-auth-migration.md): operator checklist and SQL template for preserving existing users, tenants, memberships, password hashes, and sessions when moving an app to scenery standard auth.

## Schemas

JSON schemas live in [schemas/](schemas/). They are part of the local agent contract and must stay in sync with CLI output.

Start with:

- [scenery.config.v1](schemas/scenery.config.v1.schema.json)
- [scenery.check.result.v1](schemas/scenery.check.result.v1.schema.json)
- [scenery.environment.registry.v1](schemas/scenery.environment.registry.v1.schema.json)
- [scenery.harness.result.v1](schemas/scenery.harness.result.v1.schema.json)
- [scenery.inspect.validation.v1](schemas/scenery.inspect.validation.v1.schema.json)
- [scenery.validation.result.v1](schemas/scenery.validation.result.v1.schema.json)
- [scenery.harness.self.v1](schemas/scenery.harness.self.v1.schema.json)
- [scenery.harness.self.summary.v1](schemas/scenery.harness.self.summary.v1.schema.json)
- [scenery.inspect.docs.v1](schemas/scenery.inspect.docs.v1.schema.json)
- [scenery.docs.index.v1](schemas/scenery.docs.index.v1.schema.json)
- [scenery.cli.v1](schemas/scenery.cli.v1.schema.json)
- [scenery.manifest.v1](schemas/scenery.manifest.v1.schema.json)
- [scenery.migrate.status.v1](schemas/scenery.migrate.status.v1.schema.json)
- [scenery.typescript-client-generated.v1](schemas/scenery.typescript-client-generated.v1.schema.json)
- [scenery.client-selection.v1](schemas/scenery.client-selection.v1.schema.json)
- [scenery.version.v1](schemas/scenery.version.v1.schema.json)
- [scenery.db.list.v3](schemas/scenery.db.list.v3.schema.json)
- [scenery.db.server.status.v1](schemas/scenery.db.server.status.v1.schema.json)
