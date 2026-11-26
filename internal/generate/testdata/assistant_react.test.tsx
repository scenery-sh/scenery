import { afterEach, expect, test } from "bun:test";
import { createElement, StrictMode } from "react";
import { act, create, type ReactTestRenderer } from "../../../tools/typescript/node_modules/react-test-renderer";
import { useSceneryAssistant, type SceneryAssistantState } from "../../uigen/testdata/assistant/react/assistants.generated";
import type { AssistantConversationClient, AssistantTransportOptions, AssistantEvent } from "../../uigen/testdata/assistant/assistant";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const mounted: ReactTestRenderer[] = [];
afterEach(async () => { await act(async () => { for (const root of mounted.splice(0)) root.unmount(); }); });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function fakeClient() {
  const creates: ReturnType<typeof deferred<{ conversationId: string; runId: string; eventsUrl: string }>>[] = [];
  const turns: ReturnType<typeof deferred<{ runId: string }>>[] = [];
  const streams: { conversationId: string; signal?: AbortSignal; next: ReturnType<typeof deferred<IteratorResult<AssistantEvent>>> }[] = [];
  const client: AssistantConversationClient = {
    createConversation() { const request = deferred<{ conversationId: string; runId: string; eventsUrl: string }>(); creates.push(request); return request.promise; },
    sendTurn() { const request = deferred<{ runId: string }>(); turns.push(request); return request.promise; },
    streamEvents(conversationId, options) {
      const stream = { conversationId, signal: options?.signal, next: deferred<IteratorResult<AssistantEvent>>() };
      streams.push(stream);
      return { [Symbol.asyncIterator]() { return { next: async () => { const result = await stream.next.promise; stream.next = deferred<IteratorResult<AssistantEvent>>(); return result; } }; } };
    },
    async resolveApproval() {}, async cancelRun() {},
  };
  return { client, creates, turns, streams };
}
async function mount(client?: AssistantConversationClient, transport?: AssistantTransportOptions) {
  let state!: SceneryAssistantState;
  function View(props: { client?: AssistantConversationClient; transport?: AssistantTransportOptions }) {
    state = useSceneryAssistant("support", props.client, props.transport);
    return createElement("output", null, `${state.pending ? "pending" : "idle"}:${state.conversationId ?? "none"}`);
  }
  let root!: ReactTestRenderer;
  await act(async () => { root = create(createElement(StrictMode, null, createElement(View, { client, transport }))); });
  mounted.push(root);
  return { state: () => state, text: () => root.toJSON(), update: async (client?: AssistantConversationClient, transport?: AssistantTransportOptions) => {
    await act(async () => { root.update(createElement(StrictMode, null, createElement(View, { client, transport }))); });
  } };
}
const conversation = (id: string) => ({ conversationId: id, runId: `run-${id}`, eventsUrl: "/events" });
async function open(view: Awaited<ReturnType<typeof mount>>, fake: ReturnType<typeof fakeClient>, id = "first") {
  let pending!: Promise<void>;
  await act(async () => { pending = view.state().createConversation(id); });
  await act(async () => { fake.creates.at(-1)!.resolve(conversation(id)); await pending; });
}

