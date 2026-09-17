// Protocol proof of the generated Scenery channel and connection against a
// simulated Eve 0.39 runtime. The simulation reproduces the Eve behavior the
// helper relies on, as observed with the real runtime: a sent message starts a
// turn whose events all carry its turn ID, a turn that requests approval ends
// (turn.completed, then session.waiting), an answered approval is resolved and
// its tool call runs in the original turn, after which Eve continues in a new
// turn without a received message (or, when another turn is active, inside that
// turn, which the helper prevents by running one run at a time), the durable
// stream is read by absolute index, and a connection header
// callback receives the executing turn. The verifier renders the templates and
// runs this file with `node --test`.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

// The verifier runs the proof from the rendered overlay.
const overlay = process.cwd();
const token = "control-token-for-helper-protocol-proof";
process.env.SCENERY_ASSISTANT_CONTROL_TOKEN = token;
process.env.SCENERY_MCP_BRIDGE_SECRET = "bridge-secret-for-helper-protocol-proof";
process.env.SCENERY_MCP_URL = "http://127.0.0.1:4455";

const channelSource = readFileSync(join(overlay, "agent/channels/scenery.ts"), "utf8");
const revision = (name) => channelSource.match(new RegExp(`const ${name} = "([^"]+)"`))[1];
const identity = JSON.parse(`{"assistant_address":${channelSource.match(/const assistantAddress = (".*?");/)[1]},"runtime_revision":${channelSource.match(/const runtimeRevision = (".*?");/)[1]},"capability_revision":${channelSource.match(/const capabilityRevision = (".*?");/)[1]}}`);
const channel = (await import(join(overlay, "agent/channels/scenery.js"))).default;
const connection = (await import(join(overlay, "agent/connections/scenery.ts"))).default;

let nextSession = 0;

