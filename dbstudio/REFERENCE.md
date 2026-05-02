# Drizzle Studio Reference

This workspace is the editable onlava-hosted rebuild of `https://local.drizzle.studio`.

The canonical visual and behavioral reference currently lives in:

- `reference/original/index.html`
- `reference/original/index.js`
- `reference/original/favicon.svg`
- `reference/original/index.js.map` when the upstream host actually serves a valid source map

Readable inspection mirrors can be generated into:

- `reference/pretty/index.html`
- `reference/pretty/index.js`
- `reference/pretty/favicon.svg`

Those files are fetched from the live Drizzle Studio host and should stay unchanged unless explicitly refreshed with:

```bash
bun run sync:reference
```

`sync:reference` also tries to fetch `index.js.map`, but only saves it if the upstream response is a real source map JSON payload. The current live host may advertise `sourceMappingURL=index.js.map` in `index.js` while still not actually serving the map.

If you want formatted files for inspection without mutating the canonical snapshot:

```bash
bun run format:reference
```

The current source app uses the live Drizzle Studio inside a full-viewport iframe as the parity baseline while the bundled client is replaced route by route with real React source.
