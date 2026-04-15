import type { DashboardNotification } from "./types";

type SocketFactory = (url: string) => WebSocketLike;

type Listener = (notification: DashboardNotification) => void;
type ConnectionListener = (connected: boolean) => void;

interface PendingRequest {
  resolve: (value: any) => void;
  reject: (error: Error) => void;
}

export interface WebSocketLike extends EventTarget {
  readyState: number;
  send(data: string): void;
  close(): void;
}

export class DashboardRpcClient {
  private socket: WebSocketLike | null = null;
  private nextID = 1;
  private readonly pending = new Map<number, PendingRequest>();
  private readonly listeners = new Set<Listener>();
  private readonly connectionListeners = new Set<ConnectionListener>();
  private readonly queue: string[] = [];
  private reconnectTimer: number | null = null;
  private disposed = false;

  constructor(
    private readonly url: string,
    private readonly socketFactory: SocketFactory = (value) => new WebSocket(value),
  ) {}

  connect(): void {
    if (this.disposed || this.socket) {
      return;
    }
    const socket = this.socketFactory(this.url);
    this.socket = socket;
    socket.addEventListener("open", this.handleOpen);
    socket.addEventListener("close", this.handleClose);
    socket.addEventListener("message", this.handleMessage as EventListener);
    socket.addEventListener("error", this.handleError);
  }

  dispose(): void {
    this.disposed = true;
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.socket) {
      this.socket.close();
      this.socket = null;
    }
    for (const [id, pending] of this.pending) {
      pending.reject(new Error(`request ${id} canceled`));
    }
    this.pending.clear();
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  subscribeConnection(listener: ConnectionListener): () => void {
    this.connectionListeners.add(listener);
    return () => this.connectionListeners.delete(listener);
  }

  async request<T>(method: string, params?: unknown): Promise<T> {
    this.connect();
    const id = this.nextID++;
    const payload = JSON.stringify({
      jsonrpc: "2.0",
      id,
      method,
      params: params ?? {},
    });
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.send(payload);
    });
  }

  private send(payload: string): void {
    const socket = this.socket;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      this.queue.push(payload);
      return;
    }
    socket.send(payload);
  }

  private readonly handleOpen = () => {
    for (const listener of this.connectionListeners) {
      listener(true);
    }
    while (this.queue.length > 0) {
      const next = this.queue.shift();
      if (next) {
        this.socket?.send(next);
      }
    }
  };

  private readonly handleClose = () => {
    this.socket = null;
    for (const listener of this.connectionListeners) {
      listener(false);
    }
    if (this.disposed) {
      return;
    }
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, 1000);
  };

  private readonly handleError = () => {
    for (const [id, pending] of this.pending) {
      pending.reject(new Error(`rpc request ${id} failed`));
    }
    this.pending.clear();
  };

  private readonly handleMessage = (event: MessageEvent<string>) => {
    const message = JSON.parse(event.data) as {
      id?: number;
      result?: unknown;
      error?: { message?: string };
      method?: string;
      params?: unknown;
    };
    if (typeof message.method === "string") {
      const notification: DashboardNotification = {
        method: message.method,
        params: message.params,
      };
      for (const listener of this.listeners) {
        listener(notification);
      }
      return;
    }
    if (typeof message.id !== "number") {
      return;
    }
    const pending = this.pending.get(message.id);
    if (!pending) {
      return;
    }
    this.pending.delete(message.id);
    if (message.error) {
      pending.reject(new Error(message.error.message || "rpc error"));
      return;
    }
    pending.resolve(message.result);
  };
}
