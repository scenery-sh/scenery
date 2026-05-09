#!/usr/bin/env node

import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const args = process.argv.slice(2);
const projectRoot = process.cwd();
const registryRoot = path.resolve(projectRoot, process.env.ONLAVA_SHADCN_REGISTRY_ROOT ?? path.join("registry", "onlava"));
const allowedOptionsWithValue = new Set(["--cwd", "-c"]);
const rejectedOptions = new Set(["--all", "-a", "--path", "-p"]);

if (args.length === 0) {
  console.error("usage: bun run shadcn:add @onlava/<item> [...]");
  process.exit(1);
}

const parsed = parseArgs(args);
if (parsed.error) {
  console.error(parsed.error);
  process.exit(1);
}

if (!existsSync(path.join(projectRoot, "components.json"))) {
  console.error("Refusing to run shadcn outside the onlava UI package.");
  console.error("Run this command from the ui/ directory.");
  process.exit(1);
}

for (const item of parsed.items) {
  if (item.startsWith("http://") || item.startsWith("https://")) {
    console.error(`Refusing URL registry item: ${item}`);
    process.exit(1);
  }
  if (!item.startsWith("@onlava/")) {
    console.error(`Refusing non-onlava shadcn item: ${item}`);
    console.error("Use @onlava/<item> from the approved onlava registry.");
    process.exit(1);
  }
  const registryName = item.slice("@onlava/".length);
  if (registryName.includes("/") || registryName.startsWith(".") || registryName === "") {
    console.error(`Refusing nested or local path registry item: ${item}`);
    process.exit(1);
  }
  if (!existsSync(path.join(registryRoot, `${registryName}.json`))) {
    console.error(`Unknown @onlava registry item: ${item}`);
    console.error(`Expected ${path.relative(projectRoot, path.join(registryRoot, `${registryName}.json`))}`);
    process.exit(1);
  }
}

if (parsed.overwrite && process.env.ONLAVA_SHADCN_OVERWRITE !== "1") {
  console.error("Refusing --overwrite unless ONLAVA_SHADCN_OVERWRITE=1 is set.");
  process.exit(1);
}

const server = await startRegistryServer(registryRoot);
try {
  const base = ["shadcn@latest", "add", "--yes", ...args.filter((arg) => arg !== "--dry-run")];
  const dryRun = await run("bunx", [...base, "--dry-run"]);
  if (dryRun !== 0) {
    process.exit(dryRun);
  }
  if (parsed.dryRun) {
    process.exit(0);
  }
  const real = await run("bunx", base);
  process.exit(real);
} finally {
  await new Promise((resolve) => server.close(resolve));
}

function run(command, commandArgs) {
  return new Promise((resolve) => {
    const child = spawn(command, commandArgs, {
      cwd: projectRoot,
      stdio: "inherit",
    });
    child.on("close", (code) => resolve(code ?? 1));
    child.on("error", () => resolve(1));
  });
}

function parseArgs(input) {
  const items = [];
  let dryRun = false;
  let overwrite = false;
  for (let i = 0; i < input.length; i += 1) {
    const arg = input[i];
    if (arg === "--dry-run") {
      dryRun = true;
      continue;
    }
    if (arg === "--overwrite" || arg === "-o") {
      overwrite = true;
      continue;
    }
    if (rejectedOptions.has(arg)) {
      return { error: `Refusing unsupported shadcn option: ${arg}` };
    }
    if (allowedOptionsWithValue.has(arg)) {
      i += 1;
      if (i >= input.length) {
        return { error: `Missing value for ${arg}` };
      }
      continue;
    }
    if (arg.startsWith("-")) {
      continue;
    }
    items.push(arg);
  }
  if (items.length === 0) {
    return { error: "No @onlava registry item provided." };
  }
  return { items, dryRun, overwrite };
}

async function startRegistryServer(root) {
  const server = createServer((request, response) => {
    const url = new URL(request.url ?? "/", "http://127.0.0.1:4873");
    if (!url.pathname.startsWith("/r/") || !url.pathname.endsWith(".json")) {
      response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      response.end("not found\n");
      return;
    }
    const name = path.basename(url.pathname);
    const filePath = path.join(root, name);
    if (!filePath.startsWith(root + path.sep) || !existsSync(filePath)) {
      response.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
      response.end("not found\n");
      return;
    }
    const body = loadRegistryItem(filePath, root);
    response.writeHead(200, {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    });
    response.end(body);
  });
  await new Promise((resolve, reject) => {
    server.once("error", (error) => {
      if (error && error.code === "EADDRINUSE") {
        resolve();
        return;
      }
      reject(error);
    });
    server.listen(4873, "127.0.0.1", resolve);
  });
  if (!server.listening) {
    return { close: (done) => done() };
  }
  return server;
}

function loadRegistryItem(filePath, root) {
  const item = JSON.parse(readFileSync(filePath, "utf8"));
  item.files = (item.files ?? []).map((file) => {
    if (!file.source || file.content) {
      return file;
    }
    const sourcePath = path.resolve(root, file.source);
    const sourceRoot = path.resolve(root, "../..");
    if (!sourcePath.startsWith(sourceRoot + path.sep)) {
      throw new Error(`registry source escapes registry source root: ${file.source}`);
    }
    return {
      ...file,
      content: readFileSync(sourcePath, "utf8"),
    };
  });
  return JSON.stringify(item, null, 2);
}
