# scenery Documentation Index

This is the human entry point for scenery's local knowledge base. The docs are also intended for AI agents, so prefer stable command contracts and JSON schemas over duplicated prose.

For agents, the machine-readable source of truth is [knowledge.json](knowledge.json). Validate it with:

```text
scenery inspect docs -o json
scenery harness self --summary
```

## Agent Entry Points

- [Repo Agent Instructions](../AGENTS.md): mandatory repo-local operating rules for agents changing scenery itself.
- [Installable Skill](../SKILL.md): concise portable skill for agents using scenery in target apps.
- [Agent Guide](agent-guide.md): agent workflows, generated artifacts, and client-app integration guidance.

## Core Contracts

- [Architecture](../ARCHITECTURE.md): high-level repo map, boundaries, and architectural invariants.
- [Local Contract](local-contract.md): CLI grammar, stable JSON schemas, generated artifacts, and local runtime contracts.
- [Environment Reference](environment.md): scenery-owned env vars, app-injected env, and local override escape hatches.
- [App Development Cookbook](app-development-cookbook.md): practical recipes for building scenery apps, including single-server storage with offsite S3 replication.
- [Harness Engineering](harness-engineering.md): agent validation loop, harness outputs, and self-harness expectations.
- [Execution Plan Standard](../PLANS.md): required structure for long-running agent-executable implementation plans.

## Current Specification

Scenery has one evolving application contract. Start with the [Scenery Specification](spec/SPEC.md), then use its normative companions for the [Go implementation](spec/go-implementation.md), [HTTP contract](spec/http.md), [typed terminal path tails](spec/http-path-tail.md), [TypeScript client](spec/typescript-client.md), and [evolution rules](spec/evolution.md).

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

- [scenery.config](schemas/scenery.config.schema.json)
- [scenery.build.result](schemas/scenery.build.result.schema.json)
- [scenery.environment.registry](schemas/scenery.environment.registry.schema.json)
- [scenery.harness.result](schemas/scenery.harness.result.schema.json)
- [scenery.inspect.validation](schemas/scenery.inspect.validation.schema.json)
- [scenery.validation.result](schemas/scenery.validation.result.schema.json)
- [scenery.harness.self](schemas/scenery.harness.self.schema.json)
- [scenery.harness.self.summary](schemas/scenery.harness.self.summary.schema.json)
- [scenery.inspect.docs](schemas/scenery.inspect.docs.schema.json)
- [scenery.docs.index](schemas/scenery.docs.index.schema.json)
- [scenery.cli](schemas/scenery.cli.schema.json)
- [scenery.cli.event](schemas/scenery.cli.event.schema.json)
- [scenery.manifest](schemas/scenery.manifest.schema.json)
- [scenery.typescript-client-generated](schemas/scenery.typescript-client-generated.schema.json)
- [scenery.version](schemas/scenery.version.schema.json)
- [scenery.db.list](schemas/scenery.db.list.schema.json)
- [scenery.db.server.status](schemas/scenery.db.server.status.schema.json)
