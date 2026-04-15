import { DashboardRpcClient, type WebSocketLike } from "./rpc";

class FakeSocket extends EventTarget implements WebSocketLike {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 3;

  readyState = FakeSocket.CONNECTING;
  sent: string[] = [];

  open() {
    this.readyState = FakeSocket.OPEN;
    this.dispatchEvent(new Event("open"));
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeSocket.CLOSED;
    this.dispatchEvent(new Event("close"));
  }

  message(data: unknown) {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(data) }));
  }
}

describe("dashboard rpc client", () => {
  it("queues requests until the socket opens and resolves responses", async () => {
    const socket = new FakeSocket();
    const client = new DashboardRpcClient("ws://example.test/__pulse", () => socket);
    const pending = client.request<{ ok: boolean }>("status", { app_id: "app-test" });

    expect(socket.sent).toHaveLength(0);

    client.connect();
    socket.open();

    expect(socket.sent).toHaveLength(1);
    socket.message({ jsonrpc: "2.0", id: 1, result: { ok: true } });

    await expect(pending).resolves.toEqual({ ok: true });
  });

  it("emits notifications", () => {
    const socket = new FakeSocket();
    const client = new DashboardRpcClient("ws://example.test/__pulse", () => socket);
    const seen: string[] = [];
    client.subscribe((notification) => {
      seen.push(notification.method);
    });

    client.connect();
    socket.open();
    socket.message({ jsonrpc: "2.0", method: "trace/new", params: { trace_id: "abc" } });

    expect(seen).toEqual(["trace/new"]);
  });
});