class FakeEve {
  constructor() {
    this.sessions = new Map();
    this.byToken = new Map();
    this.responses = [];
  }
  session(id) {
    return this.sessions.get(id);
  }
  create(tokenValue) {
    const id = `session_${++nextSession}`;
    const session = { id, events: [], queue: [], active: null, turns: 0, waiters: new Set() };
    this.sessions.set(id, session);
    this.byToken.set(tokenValue, id);
    this.emit(session, { type: "session.started", data: {} });
    return session;
  }
  emit(session, event) {
    session.events.push({ ...event, meta: { id: `evt_${session.events.length}`, at: new Date(0).toISOString() } });
    for (const wake of session.waiters) wake();
  }
  enqueue(session, message, turnPolicy) {
    assert.equal(turnPolicy, "queue", "the helper submits turns with the queue turn policy");
    session.queue.push(message);
  }
  newTurn(session) {
    assert.equal(session.active, null, "one turn at a time");
    const turnId = `turn_${session.turns++}`;
    session.active = turnId;
    this.emit(session, { type: "turn.started", data: { sequence: session.turns, turnId } });
    return turnId;
  }
  // startTurn starts the next queued message as a new turn.
  startTurn(sessionID) {
    const session = this.session(sessionID);
    const message = session.queue.shift();
    assert.notEqual(message, undefined, "a queued message starts the turn");
    const turnId = this.newTurn(session);
    this.emit(session, { type: "message.received", data: { message, sequence: session.turns, turnId } });
    return turnId;
  }
  // startForeignTurn starts a turn no Scenery request sent.
  startForeignTurn(sessionID, message) {
    const session = this.session(sessionID);
    const turnId = this.newTurn(session);
    this.emit(session, { type: "message.received", data: { message, sequence: session.turns, turnId } });
    return turnId;
  }
  // park requests approval; Eve ends the turn and waits.
  park(sessionID, turnId, requestId) {
    const session = this.session(sessionID);
    this.emit(session, { type: "input.requested", data: { requests: [{ kind: "tool-approval", requestId, action: { toolName: "scenery__house__process_scene" } }], turnId } });
    this.end(session, turnId, "turn.completed");
  }
  // resolve settles an approval in its original turn, where the approved tool
  // call runs.
  resolve(sessionID, requestId, originalTurn) {
    const session = this.session(sessionID);
    this.emit(session, { type: "approval.settled", data: { outcome: "approved", requestId } });
    this.emit(session, { type: "input.resolved", data: { resolutions: [{ kind: "tool-approval", outcome: "approved", requestId }], turnId: originalTurn } });
  }
  // continueTurn starts the continuation turn after a resolved approval.
  continueTurn(sessionID) {
    const session = this.session(sessionID);
    const turnId = this.newTurn(session);
    this.emit(session, { type: "step.started", data: { stepIndex: 0, turnId } });
    return turnId;
  }
  complete(sessionID, turnId) {
    const session = this.session(sessionID);
    this.emit(session, { type: "message.completed", data: { message: `done ${turnId}`, turnId } });
    this.end(session, turnId, "turn.completed");
  }
  end(session, turnId, type) {
    this.emit(session, { type, data: { turnId } });
    this.emit(session, { type: "session.waiting", data: { continuationToken: "", wait: "next-user-message" } });
    if (session.active === turnId) session.active = null;
  }
  handle(sessionID) {
    const eve = this;
    const session = this.session(sessionID);
    return {
      id: sessionID,
      async send(message, options) {
        eve.enqueue(session, message, options.turnPolicy);
        return { status: "accepted", sessionId: sessionID };
      },
      async cancel(options) {
        return { status: "accepted", turnId: options?.turnId };
      },
      async getStreamTailIndex() {
        return session.events.length - 1;
      },
      async getEventStream({ startIndex = 0 } = {}) {
        let index = startIndex;
        let wake = null;
        let cancelled = false;
        return new ReadableStream({
          async pull(controller) {
            while (!cancelled && index >= session.events.length) {
              await new Promise((resolve) => {
                wake = resolve;
                session.waiters.add(resolve);
              });
              session.waiters.delete(wake);
            }
            if (!cancelled) controller.enqueue(session.events[index++]);
          },
          cancel() {
            cancelled = true;
            if (wake) {
              session.waiters.delete(wake);
              wake();
            }
          },
        });
      },
    };
  }
  operations() {
    const eve = this;
    return {
      requestIp: "127.0.0.1",
      from(address) {
        return {
          async send(message, options) {
            const existing = eve.byToken.get(address);
            const session = existing ? eve.session(existing) : eve.create(address);
            eve.enqueue(session, message, options.turnPolicy);
            return { id: session.id };
          },
          async respond(responses) {
            eve.responses.push(...responses);
            return { id: eve.byToken.get(address) };
          },
        };
      },
      attachSession: (sessionID) => eve.handle(sessionID),
    };
  }
}

function route(method, path) {
  return channel.routes.find((candidate) => candidate.method === method && candidate.path === path).handler;
}

