import { expect, test } from "bun:test";
import { assertRestart, assertSameBuild, assertServedResponse, responseIdentity, assertSession, type ResponseIdentity, type SessionIdentity } from "./runtime_identity";
type ServedIdentity = ResponseIdentity & SessionIdentity & { ownerStartedAt: string; producer: object };
const a = `sha256:${"a".repeat(64)}`;
const b = `sha256:${"b".repeat(64)}`;
const c = `sha256:${"c".repeat(64)}`;
const producer = { version: "candidate" };
function response(processID = "123") {
  return new Response(null, {
    headers: {
      "X-Scenery-Contract-Revision": a,
      "X-Scenery-Implementation-Revision": b,
      "X-Scenery-Build-Input-Digest": c,
      "X-Scenery-Go-Target": "development",
      "X-Scenery-Process-ID": processID,
    },
  });
}

test("every HTTP response must match the intended build and process", () => {
  const expected = responseIdentity(response());
  assertServedResponse(response(), expected);
  expect(() => assertServedResponse(response("124"), expected)).toThrow("generation");
  expect(() => assertServedResponse(new Response(), expected)).toThrow("identity");
  expect(() => assertSameBuild({ ...expected, implementationRevision: a }, expected)).toThrow(
    "implementationRevision",
  );
  expect(() => assertSameBuild({ ...expected, buildInputDigest: a }, expected)).toThrow(
    "buildInputDigest",
  );
});

test("restart retains exact build and root but replaces owned processes", () => {
  const before: ServedIdentity = {
    ...responseIdentity(response()),
    origin: "http://localhost:12345",
    sessionID: "fixture",
    baseAppID: "clean-tech",
    ownerPID: 12,
    ownerStartedAt: "before",
    specRevision: a,
    producer,
  };
  const after = { ...before, processID: 124, ownerPID: 13, ownerStartedAt: "after" };
  assertRestart(before, after);
  for (const invalid of [
    before,
    { ...after, buildInputDigest: a },
    { ...after, origin: "http://localhost:54321" },
    { ...after, ownerPID: before.ownerPID },
  ])
    expect(() => assertRestart(before, invalid)).toThrow();
});

test("HTTP session identity must match the owned root and process", () => {
 const session = { origin: "http://localhost:1", sessionID: "one", baseAppID: "app", specRevision: a, ownerPID: 1, processID: 123 };
 assertSession(responseIdentity(response()), { sessionID: "one", baseAppID: "app" }, session);
 expect(() => assertSession(responseIdentity(response()), { sessionID: "other", baseAppID: "app" }, session)).toThrow("session");
 expect(() => responseIdentity(response("9007199254740993"))).toThrow("identity");
});
