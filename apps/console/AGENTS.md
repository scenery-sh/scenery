# Console App Agent Instructions

## Purpose

`apps/console` is a standalone Vite React console prototype for scenery.

## Ownership

Keep this app isolated from the existing `ui/` dashboard unless a task explicitly asks to share code.

## Local Contracts

- React Compiler is enabled through the `react-compiler-ts` Vite scaffold.
- shadcn components live under `src/components/ui`.
- Use `@/*` imports for app source.
- Vite proxies `/__scenery` WebSocket traffic to Scenery's default dashboard backend at `127.0.0.1:9401`.
- Do not commit `node_modules/` or `dist/`.

## Work Guidance

Prefer the downloaded shadcn primitives before adding new UI dependencies.

## Verification

Run from this directory when touching the app:

```sh
bun run lint
bun run build
```

## Child Agent Index

- No child `AGENTS.md` files are currently indexed under this directory.
