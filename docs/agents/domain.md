# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Current Repository Map

Start with [ARCHITECTURE.md](../../ARCHITECTURE.md), the applicable `AGENTS.md`
scopes, and `scenery inspect docs --for-path <path> -o json`. They are the
current repository map and contract-discovery path.

## Optional Domain Notes

- **`CONTEXT-MAP.md`** at the repo root. It points at one `CONTEXT.md` per context. Read each one relevant to the topic.
- **`docs/adr/`** for repo-wide decisions that touch the area you're about to work in.
- **Context-scoped ADRs** next to the relevant context, if present, such as `<context>/docs/adr/` or another path listed by `CONTEXT-MAP.md`.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The producer skill (`/grill-with-docs`) creates them lazily when terms or decisions actually get resolved.

## File structure

When domain notes are introduced, contexts may be top-level package or app areas; there is no required `src/` directory.

```
/
+-- CONTEXT-MAP.md
+-- docs/adr/                          # repo-wide decisions
+-- cmd/
|   +-- CONTEXT.md
|   +-- docs/adr/                      # context-specific decisions, if present
+-- internal/
|   +-- CONTEXT.md
|   +-- docs/adr/
+-- ui/
    +-- CONTEXT.md
    +-- docs/adr/
```

The example above is illustrative. Always follow `CONTEXT-MAP.md` when it exists, rather than assuming a fixed directory list.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in the relevant `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use, or there's a real gap to note for `/grill-with-docs`.

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because..._