async function control(eve, type, fields, operations = eve.operations()) {
  const body = {
    kind: "scenery.assistant.control.request",
    schema_revision: revision("requestSchemaRevision"),
    type,
    request_id: `request_${type}_${Math.random()}`,
    ...identity,
    principal: "principal-1",
    ...fields,
  };
  const request = new Request("http://127.0.0.1/scenery/v1/control", {
    method: "POST",
    headers: { "x-scenery-assistant-control-token": token, "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  const response = await route("POST", "/scenery/v1/control")(request, operations);
  return { status: response.status, body: await response.json() };
}

async function stream(eve, sessionID, after = 0) {
  const request = new Request(`http://127.0.0.1/scenery/v1/control/sessions/${sessionID}/events?after=${after}`, {
    headers: { "x-scenery-assistant-control-token": token },
  });
  const response = await route("GET", "/scenery/v1/control/sessions/:sessionId/events")(request, { ...eve.operations(), params: { sessionId: sessionID } });
  assert.equal(response.status, 200);
  const text = await response.text();
  return text.split("\n").filter(Boolean).map((line) => JSON.parse(line));
}

// toolCall resolves the connection headers as Eve does for one tool call of a
// turn, and returns the run the signed assertion names or the refusal.
async function toolCall(sessionID, turnId) {
  try {
    const headers = await connection.headers({ session: { id: sessionID, auth: { current: { principalId: "principal-1" } }, turn: { id: turnId, sequence: 0 } } });
    const [payload] = headers["Scenery-Assistant-Assertion"].split(".");
    return JSON.parse(Buffer.from(payload, "base64url").toString()).run_id;
  } catch (error) {
    return `refused: ${error.message}`;
  }
}

async function until(condition, what) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.fail(`timed out waiting until ${what}`);
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function created(eve, message, runID, digest) {
  const response = await control(eve, "conversation.create", { conversation_digest: digest, run_id: runID, message });
  assert.equal(response.status, 200, JSON.stringify(response.body));
  return response.body;
}

async function turn(eve, conversation, runID, message) {
  const response = await control(eve, "conversation.turn", { private_session_id: conversation.private_session_id, continuation_token: conversation.continuation_token, run_id: runID, message });
  assert.equal(response.status, 200, JSON.stringify(response.body));
}

const terminals = (events) => events.filter((event) => event.type.startsWith("run.") && event.type !== "run.started").map((event) => `${event.type}:${event.run_id}`);

test("a later run waits while a run is open, and a late tool call of the open run keeps its run", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-a");
  const sessionID = conversation.private_session_id;
  const session = eve.session(sessionID);
  const first = eve.startTurn(sessionID);
  assert.equal(await toolCall(sessionID, first), "run_1");
  await turn(eve, conversation, "run_2", "second");
  await sleep(250);
  assert.equal(session.queue.length, 0, "the later run is not sent while the first is open");
  assert.equal(await toolCall(sessionID, first), "run_1", "a late callback of the open run keeps its run");
  eve.complete(sessionID, first);
  await until(() => session.queue.length === 1, "the later run is sent after the first closes");
  const second = eve.startTurn(sessionID);
  assert.equal(await toolCall(sessionID, second), "run_2");
  const opened = stream(eve, sessionID);
  eve.complete(sessionID, second);
  assert.deepEqual(terminals(await opened), ["run.completed:run_1", "run.completed:run_2"]);
});

test("an approval resumes its own run in a new turn while a later run waits, and resolves only for that run", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-b");
  const sessionID = conversation.private_session_id;
  const session = eve.session(sessionID);
  const first = eve.startTurn(sessionID);
  eve.park(sessionID, first, "approval_1");
  assert.deepEqual(terminals(await stream(eve, sessionID)), [], "a run parked on approval has not ended");
  await turn(eve, conversation, "run_2", "second");
  await sleep(250);
  assert.equal(session.queue.length, 0, "the later run waits for the approval");
  const wrongRun = await control(eve, "approval.resolve", { private_session_id: sessionID, continuation_token: conversation.continuation_token, run_id: "run_2", approval_id: "approval_1", decision: "allow" });
  assert.equal(wrongRun.status, 404, "an approval is resolved only for the run that requested it");
  const resolved = await control(eve, "approval.resolve", { private_session_id: sessionID, continuation_token: conversation.continuation_token, run_id: "run_1", approval_id: "approval_1", decision: "allow" });
  assert.equal(resolved.status, 200, JSON.stringify(resolved.body));
  eve.resolve(sessionID, "approval_1", first);
  assert.equal(await toolCall(sessionID, first), "run_1", "the approved call runs in the original turn");
  const continued = eve.continueTurn(sessionID);
  assert.equal(await toolCall(sessionID, continued), "run_1", "the continuation turn belongs to the approved run");
  eve.complete(sessionID, continued);
  await until(() => session.queue.length === 1, "the later run is sent after the approved run closes");
  const second = eve.startTurn(sessionID);
  assert.equal(await toolCall(sessionID, second), "run_2");
  const opened = stream(eve, sessionID);
  eve.complete(sessionID, second);
  assert.deepEqual(terminals(await opened), ["run.completed:run_1", "run.completed:run_2"]);
});

test("cancelling a queued run never sends it, and cancelling a parked run denies its approvals", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-f");
  const sessionID = conversation.private_session_id;
  const session = eve.session(sessionID);
  const first = eve.startTurn(sessionID);
  eve.park(sessionID, first, "approval_1");
  await turn(eve, conversation, "run_2", "second");
  const queued = await control(eve, "run.cancel", { private_session_id: sessionID, continuation_token: conversation.continuation_token, run_id: "run_2" });
  assert.equal(queued.status, 200, JSON.stringify(queued.body));
  const parked = await control(eve, "run.cancel", { private_session_id: sessionID, continuation_token: conversation.continuation_token, run_id: "run_1" });
  assert.equal(parked.status, 200, JSON.stringify(parked.body));
  assert.deepEqual(eve.responses, [{ requestId: "approval_1", optionId: "cancel" }]);
  eve.resolve(sessionID, "approval_1", first);
  const continued = eve.continueTurn(sessionID);
  const opened = stream(eve, sessionID);
  eve.complete(sessionID, continued);
  assert.deepEqual(terminals(await opened), ["run.cancelled:run_2", "run.cancelled:run_1"]);
  await sleep(250);
  assert.equal(session.queue.length, 0, "a cancelled queued run is never sent");
});

