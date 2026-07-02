import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Bell,
  ChevronsUpDown,
  CircleCheck,
  Database,
  ExternalLink,
  GitBranch,
  ChevronLeft,
  ChevronRight,
  PlugZap,
  RefreshCcw,
  Terminal,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SceneryRpcClient } from '@/lib/scenery-rpc'
import type { AppStatus, AppSummary, DashboardNotification, DevLogEntry, SQLDatabase, SQLiteColumn, SQLiteRows, SQLiteTable, TraceEvent, TraceSummary } from '@/lib/scenery-types'
import { cn } from '@/lib/utils'

type Page = 'Overview' | 'Services' | 'Logs' | 'Traces' | 'Deployments'

const navItems: Page[] = ['Overview', 'Services', 'Logs', 'Traces', 'Deployments']
const preferredAppID = import.meta.env.VITE_SCENERY_APP_ID

function initialAppID(): string {
  return new URLSearchParams(window.location.search).get('app') || preferredAppID || ''
}

function formatTime(value?: string): string {
  if (!value) {
    return ''
  }
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString()
}

function formatDuration(nanos?: number): string {
  if (!nanos) {
    return '0 ms'
  }
  const ms = nanos / 1_000_000
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)} s`
  }
  if (ms >= 1) {
    return `${ms.toFixed(2)} ms`
  }
  return `${Math.round(nanos / 1000)} us`
}

function formatMegabytes(bytes?: number): string {
  if (typeof bytes !== 'number' || !Number.isFinite(bytes) || bytes <= 0) {
    return '0 MB'
  }
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(bytes / 1024 / 1024)} MB`
}

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 12)}...` : value
}

function recordValue(value: unknown, key: string): unknown {
  return value && typeof value === 'object' ? (value as Record<string, unknown>)[key] : undefined
}

function routeLabel(name: string): string {
  if (name === 'api') {
    return 'API'
  }
  if (name === 'console') {
    return 'Console'
  }
  return name.replace(/[-_]/g, ' ').replace(/\b\w/g, (char) => char.toUpperCase())
}

function databaseLabel(database: SQLDatabase): string {
  return database.file_label || database.name
}

function databaseKey(database: SQLDatabase): string {
  return database.path || database.file_label || database.name
}

function renderCellValue(value: unknown): string {
  if (value === null || value === undefined) {
    return 'NULL'
  }
  if (typeof value === 'object') {
    return JSON.stringify(value)
  }
  return String(value)
}

function appKey(app: AppSummary): string {
  return app.session_id || app.id
}

function worktreeLabel(app?: AppSummary | null): string {
  const value = app?.session_id || app?.id || ''
  const label = value.replace(/-[0-9a-f]{6,8}$/i, '')
  return label === value ? '' : label.replace(/^feat-/, 'feat/')
}

function appDisplayName(app?: AppSummary | null): string {
  const worktree = worktreeLabel(app)
  return app ? `${app.name}${worktree ? ` ${worktree}` : ''}` : 'No app selected'
}

function appStatusLabel(app?: AppSummary | null): string {
  if (!app) {
    return 'unknown'
  }
  return app.sessionStatus || (app.offline ? 'stale' : 'running')
}

function appStatusDotClass(app?: AppSummary | null): string {
  const status = appStatusLabel(app)
  if (status === 'running' || status === 'starting') {
    return 'bg-primary'
  }
  if (status.includes('error') || status.includes('failed') || status.includes('degraded')) {
    return 'bg-destructive'
  }
  return 'bg-muted-foreground'
}

function chooseApp(apps: AppSummary[], selectedAppID: string): AppSummary | undefined {
  return apps.find((app) => app.id === selectedAppID || app.session_id === selectedAppID)
    ?? apps.find((app) => !app.offline)
    ?? apps[0]
}

function App() {
  const [rpc] = useState(() => new SceneryRpcClient())
  const [connected, setConnected] = useState(false)
  const [page, setPage] = useState<Page>('Overview')
  const [apps, setApps] = useState<AppSummary[]>([])
  const [status, setStatus] = useState<AppStatus | null>(null)
  const [traces, setTraces] = useState<TraceSummary[]>([])
  const [logs, setLogs] = useState<DevLogEntry[]>([])
  const [notifications, setNotifications] = useState<DashboardNotification[]>([])
  const [error, setError] = useState('')
  const [appPickerOpen, setAppPickerOpen] = useState(false)
  const [selectedAppID, setSelectedAppID] = useState(initialAppID)
  const [selectedDatabaseKey, setSelectedDatabaseKey] = useState('')

  const activeApp = useMemo(() => chooseApp(apps, selectedAppID) ?? null, [apps, selectedAppID])

  const refresh = useCallback(async () => {
    setError('')
    const nextApps = await rpc.request<AppSummary[]>('list-apps')
    setApps(nextApps)
    const nextApp = chooseApp(nextApps, selectedAppID)
    if (!nextApp) {
      setStatus(null)
      setTraces([])
      setLogs([])
      return
    }
    const nextStatus = await rpc.request<AppStatus>('status', { app_id: nextApp.id })
    const [nextTraces, nextOutputs] = await Promise.all([
      rpc.request<TraceSummary[]>('traces/list', { app_id: nextApp.id }).catch(() => []),
      rpc.request<DevLogEntry[]>('logs/list', { app_id: nextApp.id, limit: 100 }).catch(() => []),
    ])
    setStatus(nextStatus)
    setTraces(nextTraces ?? [])
    setLogs(nextOutputs ?? [])
  }, [rpc, selectedAppID])

  const selectApp = useCallback((app: AppSummary) => {
    const nextID = appKey(app)
    const url = new URL(window.location.href)
    url.searchParams.set('app', nextID)
    window.history.pushState({}, '', url)
    setAppPickerOpen(false)
    setSelectedAppID(nextID)
    setStatus(null)
    setTraces([])
    setLogs([])
    setSelectedDatabaseKey('')
  }, [])

  const tailLogs = useCallback(async () => {
    if (!activeApp) {
      return
    }
    const afterID = logs.at(-1)?.id ?? 0
    const next = await rpc.request<DevLogEntry[]>('logs/list', {
      app_id: activeApp.id,
      after_id: afterID,
      limit: 100,
    }).catch(() => [])
    if (next.length > 0) {
      setLogs((current) => [...current, ...next].slice(-500))
    }
  }, [activeApp, logs, rpc])

  useEffect(() => {
    const unsubscribeConnection = rpc.subscribeConnection(setConnected)
    const unsubscribeNotifications = rpc.subscribe((notification) => {
      setNotifications((current) => [notification, ...current].slice(0, 5))
    })
    rpc.connect()
    void refresh().catch((err: unknown) => {
      setError(err instanceof Error ? err.message : String(err))
    })
    const timer = window.setInterval(() => {
      void refresh().catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err))
      })
    }, 5000)
    return () => {
      unsubscribeConnection()
      unsubscribeNotifications()
      window.clearInterval(timer)
      rpc.dispose()
    }
  }, [refresh, rpc])

  useEffect(() => {
    const timer = window.setInterval(() => {
      void tailLogs().catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err))
      })
    }, 1000)
    return () => window.clearInterval(timer)
  }, [tailLogs])

  const services = [
    {
      name: 'Dashboard WS',
      status: connected ? 'Connected' : 'Disconnected',
      value: connected ? 'live' : 'waiting',
    },
    {
      name: 'App API',
      status: status?.routes?.api ? 'Routed' : 'No route',
      value: status?.routes?.api ? 'ready' : 'unknown',
    },
    {
      name: 'Services',
      status: activeApp?.name ?? 'No app',
      value: String(status?.meta?.svcs?.length ?? 0),
    },
  ]

  const serviceLinks = useMemo(() => {
    const routes = Object.entries(status?.routes ?? {}).map(([name, url]) => ({
      key: `route:${name}`,
      name: routeLabel(name),
      kind: name === 'api' ? 'api' : ['grafana', 'temporal', 'console'].includes(name) ? 'platform' : 'frontend',
      href: url,
      detail: name,
    }))
    const aliases = Object.entries(status?.aliases ?? {})
      .filter(([name]) => !status?.routes?.[name])
      .map(([name, url]) => ({
        key: `alias:${name}`,
        name: `${routeLabel(name)} alias`,
        kind: 'alias',
        href: url,
        detail: name,
      }))
    return [...routes, ...aliases]
  }, [status])

  const sqliteDatabases = status?.meta?.sql_databases ?? []
  const selectedDatabase = sqliteDatabases.find((database) => databaseKey(database) === selectedDatabaseKey) ?? null

  const activity = logs.length > 0
    ? logs.slice(-5).reverse().map((item) => ({
        label: item.message || item.raw || `${item.source.id} event`,
        time: formatTime(item.time),
      }))
    : notifications.map((item) => ({ label: item.method, time: 'now' }))

  return (
    <main className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-10 border-b bg-background/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-4 px-4">
          <Popover open={appPickerOpen} onOpenChange={setAppPickerOpen}>
            <PopoverTrigger asChild>
              <Button variant="ghost" className="max-w-[18rem] justify-start gap-2 px-1 font-heading text-sm font-semibold">
                <span
                  aria-label={appStatusLabel(activeApp)}
                  className={cn('size-2 shrink-0 rounded-full', appStatusDotClass(activeApp))}
                />
                <span className="truncate">{appDisplayName(activeApp)}</span>
                <ChevronsUpDown className="ml-auto size-3.5 shrink-0 text-muted-foreground" />
              </Button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-96 p-0">
              <Command>
                <CommandInput placeholder="Filter apps..." />
                <CommandList>
                  <CommandEmpty>No apps found.</CommandEmpty>
                  <CommandGroup>
                    {apps.map((app) => {
                      const selected = appKey(app) === appKey(activeApp ?? app)
                      return (
                        <CommandItem
                          key={appKey(app)}
                          data-checked={selected}
                          value={`${app.name} ${app.id} ${app.session_id ?? ''} ${app.app_root}`}
                          onSelect={() => selectApp(app)}
                        >
                          <div className="flex min-w-0 items-start gap-2">
                            <span
                              aria-label={appStatusLabel(app)}
                              className={cn('mt-1.5 size-2 shrink-0 rounded-full', appStatusDotClass(app))}
                            />
                            <div className="min-w-0">
                              <div className="flex min-w-0 items-center gap-2">
                                <span className="truncate font-medium">{appDisplayName(app)}</span>
                                <span className="shrink-0 text-xs text-muted-foreground">{appStatusLabel(app)}</span>
                              </div>
                              <div className="truncate text-xs text-muted-foreground">{app.app_root}</div>
                            </div>
                          </div>
                        </CommandItem>
                      )
                    })}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
          <nav className="flex flex-1 items-center gap-1 overflow-x-auto" aria-label="Console">
            {navItems.map((item) => (
              <Button
                key={item}
                variant={item === page ? 'secondary' : 'ghost'}
                size="sm"
                className="shrink-0"
                onClick={() => setPage(item)}
              >
                {item}
              </Button>
            ))}
          </nav>
          <Button variant="ghost" size="icon-sm" aria-label="Notifications">
            <Bell />
          </Button>
          <Button variant="outline" size="sm">
            <GitBranch data-icon="inline-start" />
            Repo
          </Button>
        </div>
      </header>

      <section className="mx-auto flex max-w-6xl flex-col gap-4 px-4 py-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <Badge variant={connected ? 'default' : 'outline'}>
              {connected ? 'connected' : 'waiting for dashboard'}
            </Badge>
            <h1 className="mt-3 text-2xl font-semibold tracking-normal">
              {activeApp?.name ?? 'Development session'}
            </h1>
            {error ? <p className="mt-1 max-w-2xl text-sm text-destructive">{error}</p> : null}
          </div>
          <Button size="sm" onClick={() => void refresh().catch((err: unknown) => {
            setError(err instanceof Error ? err.message : String(err))
          })}>
            <RefreshCcw data-icon="inline-start" />
            Refresh
          </Button>
        </div>

        {page === 'Logs' ? (
          <LogsPage connected={connected} logs={logs} />
        ) : page === 'Traces' ? (
          <TracesPage activeApp={activeApp} rpc={rpc} traces={traces} />
        ) : (
          <>
            <Card>
              <CardHeader>
                <CardTitle>Observability</CardTitle>
                <CardDescription>
                  Telemetry signals for this session
                </CardDescription>
              </CardHeader>
              <CardContent className="grid gap-2 md:grid-cols-3">
                {[
                  { label: 'Metrics', signal: status?.observability?.metrics },
                  { label: 'Logs', signal: status?.observability?.logs },
                  { label: 'Traces', signal: status?.observability?.traces },
                ].map(({ label, signal }) => (
                  <div key={label} className="flex items-center gap-3 border p-3">
                    <Badge variant={signal?.available ? 'default' : 'outline'}>{signal?.status || 'unknown'}</Badge>
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium">{label}</div>
                      <div className="truncate text-xs text-muted-foreground">{signal?.dialect || 'not configured'}</div>
                    </div>
                    {signal?.url ? (
                      <Button variant="outline" size="sm" asChild>
                        <a href={signal.url} target="_blank" rel="noreferrer">
                          <ExternalLink data-icon="inline-start" />
                          Open
                        </a>
                      </Button>
                    ) : null}
                  </div>
                ))}
              </CardContent>
            </Card>

            <div className="grid gap-4 md:grid-cols-3">
              {services.map((service) => (
                <Card key={service.name}>
                  <CardHeader>
                    <CardTitle>{service.name}</CardTitle>
                    <CardDescription>{service.status}</CardDescription>
                    <CardAction>
                      {service.value === 'unknown' || service.value === 'waiting' ? (
                        <PlugZap className="size-4 text-muted-foreground" />
                      ) : (
                        <CircleCheck className="size-4 text-primary" />
                      )}
                    </CardAction>
                  </CardHeader>
                  <CardContent>
                    <div className="text-2xl font-semibold">{service.value}</div>
                    <p className="mt-1 text-xs text-muted-foreground">current signal</p>
                  </CardContent>
                </Card>
              ))}
            </div>

            <Card>
              <CardHeader>
                <CardTitle>SQLite Databases</CardTitle>
                <CardDescription>
                  {sqliteDatabases.length} database{sqliteDatabases.length === 1 ? '' : 's'}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-2">
                {sqliteDatabases.length === 0 ? (
                  <div className="text-sm text-muted-foreground">No SQLite databases discovered.</div>
                ) : (
                  sqliteDatabases.map((database) => (
                    <button
                      key={`${database.name}:${database.path ?? ''}`}
                      type="button"
                      className={cn(
                        'flex w-full items-start justify-between gap-4 border p-3 text-left hover:bg-muted/50',
                        selectedDatabaseKey === databaseKey(database) && 'bg-muted/50'
                      )}
                      onClick={() => setSelectedDatabaseKey(databaseKey(database))}
                    >
                      <div className="min-w-0">
                        <div className="flex items-center gap-2 truncate text-sm font-medium">
                          <Database className="size-4 shrink-0 text-muted-foreground" />
                          {databaseLabel(database)}
                        </div>
                        {database.path ? (
                          <div className="truncate text-xs text-muted-foreground">{database.path}</div>
                        ) : null}
                      </div>
                      <Badge variant={database.exists === false ? 'outline' : 'secondary'}>
                        {database.exists === false ? 'missing' : formatMegabytes(database.size_bytes)}
                      </Badge>
                    </button>
                  ))
                )}
              </CardContent>
            </Card>

            {selectedDatabase ? (
              <SQLiteBrowser activeApp={activeApp} rpc={rpc} database={selectedDatabase} />
            ) : null}

            <Card>
              <CardHeader>
                <CardTitle>Service Links</CardTitle>
                <CardDescription>Routes and app services for quick navigation</CardDescription>
              </CardHeader>
              <CardContent className="grid gap-2 md:grid-cols-2">
                {serviceLinks.length === 0 ? (
                  <div className="text-sm text-muted-foreground">No routes discovered yet.</div>
                ) : (
                  serviceLinks.map((service) => (
                    <div key={service.key} className="flex items-center gap-3 border p-3">
                      <Badge variant={service.kind === 'frontend' ? 'default' : 'secondary'}>{service.kind}</Badge>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">{service.name}</div>
                        <div className="truncate text-xs text-muted-foreground">{service.detail}</div>
                      </div>
                      {service.href ? (
                        <Button variant="outline" size="sm" asChild>
                          <a href={service.href} target="_blank" rel="noreferrer">
                            <ExternalLink data-icon="inline-start" />
                            Open
                          </a>
                        </Button>
                      ) : (
                        <Badge variant="outline">no link</Badge>
                      )}
                    </div>
                  ))
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Activity</CardTitle>
                <CardDescription>
                  {traces.length} trace{traces.length === 1 ? '' : 's'} loaded
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                {(activity.length > 0 ? activity : [{ label: 'No dashboard events yet', time: '' }]).map(
                  (event, index) => (
                    <div key={`${event.label}-${index}`} className="flex items-center gap-3 text-sm">
                      <Badge variant={index === 0 && activity.length > 0 ? 'default' : 'secondary'}>
                        {index + 1}
                      </Badge>
                      <span className="min-w-0 flex-1 truncate">{event.label}</span>
                      <Separator className="hidden flex-1 sm:block" />
                      <span className="shrink-0 text-xs text-muted-foreground">{event.time}</span>
                    </div>
                  )
                )}
              </CardContent>
            </Card>
          </>
        )}
      </section>
    </main>
  )
}

function SQLiteBrowser({
  activeApp,
  rpc,
  database,
}: {
  activeApp: AppSummary | null
  rpc: SceneryRpcClient
  database: SQLDatabase
}) {
  const [tables, setTables] = useState<SQLiteTable[]>([])
  const [selectedTable, setSelectedTable] = useState('')
  const [schema, setSchema] = useState<SQLiteColumn[]>([])
  const [rows, setRows] = useState<SQLiteRows | null>(null)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)
  const [dbError, setDBError] = useState('')
  const databaseID = database.path || database.name
  const appID = activeApp?.id ?? ''

  const openTable = useCallback(async (table: string, nextOffset = 0) => {
    if (!appID || !databaseID) {
      return
    }
    setLoading(true)
    setDBError('')
    setSelectedTable(table)
    setOffset(nextOffset)
    try {
      const [nextSchema, nextRows] = await Promise.all([
        rpc.request<SQLiteColumn[]>('sqlite/schema', {
          app_id: appID,
          database: databaseID,
          table,
        }),
        rpc.request<SQLiteRows>('sqlite/rows', {
          app_id: appID,
          database: databaseID,
          table,
          limit: 50,
          offset: nextOffset,
        }),
      ])
      setSchema(nextSchema ?? [])
      setRows(nextRows)
    } catch (err) {
      setDBError(err instanceof Error ? err.message : String(err))
      setSchema([])
      setRows(null)
    } finally {
      setLoading(false)
    }
  }, [appID, databaseID, rpc])

  useEffect(() => {
    if (!appID || !databaseID) {
      return
    }
    setTables([])
    setSelectedTable('')
    setSchema([])
    setRows(null)
    setOffset(0)
    setDBError('')
    setLoading(true)
    rpc.request<SQLiteTable[]>('sqlite/tables', {
      app_id: appID,
      database: databaseID,
    }).then((nextTables) => {
      setTables(nextTables ?? [])
    }).catch((err: unknown) => {
      setDBError(err instanceof Error ? err.message : String(err))
    }).finally(() => {
      setLoading(false)
    })
  }, [appID, databaseID, rpc])

  const hasNext = Boolean(rows && rows.rows.length === rows.limit)
  const hasPrevious = offset > 0

  return (
    <Card>
      <CardHeader>
        <CardTitle>{databaseLabel(database)}</CardTitle>
        <CardDescription>{database.path}</CardDescription>
        <CardAction>
          <Badge variant="outline">read-only</Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]">
        <div className="overflow-hidden border">
          <div className="border-b px-3 py-2 text-sm font-medium">Tables</div>
          <div className="max-h-[520px] overflow-auto">
            {tables.length === 0 ? (
              <div className="p-3 text-sm text-muted-foreground">{loading ? 'Loading...' : 'No tables'}</div>
            ) : (
              tables.map((table) => (
                <button
                  key={table.name}
                  type="button"
                  className={cn(
                    'flex w-full items-center justify-between gap-3 border-b px-3 py-2 text-left text-sm last:border-b-0 hover:bg-muted/50',
                    selectedTable === table.name && 'bg-muted/50'
                  )}
                  onClick={() => void openTable(table.name, 0)}
                >
                  <span className="truncate">{table.name}</span>
                  <Badge variant="secondary">{table.type}</Badge>
                </button>
              ))
            )}
          </div>
        </div>

        <div className="min-w-0 space-y-3">
          {dbError ? <div className="border border-destructive p-3 text-sm text-destructive">{dbError}</div> : null}
          {selectedTable ? (
            <>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{selectedTable}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {schema.map((column) => `${column.name} ${column.type}`.trim()).join(', ')}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!hasPrevious || loading}
                    onClick={() => void openTable(selectedTable, Math.max(0, offset - 50))}
                  >
                    <ChevronLeft data-icon="inline-start" />
                    Prev
                  </Button>
                  <Badge variant="outline">{offset + 1}-{offset + (rows?.rows.length ?? 0)}</Badge>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!hasNext || loading}
                    onClick={() => void openTable(selectedTable, offset + 50)}
                  >
                    Next
                    <ChevronRight data-icon="inline-end" />
                  </Button>
                </div>
              </div>
              <div className="max-h-[520px] overflow-auto border [&_[data-slot=table-container]]:overflow-visible">
                <Table className="min-w-max">
                  <TableHeader>
                    <TableRow>
                      {(rows?.columns ?? []).map((column) => (
                        <TableHead key={column}>{column}</TableHead>
                      ))}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(rows?.rows ?? []).map((row, rowIndex) => (
                      <TableRow key={`${selectedTable}-${offset}-${rowIndex}`}>
                        {row.map((value, columnIndex) => (
                          <TableCell key={columnIndex} className="max-w-80 truncate font-mono">
                            {renderCellValue(value)}
                          </TableCell>
                        ))}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          ) : (
            <div className="border p-4 text-sm text-muted-foreground">Select a table</div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function LogsPage({ connected, logs }: { connected: boolean; logs: DevLogEntry[] }) {
  const [serviceFilter, setServiceFilter] = useState('')
  const services = useMemo(() => {
    return [...new Set(logs.map(logService).filter(Boolean))].sort()
  }, [logs])
  const filteredLogs = serviceFilter ? logs.filter((entry) => logService(entry) === serviceFilter) : logs

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Terminal className="size-4" />
          Logs
        </CardTitle>
        <CardDescription>
          Live logs through Scenery's dashboard WebSocket
        </CardDescription>
        <CardAction>
          <Badge variant={connected ? 'default' : 'outline'}>{connected ? 'tailing' : 'offline'}</Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-center gap-2">
          <select
            aria-label="Filter logs by service"
            className="h-8 min-w-40 border bg-background px-2 text-sm"
            value={serviceFilter}
            onChange={(event) => setServiceFilter(event.target.value)}
          >
            <option value="">All services</option>
            {services.map((service) => (
              <option key={service} value={service}>{service}</option>
            ))}
          </select>
          <span className="text-xs text-muted-foreground">
            {filteredLogs.length} / {logs.length}
          </span>
        </div>
        <div className="h-[560px] overflow-auto border bg-muted/30 font-mono text-xs">
          {filteredLogs.length === 0 ? (
            <div className="p-4 text-muted-foreground">No log events loaded yet.</div>
          ) : (
            filteredLogs.map((entry) => (
              <div
                key={entry.id}
                className="grid grid-cols-[7rem_5rem_10rem_1fr] gap-3 border-b px-3 py-2 last:border-b-0"
              >
                <span className="text-muted-foreground">{formatTime(entry.time)}</span>
                <span className={entry.level === 'error' ? 'text-destructive' : 'text-muted-foreground'}>
                  {entry.level || 'info'}
                </span>
                <span className="truncate text-muted-foreground">{entry.source.id || entry.source.name || 'source'}</span>
                <span className="min-w-0 whitespace-pre-wrap break-words">{entry.raw || entry.message}</span>
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function logService(entry: DevLogEntry): string {
  const fieldService = recordValue(entry.fields, 'service')
  if (typeof fieldService === 'string' && fieldService.trim()) {
    return fieldService.trim()
  }
  const raw = entry.raw || entry.message || ''
  return raw.match(/\bservice=([^\s]+)/)?.[1] ?? raw.match(/\b(?:TRC|DBG|INF|WRN|ERR|EROR)\s+([a-z][\w-]*)[._]/i)?.[1] ?? ''
}

function eventKind(event: TraceEvent): string {
  if (event.span_start) {
    return 'span start'
  }
  if (event.span_end) {
    return 'span end'
  }
  return 'event'
}

function eventPayload(event: TraceEvent): unknown {
  return decodedTraceEvent(event) ?? event.span_start ?? event.span_end ?? event.span_event ?? event
}

function decodedTraceEvent(event: TraceEvent): Record<string, unknown> | null {
  const fields = (event.span_event as { victoria?: { fields?: Record<string, unknown> } } | undefined)?.victoria?.fields
  const raw = fields?.['scenery.event']
  if (typeof raw !== 'string') {
    return null
  }
  try {
    const decoded = JSON.parse(raw) as unknown
    return decoded && typeof decoded === 'object' ? decoded as Record<string, unknown> : null
  } catch {
    return null
  }
}

function payloadRecord(payload: unknown): Record<string, unknown> {
  return payload && typeof payload === 'object' ? payload as Record<string, unknown> : {}
}

function nanosValue(value: unknown): number {
  if (typeof value === 'number') {
    return value
  }
  if (typeof value === 'string') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
  return 0
}

interface TraceSpanRow {
  id: string
  label: string
  startMs: number
  durationMs: number
  events: TraceEvent[]
  error: boolean
}

function traceSpanRows(events: TraceEvent[], fallback?: TraceSummary): TraceSpanRow[] {
  const spans = new Map<string, TraceSpanRow>()
  const starts: number[] = []

  for (const event of events) {
    const spanID = event.span_id || 'trace'
    const eventTime = event.event_time ? new Date(event.event_time).getTime() : Number.NaN
    const directPayload = payloadRecord(event.span_start ? { span_start: event.span_start } : event.span_end ? { span_end: event.span_end } : {})
    const decodedPayload = decodedTraceEvent(event)
    const payloads = [directPayload, decodedPayload].filter(Boolean) as Record<string, unknown>[]

    let row = spans.get(spanID)
    if (!row) {
      row = {
        id: spanID,
        label: fallback?.service_name && fallback.endpoint_name ? `${fallback.service_name}.${fallback.endpoint_name}` : shortID(spanID),
        startMs: Number.isFinite(eventTime) ? eventTime : 0,
        durationMs: 0,
        events: [],
        error: false,
      }
      spans.set(spanID, row)
    }
    row.events.push(event)
    if (Number.isFinite(eventTime)) {
      row.startMs = row.startMs === 0 ? eventTime : Math.min(row.startMs, eventTime)
      starts.push(eventTime)
    }

    for (const payload of payloads) {
      const start = payloadRecord(payload.span_start)
      const end = payloadRecord(payload.span_end)
      const request = payloadRecord(start.request ?? end.request)
      const service = typeof request.service_name === 'string' ? request.service_name : ''
      const endpoint = typeof request.endpoint_name === 'string' ? request.endpoint_name : ''
      if (service || endpoint) {
        row.label = service && endpoint ? `${service}.${endpoint}` : service || endpoint
      }
      const durationNanos = nanosValue(end.duration_nanos)
      if (durationNanos > 0) {
        row.durationMs = Math.max(row.durationMs, durationNanos / 1_000_000)
      }
      row.error ||= Boolean(end.error)
    }
  }

  const traceStart = starts.length > 0 ? Math.min(...starts) : 0
  return [...spans.values()]
    .map((row) => ({
      ...row,
      startMs: traceStart ? Math.max(0, row.startMs - traceStart) : 0,
      durationMs: row.durationMs || fallback?.duration_nanos ? (row.durationMs || (fallback?.duration_nanos ?? 0) / 1_000_000) : 0.01,
    }))
    .sort((a, b) => a.startMs - b.startMs || b.durationMs - a.durationMs)
}

function TracesPage({
  activeApp,
  rpc,
  traces,
}: {
  activeApp: AppSummary | null
  rpc: SceneryRpcClient
  traces: TraceSummary[]
}) {
  const latest = traces.slice(0, 100)
  const [selectedTraceID, setSelectedTraceID] = useState('')
  const [events, setEvents] = useState<TraceEvent[]>([])
  const [traceError, setTraceError] = useState('')

  const selectedTrace = latest.find((trace) => trace.trace_id === selectedTraceID)
  const spanRows = useMemo(() => traceSpanRows(events, selectedTrace), [events, selectedTrace])
  const traceDurationMs = Math.max(1, ...spanRows.map((span) => span.startMs + span.durationMs))

  const openTrace = useCallback(async (trace: TraceSummary) => {
    if (!activeApp) {
      return
    }
    setTraceError('')
    setSelectedTraceID(trace.trace_id)
    setEvents([])
    try {
      const nextEvents = await rpc.request<TraceEvent[]>('traces/get', {
        app_id: activeApp.id,
        trace_id: trace.trace_id,
      })
      setEvents(nextEvents ?? [])
    } catch (err) {
      setTraceError(err instanceof Error ? err.message : String(err))
    }
  }, [activeApp, rpc])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Traces</CardTitle>
        <CardDescription>Latest trace spans</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_24rem]">
        <div className="overflow-hidden border">
          {latest.length === 0 ? (
            <div className="p-4 text-sm text-muted-foreground">No trace spans loaded yet.</div>
          ) : (
            <div className="divide-y">
              {latest.map((trace) => (
                <button
                  key={`${trace.trace_id}-${trace.span_id}`}
                  type="button"
                  className="grid w-full gap-3 p-3 text-left text-sm hover:bg-muted/50 md:grid-cols-[8rem_7rem_1fr_8rem_9rem]"
                  onClick={() => void openTrace(trace)}
                >
                  <div className="font-mono text-xs text-muted-foreground">{formatTime(trace.started_at)}</div>
                  <Badge variant={trace.is_error ? 'destructive' : 'secondary'}>
                    {trace.is_error ? 'error' : trace.type || 'trace'}
                  </Badge>
                  <div className="min-w-0">
                    <div className="truncate font-medium">
                      {trace.service_name || 'service'}
                      {trace.endpoint_name ? `.${trace.endpoint_name}` : ''}
                    </div>
                    <div className="truncate font-mono text-xs text-muted-foreground">
                      {shortID(trace.trace_id)}
                    </div>
                  </div>
                  <div className="text-muted-foreground">{formatDuration(trace.duration_nanos)}</div>
                  <div className="truncate font-mono text-xs text-muted-foreground">{shortID(trace.span_id)}</div>
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="min-h-80 border bg-muted/20">
          {!selectedTrace ? (
            <div className="p-4 text-sm text-muted-foreground">Select a trace to inspect spans and events.</div>
          ) : (
            <div className="flex max-h-[680px] flex-col">
              <div className="border-b p-4">
                <div className="text-sm font-semibold">
                  {selectedTrace.service_name || 'service'}
                  {selectedTrace.endpoint_name ? `.${selectedTrace.endpoint_name}` : ''}
                </div>
                <div className="mt-1 font-mono text-xs text-muted-foreground">{selectedTrace.trace_id}</div>
                {traceError ? <div className="mt-2 text-xs text-destructive">{traceError}</div> : null}
              </div>
              <div className="overflow-auto p-3">
                {events.length === 0 && !traceError ? (
                  <div className="text-sm text-muted-foreground">Loading trace events...</div>
                ) : (
                  <>
                    <div className="mb-4 border bg-background p-3">
                      <div className="mb-3 flex items-center justify-between text-xs text-muted-foreground">
                        <span>0 ms</span>
                        <span>{traceDurationMs.toFixed(2)} ms</span>
                      </div>
                      <div className="space-y-3">
                        {spanRows.map((span) => (
                          <div key={span.id} className="grid grid-cols-[9rem_1fr] items-center gap-3 text-xs">
                            <div className="min-w-0">
                              <div className="truncate font-medium">{span.label}</div>
                              <div className="truncate font-mono text-muted-foreground">{shortID(span.id)}</div>
                            </div>
                            <div className="relative h-7 border bg-muted/40">
                              <div
                                className={span.error ? 'absolute top-1 h-5 bg-destructive' : 'absolute top-1 h-5 bg-primary'}
                                style={{
                                  left: `${(span.startMs / traceDurationMs) * 100}%`,
                                  width: `${Math.max(1, (span.durationMs / traceDurationMs) * 100)}%`,
                                }}
                                title={`${span.label} ${span.durationMs.toFixed(2)} ms`}
                              />
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                    {events.map((event, index) => (
                      <div key={`${event.event_id ?? index}-${event.event_time ?? index}`} className="mb-3 border bg-background p-3">
                        <div className="mb-2 flex items-center gap-2">
                          <Badge variant="secondary">{eventKind(event)}</Badge>
                          <span className="font-mono text-xs text-muted-foreground">{formatTime(event.event_time)}</span>
                        </div>
                        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-xs">
                          {JSON.stringify(eventPayload(event), null, 2)}
                        </pre>
                      </div>
                    ))}
                  </>
                )}
              </div>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

export default App
