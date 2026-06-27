import type { DashboardNotification } from '@/lib/scenery-types'

type Listener = (notification: DashboardNotification) => void
type ConnectionListener = (connected: boolean) => void

interface PendingRequest {
  resolve: (value: unknown) => void
  reject: (error: Error) => void
}

export function sceneryWebSocketURL(): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/__scenery`
}

export class SceneryRpcClient {
  private readonly url: string
  private socket: WebSocket | null = null
  private nextID = 1
  private disposed = false
  private reconnectTimer: number | null = null
  private readonly pending = new Map<number, PendingRequest>()
  private readonly queue: string[] = []
  private readonly listeners = new Set<Listener>()
  private readonly connectionListeners = new Set<ConnectionListener>()

  constructor(url = sceneryWebSocketURL()) {
    this.url = url
  }

  connect(): void {
    if (this.disposed || this.socket) {
      return
    }
    const socket = new WebSocket(this.url)
    this.socket = socket
    socket.addEventListener('open', this.handleOpen)
    socket.addEventListener('close', this.handleClose)
    socket.addEventListener('message', this.handleMessage)
    socket.addEventListener('error', this.handleError)
  }

  dispose(): void {
    this.disposed = true
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer)
    }
    this.socket?.close()
    this.socket = null
    for (const [id, pending] of this.pending) {
      pending.reject(new Error(`request ${id} canceled`))
    }
    this.pending.clear()
  }

  request<T>(method: string, params: unknown = {}): Promise<T> {
    this.connect()
    const id = this.nextID++
    const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params })
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: (value) => resolve(value as T), reject })
      this.send(payload)
    })
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  subscribeConnection(listener: ConnectionListener): () => void {
    this.connectionListeners.add(listener)
    return () => this.connectionListeners.delete(listener)
  }

  private send(payload: string): void {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      this.queue.push(payload)
      return
    }
    this.socket.send(payload)
  }

  private readonly handleOpen = () => {
    for (const listener of this.connectionListeners) {
      listener(true)
    }
    while (this.queue.length > 0) {
      const payload = this.queue.shift()
      if (payload) {
        this.socket?.send(payload)
      }
    }
  }

  private readonly handleClose = () => {
    this.socket = null
    for (const listener of this.connectionListeners) {
      listener(false)
    }
    if (!this.disposed) {
      this.reconnectTimer = window.setTimeout(() => {
        this.reconnectTimer = null
        this.connect()
      }, 1000)
    }
  }

  private readonly handleError = () => {
    for (const [id, pending] of this.pending) {
      pending.reject(new Error(`rpc request ${id} failed`))
    }
    this.pending.clear()
  }

  private readonly handleMessage = (event: MessageEvent<string>) => {
    const message = JSON.parse(event.data) as {
      id?: number
      result?: unknown
      error?: { message?: string }
      method?: string
      params?: unknown
    }
    if (message.method) {
      for (const listener of this.listeners) {
        listener({ method: message.method, params: message.params })
      }
      return
    }
    if (typeof message.id !== 'number') {
      return
    }
    const pending = this.pending.get(message.id)
    if (!pending) {
      return
    }
    this.pending.delete(message.id)
    if (message.error) {
      pending.reject(new Error(message.error.message || 'rpc error'))
    } else {
      pending.resolve(message.result)
    }
  }
}
