export type DashboardEvent = {
  method: string
  params: unknown
}

type PendingCall = {
  resolve: (value: unknown) => void
  reject: (error: Error) => void
}

type EventHandler = (event: DashboardEvent) => void
type ConnectionHandler = (connected: boolean) => void

export function dashboardSocketURL(): string {
  const configured = import.meta.env.VITE_SCENERY_DASHBOARD_WS_URL
  if (configured) {
    return configured
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${dashboardPathPrefix()}/__scenery`
}

function dashboardPathPrefix(): string {
  const path = window.location.pathname
  if (path === '/consolenext' || path.startsWith('/consolenext/')) {
    return '/consolenext'
  }
  return ''
}

export class DashboardRPC {
  private readonly url: string
  private socket: WebSocket | null = null
  private retryTimer: number | null = null
  private nextID = 1
  private closed = false
  private readonly pending = new Map<number, PendingCall>()
  private readonly outbox: string[] = []
  private readonly eventHandlers = new Set<EventHandler>()
  private readonly connectionHandlers = new Set<ConnectionHandler>()

  constructor(url = dashboardSocketURL()) {
    this.url = url
  }

  connect(): void {
    if (this.socket !== null) {
      return
    }
    this.closed = false
    const socket = new WebSocket(this.url)
    this.socket = socket
    socket.addEventListener('open', this.handleOpen)
    socket.addEventListener('close', this.handleClose)
    socket.addEventListener('message', this.handleMessage)
    socket.addEventListener('error', this.handleError)
  }

  dispose(): void {
    this.closed = true
    if (this.retryTimer !== null) {
      window.clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    const socket = this.socket
    this.socket = null
    socket?.close()
    this.rejectAll(new Error('dashboard connection closed'))
  }

  call<T>(method: string, params: unknown = {}): Promise<T> {
    this.connect()
    const id = this.nextID
    this.nextID += 1
    const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params })
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: (value) => resolve(value as T), reject })
      this.send(payload)
    })
  }

  listStoredRequests(appID: string): Promise<StoredRequest[]> {
    return this.call<StoredRequest[]>('stored-requests/list', { app_id: appID })
  }

  createStoredRequest(appID: string, input: StoredRequestInput): Promise<string> {
    return this.call<string>('stored-requests/create', { app_id: appID, input })
  }

  updateStoredRequest(appID: string, id: string, input: StoredRequestInput): Promise<string> {
    return this.call<string>('stored-requests/update', { app_id: appID, id, input })
  }

  deleteStoredRequest(appID: string, id: string): Promise<boolean> {
    return this.call<boolean>('stored-requests/delete', { app_id: appID, id })
  }

  symphonyState(appID: string): Promise<SymphonyState> {
    return this.call<SymphonyState>('symphony/state', { app_id: appID })
  }

  symphonyCreateTask(appID: string, input: SymphonyTaskInput): Promise<SymphonyTask> {
    return this.call<SymphonyTask>('symphony/task/create', { app_id: appID, input })
  }

  symphonyUpdateTask(appID: string, id: string, input: SymphonyTaskInput): Promise<SymphonyTask> {
    return this.call<SymphonyTask>('symphony/task/update', { app_id: appID, id, input })
  }

  symphonyMoveTask(appID: string, id: string, statusKey: string, index: number): Promise<SymphonyState> {
    return this.call<SymphonyState>('symphony/task/move', { app_id: appID, id, status_key: statusKey, index })
  }

  symphonyDeleteTask(appID: string, id: string): Promise<boolean> {
    return this.call<boolean>('symphony/task/delete', { app_id: appID, id })
  }

  symphonyUpdateStatuses(appID: string, statuses: SymphonyStatusUpdate[]): Promise<SymphonyState> {
    return this.call<SymphonyState>('symphony/statuses/update', { app_id: appID, statuses })
  }

  symphonyUpdateWorkflow(appID: string, input: SymphonyWorkflowInput): Promise<SymphonyWorkflow> {
    return this.call<SymphonyWorkflow>('symphony/workflow/update', { app_id: appID, input })
  }

  symphonyRunDetail(appID: string, runID: string): Promise<SymphonyRunDetail> {
    return this.call<SymphonyRunDetail>('symphony/run/detail', { app_id: appID, run_id: runID })
  }

  onEvent(handler: EventHandler): () => void {
    this.eventHandlers.add(handler)
    return () => this.eventHandlers.delete(handler)
  }

  onConnection(handler: ConnectionHandler): () => void {
    this.connectionHandlers.add(handler)
    return () => this.connectionHandlers.delete(handler)
  }

  private send(payload: string): void {
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.socket.send(payload)
      return
    }
    this.outbox.push(payload)
  }

  private emitConnection(connected: boolean): void {
    for (const handler of this.connectionHandlers) {
      handler(connected)
    }
  }

  private rejectAll(error: Error): void {
    for (const request of this.pending.values()) {
      request.reject(error)
    }
    this.pending.clear()
  }

  private readonly handleOpen = (event: Event) => {
    if (event.target !== this.socket) {
      return
    }
    this.emitConnection(true)
    while (this.outbox.length > 0 && this.socket?.readyState === WebSocket.OPEN) {
      const payload = this.outbox.shift()
      if (payload !== undefined) {
        this.socket.send(payload)
      }
    }
  }

  private readonly handleClose = (event: CloseEvent) => {
    if (event.target !== this.socket) {
      return
    }
    this.socket = null
    this.emitConnection(false)
    this.rejectAll(new Error('dashboard connection closed'))
    if (!this.closed) {
      this.retryTimer = window.setTimeout(() => {
        this.retryTimer = null
        this.connect()
      }, 1000)
    }
  }

  private readonly handleError = (event: Event) => {
    if (event.target === this.socket) {
      this.rejectAll(new Error('dashboard connection failed'))
    }
  }

  private readonly handleMessage = (event: MessageEvent<string>) => {
    if (event.target !== this.socket) {
      return
    }
    const message = JSON.parse(event.data) as {
      id?: number
      result?: unknown
      error?: { message?: string }
      method?: string
      params?: unknown
    }
    if (message.method !== undefined) {
      for (const handler of this.eventHandlers) {
        handler({ method: message.method, params: message.params })
      }
      return
    }
    if (typeof message.id !== 'number') {
      return
    }
    const request = this.pending.get(message.id)
    if (request === undefined) {
      return
    }
    this.pending.delete(message.id)
    if (message.error !== undefined) {
      request.reject(new Error(message.error.message ?? 'dashboard rpc failed'))
      return
    }
    request.resolve(message.result)
  }
}

export type AppSummary = {
  id: string
  name?: string
  app_root?: string
  base_app_id?: string
  session_id?: string
  offline?: boolean
  sessionStatus?: string
  sessionStatusReason?: string
  compileError?: string
}

export type AppStatus = {
  running: boolean
  appID: string
  baseAppID?: string
  runtimeAppID?: string
  sessionID?: string
  appRoot: string
  pid?: string
  addr?: string
  routes?: Record<string, string>
  aliases?: Record<string, string>
  sessionStatus?: string
  sessionStatusReason?: string
  compiling: boolean
  compileError?: string
  observability?: {
    enabled?: boolean
    backend?: string
    message?: string
    scope?: {
      app_id?: string
      session_id?: string
      branch?: string
      worktree?: string
    }
    metrics?: ObservabilitySignal
    logs?: ObservabilitySignal
    traces?: ObservabilitySignal
  }
  meta?: DashboardMeta
  apiEncoding?: APIEncoding
}

export type DashboardMeta = {
  module_path?: string
  svcs?: ServiceMeta[]
  cron_jobs?: CronJob[]
  middleware?: MiddlewareMeta[]
  sql_databases?: SQLDatabase[]
  auth_handler?: AuthHandlerMeta
}

export type APIEncoding = {
  services?: Array<{
    name: string
    rpcs: Array<{
      name: string
      path: string
      methods?: string[]
      raw?: boolean
      access_type?: string
      service_name?: string
    }>
  }>
}

export type MetadataPath = {
  type?: string
  segments?: Array<{ type: 'LITERAL' | 'PARAM'; value: string; value_type?: string }>
}

export type ServiceMeta = {
  name: string
  rel_path?: string
  rpcs?: ServiceRPC[]
}

export type ServiceRPC = {
  name: string
  access_type?: string
  proto?: string
  path?: MetadataPath
  loc?: {
    pkg_path?: string
    filename?: string
    src_line_start?: number
    src_col_start?: number
  }
  http_methods?: string[]
  request_schema?: unknown
  response_schema?: unknown
}

export type MiddlewareMeta = {
  name: { pkg: string; name: string }
  global?: boolean
  service_name?: string
}

export type AuthHandlerMeta = {
  name: string
  pkg_path?: string
  pkg_name?: string
}

export type CronJob = {
  id: string
  title?: string
  schedule?: string
  every?: string
  endpoint?: {
    service_name?: string
    rpc_name?: string
  }
}

export type ObservabilitySignal = {
  enabled: boolean
  available: boolean
  status: string
  url?: string
  query_path?: string
  dialect?: string
  message?: string
}

export type SQLDatabase = {
  name: string
  source?: string
  schemas?: Array<{ service: string; schema: string }>
  file_label?: string
  path?: string
  url?: string
  size_bytes?: number
  exists?: boolean
}

export type PostgresTable = {
  name: string
  type: string
}

export type PostgresColumn = {
  name: string
  type: string
  not_null: boolean
  primary_key: boolean
}

export type PostgresRows = {
  columns: string[]
  rows: unknown[][]
  limit: number
  offset: number
}

export type DevLogEntry = {
  id: number
  time: string
  session_id?: string
  source: {
    id: string
    kind?: string
    name?: string
    role?: string
    pid?: string
    stream?: string
    status?: string
  }
  level: string
  message: string
  raw?: string
}

export type TraceSummary = {
  trace_id: string
  span_id: string
  type: string
  is_root?: boolean
  is_error: boolean
  started_at: string
  duration_nanos: number
  service_name?: string
  endpoint_name?: string | null
  message_id?: string | null
  parent_span_id?: string | null
}

export type ProcessOutput = {
  appID: string
  pid: string
  stream: string
  output: string
  created_at: string
}

export type ApiCallResponse = {
  status: string
  status_code: number
  body: string
  trace_id?: string
}

export type StoredRequest = {
  id: string
  title: string
  rpcName: string
  svcName: string
  shared: boolean
  data: {
    method: string
    pathParams: unknown
    payload: unknown
  }
}

export type StoredRequestInput = {
  title: string
  rpcName: string
  svcName: string
  shared: boolean
  data: {
    method: string
    pathParams: unknown
    payload: unknown
  }
}

export type SymphonyState = {
  statuses: SymphonyStatus[]
  tasks: SymphonyTask[]
  workflow: SymphonyWorkflow
}

export type SymphonyStatus = {
  key: string
  name: string
  kind: string
  sort_order: number
  hidden: boolean
  color: string
  created_at: string
  updated_at: string
}

export type SymphonyTask = {
  id: string
  app_id: string
  identifier: string
  title: string
  description: string
  status_key: string
  sort_order: number
  priority: string
  assignee: string
  estimate: string
  branch_name: string
  url: string
  source: string
  labels?: string[]
  latest_run?: SymphonyRun
  created_at: string
  updated_at: string
}

export type SymphonyTaskInput = {
  title: string
  description: string
  status_key: string
  priority: string
  assignee: string
  estimate: string
  branch_name: string
  url: string
  source: string
  labels: string[]
}

export type SymphonyStatusUpdate = {
  key: string
  sort_order: number
  hidden: boolean
}

export type SymphonyRun = {
  id: string
  app_id: string
  task_id: string
  status: string
  attempt: number
  repo_root?: string
  repo_workspace_path?: string
  workspace_path: string
  thread_id: string
  turn_id: string
  process_id: number
  owner_session_id: string
  owner_started_at?: string
  lease_expires_at?: string
  summary: string
  error: string
  diff_stat: string
  diff: string
  started_at?: string
  ended_at?: string
  created_at: string
  updated_at: string
}

export type SymphonyRunEvent = {
  app_id: string
  run_id: string
  seq: number
  type: string
  payload_json: unknown
  created_at: string
}

export type SymphonyRunDetail = {
  run: SymphonyRun
  events: SymphonyRunEvent[]
}

export type SymphonyWorkflow = {
  app_id: string
  workflow_markdown: string
  mode: string
  max_concurrency: number
  updated_at: string
}

export type SymphonyWorkflowInput = {
  workflow_markdown: string
  mode: 'manual' | 'auto'
  max_concurrency: number
}