for (const order of [[0, 1], [1, 0]]) {
  test(`pending counts overlapping requests settled ${order}`, async () => {
    const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
    const requests: Promise<void>[] = [];
    await act(async () => { requests.push(view.state().sendTurn("A"), view.state().sendTurn("B")); });
    expect(view.state().pending).toBe(true);
    await act(async () => { fake.turns[order[0]].resolve({ runId: `run-${order[0]}` }); await requests[order[0]]; });
    expect(view.text()).toMatchObject({ children: ["pending:first"] });
    await act(async () => { fake.turns[order[1]].resolve({ runId: `run-${order[1]}` }); await requests[order[1]]; });
    expect(view.state().pending).toBe(false);
    expect(view.state().runId).toBe("run-1");
  });
}
test("rejection settles only its request and preserves promise rejection", async () => {
  const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
  let first!: Promise<unknown>; let second!: Promise<void>;
  await act(async () => { first = view.state().sendTurn("A").catch(e => e); second = view.state().sendTurn("B"); });
  const failure = new Error("failed A");
  await act(async () => { fake.turns[0].reject(failure); expect(await first).toBe(failure); });
  expect(view.state().pending).toBe(true);
  await act(async () => { fake.turns[1].resolve({ runId: "B" }); await second; });
  expect(view.state().pending).toBe(false);
});
for (const fail of [false, true]) {
  test(`client replacement ignores old ${fail ? "failure" : "success"}`, async () => {
    const old = fakeClient(); const next = fakeClient(); const view = await mount(old.client);
    let request!: Promise<unknown>;
    await act(async () => { request = view.state().createConversation("old").catch(e => e); });
    await view.update(next.client);
    expect(view.text()).toMatchObject({ children: ["idle:none"] });
    await open(view, next, "new");
    await act(async () => { if (fail) old.creates[0].reject(new Error("old failure")); else old.creates[0].resolve(conversation("old")); await request; });
    expect(view.state().conversationId).toBe("new");
    expect(view.state().error).toBeUndefined();
    expect(next.streams.every(s => s.conversationId === "new")).toBe(true);
  });
}
test("latest creation wins and replaced conversation turns cannot overwrite it", async () => {
  const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
  let turn!: Promise<void>; let older!: Promise<void>; let newer!: Promise<void>;
  await act(async () => { turn = view.state().sendTurn("old turn"); older = view.state().createConversation("older"); newer = view.state().createConversation("newer"); });
  await act(async () => { fake.creates[2].resolve(conversation("newer")); await newer; });
  expect(fake.streams[0].signal?.aborted).toBe(true);
  await act(async () => { fake.creates[1].resolve(conversation("older")); fake.turns[0].resolve({ runId: "old turn" }); await Promise.all([older, turn]); });
  expect(view.state().conversationId).toBe("newer"); expect(view.state().runId).toBe("run-newer"); expect(view.state().pending).toBe(false);
});
test("equivalent transport objects preserve client and pending conversation", async () => {
  const response = deferred<Response>();
  const fetcher = (() => response.promise) as typeof fetch;
  const view = await mount(undefined, { baseUrl: "https://app.test", fetch: fetcher, defaultHeaders: { a: "1", b: "2" }, authentication: { credentials: "include" } });
  const client = view.state().client;
  let pending!: Promise<void>;
  await act(async () => { pending = view.state().createConversation("hello"); });
  await view.update(undefined, { baseUrl: "https://app.test", fetch: fetcher, defaultHeaders: { b: "2", a: "1" }, authentication: { credentials: "include" } });
  expect(view.state().client).toBe(client); expect(view.state().pending).toBe(true);
  await act(async () => { response.resolve(new Response("failure", { status: 500 })); await pending.catch(() => {}); });
  expect(view.state().pending).toBe(false);
});
test("unmount aborts the stream and late completion cannot affect another mount", async () => {
  const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
  let pending!: Promise<void>;
  await act(async () => { pending = view.state().sendTurn("late"); mounted.pop()!.unmount(); });
  expect(fake.streams[0].signal?.aborted).toBe(true);
  const replacement = await mount(fake.client);
  await act(async () => { fake.turns[0].resolve({ runId: "late" }); await pending; });
  expect(replacement.state().conversationId).toBeUndefined();
});

test("stream events deduplicate and an obsolete stream cannot update the new conversation", async () => {
  const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
  const event: AssistantEvent = { type: "assistant.run.started", assistant: "support", conversationId: "first", runId: "stream-run", sequence: 1, occurredAt: "2026-11-16T00:00:00Z", data: { state: "started" } };
  await act(async () => { fake.streams[0].next.resolve({ done: false, value: event }); });
  await act(async () => { fake.streams[0].next.resolve({ done: false, value: event }); });
  expect(view.state().events).toHaveLength(1);
  await open(view, fake, "second");
  await act(async () => { fake.streams[0].next.resolve({ done: false, value: { ...event, sequence: 2 } }); });
  expect(view.state().events).toHaveLength(0); expect(view.state().runId).toBe("run-second");
});
test("turn failure from a replaced conversation cannot overwrite its error state", async () => {
  const fake = fakeClient(); const view = await mount(fake.client); await open(view, fake);
  let pending!: Promise<unknown>;
  await act(async () => { pending = view.state().sendTurn("old").catch(e => e); });
  await open(view, fake, "second");
  await act(async () => { fake.turns[0].reject(new Error("obsolete")); await pending; });
  expect(view.state().error).toBeUndefined(); expect(view.state().conversationId).toBe("second");
});