test("a turn without evidence of its run is never attributed and its events are never published", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-c");
  const sessionID = conversation.private_session_id;
  eve.complete(sessionID, eve.startTurn(sessionID));
  const foreign = eve.startForeignTurn(sessionID, "not sent by Scenery");
  assert.match(await toolCall(sessionID, foreign), /^refused: /);
  const opened = stream(eve, sessionID);
  eve.complete(sessionID, foreign);
  const events = await opened;
  assert.ok(events.every((event) => event.run_id === "run_1"), JSON.stringify(events));
});

test("a refused send leaves no pending run, so the next run binds its own turn", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-d");
  const sessionID = conversation.private_session_id;
  eve.complete(sessionID, eve.startTurn(sessionID));
  await stream(eve, sessionID);
  const operations = eve.operations();
  const refusing = { ...operations, attachSession: (id) => ({ ...eve.handle(id), send: async () => ({ status: "session_not_active" }) }) };
  const refused = await control(eve, "conversation.turn", { private_session_id: sessionID, continuation_token: conversation.continuation_token, run_id: "run_refused", message: "same text" }, refusing);
  assert.equal(refused.status, 500);
  await turn(eve, conversation, "run_2", "same text");
  const second = eve.startTurn(sessionID);
  assert.equal(await toolCall(sessionID, second), "run_2");
});

test("concurrent streams and tool calls read one history with unique sequences and stable run IDs", async () => {
  const eve = new FakeEve();
  const conversation = await created(eve, "first", "run_1", "sha256:conversation-e");
  const sessionID = conversation.private_session_id;
  const session = eve.session(sessionID);
  const first = eve.startTurn(sessionID);
  const pendingStreams = [stream(eve, sessionID), stream(eve, sessionID)];
  const calls = [toolCall(sessionID, first), toolCall(sessionID, first)];
  await turn(eve, conversation, "run_2", "second");
  assert.deepEqual(await Promise.all(calls), ["run_1", "run_1"]);
  eve.complete(sessionID, first);
  // Streams follow the session until it settles, which includes the queued run.
  await until(() => session.queue.length === 1, "the second run is sent");
  const second = eve.startTurn(sessionID);
  eve.complete(sessionID, second);
  const [left, right] = await Promise.all(pendingStreams);
  assert.deepEqual(left, right, "overlapping streams read the same history");
  assert.deepEqual(left.map((event) => event.sequence), left.map((_, index) => index + 1), "sequences are unique and contiguous");
  assert.deepEqual(terminals(left), ["run.completed:run_1", "run.completed:run_2"]);
  const all = await stream(eve, sessionID);
  assert.deepEqual(all, left, "a later stream of the settled session replays the same history");
});
