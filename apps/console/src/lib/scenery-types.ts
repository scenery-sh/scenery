export interface AppSummary {
  id: string
  name: string
  app_root: string
  offline: boolean
  session_id?: string
  sessionStatus?: string
  sessionStatusReason?: string
  compileError?: string
}

export interface AppStatus {
  running: boolean
  appID: string
  appRoot: string
  pid?: string
  sessionID?: string
  routes?: Record<string, string>
  aliases?: Record<string, string>
  sessionStatus?: string
  sessionStatusReason?: string
  compiling: boolean
  compileError?: string
  grafana?: {
    available: boolean
    status: string
    url?: string
    overview_url?: string
    datasource_status?: Record<string, string>
    datasources?: Record<string, string>
    message?: string
  }
  observability?: {
    backend?: string
    metrics?: ObservabilitySignal
    logs?: ObservabilitySignal
    traces?: ObservabilitySignal
  }
  meta?: {
    svcs?: Array<{ name: string; rel_path?: string; rpcs?: unknown[] }>
    cron_jobs?: unknown[]
    sql_databases?: SQLDatabase[]
  }
}

export interface SQLDatabase {
  name: string
  file_label?: string
  path?: string
  url?: string
  size_bytes?: number
  exists?: boolean
}

export interface SQLiteTable {
  name: string
  type: string
}

export interface SQLiteColumn {
  name: string
  type: string
  not_null: boolean
  primary_key: boolean
}

export interface SQLiteRows {
  columns: string[]
  rows: unknown[][]
  limit: number
  offset: number
}

export interface ObservabilitySignal {
  enabled: boolean
  available: boolean
  status: string
  url?: string
  query_path?: string
  dialect?: string
}

export interface DevSource {
  id: string
  kind?: string
  name?: string
  role?: string
  pid?: string
  stream?: string
  status?: string
}

export interface DevLogEntry {
  id: number
  time: string
  session_id?: string
  source: DevSource
  level: string
  message: string
  raw?: string
  fields?: unknown
  parse: {
    format: string
    ok: boolean
  }
}

export interface TraceSummary {
  trace_id: string
  span_id: string
  type: string
  is_error: boolean
  started_at: string
  duration_nanos: number
  service_name?: string
  endpoint_name?: string | null
}

export interface TraceEvent {
  event_id?: string
  event_time?: string
  span_id?: string
  span_start?: unknown
  span_event?: unknown
  span_end?: unknown
}

export interface DashboardNotification {
  method: string
  params: unknown
}
