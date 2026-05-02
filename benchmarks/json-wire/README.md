# JSON vs Wire Benchmark

This fixture measures the generated TypeScript client against a small onlava app with one typed endpoint.

Run it from the repo root:

```sh
benchmarks/json-wire/run.sh
```

Useful knobs:

```sh
ITEMS=120 ITERATIONS=1800 CONCURRENCY=24 benchmarks/json-wire/run.sh
ACCEPT_ENCODING=identity benchmarks/json-wire/run.sh
PORT=48200 benchmarks/json-wire/run.sh
```

`ACCEPT_ENCODING` is intentionally exposed because Bun/fetch sends gzip by default and transparently decompresses responses. Use `ACCEPT_ENCODING=identity` to measure codec overhead without response compression.

The runner:

1. validates this onlava app with `onlava check`;
2. generates the TypeScript client into `.generated/client.ts`;
3. starts `onlava run` on `127.0.0.1:${PORT}`;
4. runs `bench.ts` with Bun;
5. stops the app process.

The benchmark reports JSON, forced wire JSON (`wire-json-strict`), forced binary wire (`binary-strict`), and auto wire with capabilities cached. It also prints one byte-sample per mode so compression behavior is visible.
