import { PublicApiClient } from "./generated/client.js";
import { SceneryClientError } from "./generated/runtime.js";
import type { URLString } from "./generated/types.js";

// The proof needs only Bun's Node-compatible argv, not a runtime type package.
declare const process: { readonly argv: readonly string[] };

const baseUrl = process.argv[2] as URLString;
const eventId = process.argv[3] ?? "acceptance-1";
if (!baseUrl) throw new Error("Usage: bun client/verify.ts <base-url> [event-id]");

function require(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

const anonymous = new PublicApiClient({ baseUrl });
const rejected = await anonymous.status({ eventId });
require(rejected.kind === "failure" && rejected.name === "unauthenticated", "anonymous status must reject");
const invalid = new PublicApiClient({ baseUrl, authentication: { authorization: "Bearer invalid" } });
const invalidResult = await invalid.status({ eventId });
require(invalidResult.kind === "failure" && invalidResult.name === "unauthenticated", "invalid bearer must reject");

// Standard local bootstrap exercises normal JWT verification, not a custom auth stub.
// It does not prove production signup, email delivery, refresh, or tenant isolation.
const sessionResponse = await fetch(`${baseUrl}/users/dev-bootstrap`, {
  method: "POST", headers: { "Content-Type": "application/json" }, body: "{}",
});
require(sessionResponse.ok, "local session bootstrap failed");
const session: unknown = await sessionResponse.json();
require(typeof session === "object" && session !== null && "token" in session && typeof session.token === "string", "missing token");
const client = new PublicApiClient({ baseUrl, authentication: { authorization: `Bearer ${session.token}` } });
const status = await client.status({ eventId });
require(status.kind === "result" && status.name === "processed", "processed result missing");
require(status.value.payload === "hello durable", "original payload was overwritten");
require(status.value.sha256 === "89d76a30eb90f9e1d0371be7165494ccaf8f81824941a2a6b142ddf1f769c1f8", "digest mismatch");
const missing = await client.status({ eventId: "unknown-event" });
require(missing.kind === "result" && missing.name === "missing", "unknown event must be missing");
const duplicate = await client.process({ eventId, payload: "changed" });
require(duplicate.kind === "enqueue" && duplicate.name === "accepted", "duplicate admission failed");
let rejectedLocally = false;
try {
  await client.process({ eventId: "", payload: "invalid" });
} catch (error) {
  rejectedLocally = error instanceof SceneryClientError && error.code === "invalid_input";
}
require(rejectedLocally, "generated min_length must reject before transport");
console.log("PASS typed client: anonymous/invalid auth, processed/missing outcomes, digest, duplicate admission, local constraints");
