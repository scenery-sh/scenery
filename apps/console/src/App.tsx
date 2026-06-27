import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity,
  Bell,
  CircleCheck,
  ExternalLink,
  GitBranch,
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
import { Separator } from '@/components/ui/separator'
import { SceneryRpcClient, sceneryWebSocketURL } from '@/lib/scenery-rpc'
import type { AppStatus, AppSummary, DashboardNotification, DevLogEntry, TraceSummary } from '@/lib/scenery-types'

type Page = 'Overview' | 'Services' | 'Logs' | 'Traces' | 'Deployments'

const navItems: Page[] = ['Overview', 'Services', 'Logs', 'Traces', 'Deployments']

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

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 12)}...` : value
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

  const activeApp = useMemo(() => apps.find((app) => !app.offline) ?? apps[0] ?? null, [apps])

  const refresh = useCallback(async () => {
    setError('')
    const nextApps = await rpc.request<AppSummary[]>('list-apps')
    setApps(nextApps)
    const nextApp = nextApps.find((app) => !app.offline) ?? nextApps[0]
    if (!nextApp) {
      setStatus(null)
      setTraces([])
      setLogs([])
      return
    }
    const [nextStatus, nextTraces, nextOutputs] = await Promise.all([
      rpc.request<AppStatus>('status', { app_id: nextApp.id }),
      rpc.request<TraceSummary[]>('traces/list', { app_id: nextApp.id }),
      rpc.request<DevLogEntry[]>('logs/list', { app_id: nextApp.id, limit: 100 }),
    ])
    setStatus(nextStatus)
    setTraces(nextTraces ?? [])
    setLogs(nextOutputs ?? [])
  }, [rpc])

  const tailLogs = useCallback(async () => {
    if (!activeApp) {
      return
    }
    const afterID = logs.at(-1)?.id ?? 0
    const next = await rpc.request<DevLogEntry[]>('logs/list', {
      app_id: activeApp.id,
      after_id: afterID,
      limit: 100,
    })
    if (next.length > 0) {
      setLogs((current) => [...current, ...next].slice(-500))
    }
  }, [activeApp, logs, rpc])

  useEffect(() => {
    rpc.connect()
    const unsubscribeConnection = rpc.subscribeConnection(setConnected)
    const unsubscribeNotifications = rpc.subscribe((notification) => {
      setNotifications((current) => [notification, ...current].slice(0, 5))
    })
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
    const appServices = (status?.meta?.svcs ?? []).map((service) => ({
      key: `service:${service.name}`,
      name: service.name,
      kind: 'service',
      href: status?.routes?.api ?? '',
      detail: `${service.rpcs?.length ?? 0} endpoint${service.rpcs?.length === 1 ? '' : 's'}`,
    }))
    return [...routes, ...aliases, ...appServices]
  }, [status])

  const metricsStatus = status?.grafana?.datasource_status?.['scenery-victoriametrics']
  const metricsAvailable = metricsStatus === 'ready' || metricsStatus === 'external' || metricsStatus === 'ok'
  const metricsURL = status?.grafana?.overview_url || status?.grafana?.url || ''

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
          <div className="flex items-center gap-2 font-heading text-sm font-semibold">
            <Activity className="size-4 text-primary" />
            Scenery Console
          </div>
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
            <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
              {error || `WebSocket JSON-RPC endpoint: ${sceneryWebSocketURL()}`}
            </p>
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
          <TracesPage grafanaURL={status?.grafana?.overview_url || status?.grafana?.url || ''} traces={traces} />
        ) : (
          <>
            <Card>
              <CardHeader>
                <CardTitle>VictoriaMetrics</CardTitle>
                <CardDescription>
                  {metricsAvailable ? 'available' : metricsStatus || status?.grafana?.status || 'unknown'}
                </CardDescription>
                <CardAction>
                  <Badge variant={metricsAvailable ? 'default' : 'outline'}>
                    {metricsAvailable ? 'available' : 'not ready'}
                  </Badge>
                </CardAction>
              </CardHeader>
              {metricsURL ? (
                <CardContent>
                  <Button variant="outline" size="sm" asChild>
                    <a href={metricsURL} target="_blank" rel="noreferrer">
                      <ExternalLink data-icon="inline-start" />
                      Open metrics
                    </a>
                  </Button>
                </CardContent>
              ) : null}
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

function LogsPage({ connected, logs }: { connected: boolean; logs: DevLogEntry[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Terminal className="size-4" />
          Logs
        </CardTitle>
        <CardDescription>
          Live tail from VictoriaLogs through Scenery's dashboard WebSocket
        </CardDescription>
        <CardAction>
          <Badge variant={connected ? 'default' : 'outline'}>{connected ? 'tailing' : 'offline'}</Badge>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div className="h-[560px] overflow-auto border bg-muted/30 font-mono text-xs">
          {logs.length === 0 ? (
            <div className="p-4 text-muted-foreground">No VictoriaLogs events loaded yet.</div>
          ) : (
            logs.map((entry) => (
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

function TracesPage({ grafanaURL, traces }: { grafanaURL: string; traces: TraceSummary[] }) {
  const latest = traces.slice(0, 100)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Traces</CardTitle>
        <CardDescription>Latest traces from VictoriaTraces</CardDescription>
        <CardAction>
          {grafanaURL ? (
            <Button variant="outline" size="sm" asChild>
              <a href={grafanaURL} target="_blank" rel="noreferrer">
                <ExternalLink data-icon="inline-start" />
                Open Grafana
              </a>
            </Button>
          ) : (
            <Badge variant="outline">Grafana unavailable</Badge>
          )}
        </CardAction>
      </CardHeader>
      <CardContent>
        <div className="overflow-hidden border">
          {latest.length === 0 ? (
            <div className="p-4 text-sm text-muted-foreground">No VictoriaTraces spans loaded yet.</div>
          ) : (
            <div className="divide-y">
              {latest.map((trace) => (
                <div
                  key={`${trace.trace_id}-${trace.span_id}`}
                  className="grid gap-3 p-3 text-sm md:grid-cols-[8rem_7rem_1fr_8rem_9rem]"
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
                </div>
              ))}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

export default App
