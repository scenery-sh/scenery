import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as stylex from '@stylexjs/stylex'
import { Badge } from '@astryxdesign/core/Badge'
import { Button } from '@astryxdesign/core/Button'
import { HStack } from '@astryxdesign/core/HStack'
import { Heading } from '@astryxdesign/core/Heading'
import { Selector } from '@astryxdesign/core/Selector'
import { StatusDot } from '@astryxdesign/core/StatusDot'
import { Tab, TabList } from '@astryxdesign/core/TabList'
import { Text } from '@astryxdesign/core/Text'
import { VStack } from '@astryxdesign/core/VStack'
import { Theme } from '@astryxdesign/core'
import { neutralTheme } from '@astryxdesign/theme-neutral/built'
import {
  DashboardRPC,
  type AppStatus,
  type AppSummary,
  type DashboardEvent,
  type DevLogEntry,
  type ProcessOutput,
  type PostgresColumn,
  type PostgresRows,
  type PostgresTable,
  type SQLDatabase,
  type TraceSummary,
} from './scenery'
import { LogsPage, OverviewPage } from './dashboard-ui'
import {
  appOptions,
  chooseAppID,
  dashboardStatusLabel,
  pages,
  routeLinks,
  shortID,
  type Page,
} from './dashboard-model'
import { upsertTrace } from './dashboard-utils'
import { SymphonyPage } from './symphony-page'
import {
  ApiExplorerPage,
  CronPage,
  DatabasesWorkbenchPage,
  LogsAndOutputPage,
  ServiceCatalogPage,
  TracesWorkbenchPage,
} from './workbench-pages'

const styles = stylex.create({
  shell: {
    minHeight: '100vh',
    colorScheme: 'dark',
    backgroundColor: 'var(--color-background-surface)',
    color: 'var(--color-text-primary)',
  },
  header: {
    position: 'sticky',
    top: 0,
    zIndex: 1,
    borderBottomWidth: 'var(--border-width)',
    borderBottomStyle: 'solid',
    borderBottomColor: 'var(--color-border)',
    backgroundColor: 'var(--color-background-surface)',
  },
  headerInner: {
    minHeight: 'var(--spacing-12)',
    maxWidth: '72rem',
    marginInline: 'auto',
    paddingInline: 'var(--spacing-4)',
    display: 'flex',
    alignItems: 'center',
    gap: 'var(--spacing-4)',
  },
  appPicker: {
    width: {
      default: '16rem',
      '@media (max-width: 760px)': '12rem',
    },
    flexShrink: 0,
  },
  nav: {
    flex: 1,
    minWidth: 0,
    overflowX: 'auto',
    overflowY: 'hidden',
    scrollbarWidth: 'none',
  },
  navContent: {
    display: 'inline-block',
    width: 'max-content',
    paddingInlineEnd: 'var(--spacing-2)',
  },
  page: {
    maxWidth: '72rem',
    marginInline: 'auto',
    padding: 'var(--spacing-6) var(--spacing-4)',
    display: 'grid',
    gap: 'var(--spacing-4)',
  },
  pageHeading: {
    display: 'flex',
    alignItems: 'flex-end',
    justifyContent: 'space-between',
    gap: 'var(--spacing-4)',
    flexWrap: 'wrap',
  },
  errorText: {
    color: 'var(--color-error)',
  },
})

function App() {
  const [rpc] = useState(() => new DashboardRPC())
  const [connected, setConnected] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState<Page>(() => initialPage())
  const [apps, setApps] = useState<AppSummary[]>([])
  const [selectedAppID, setSelectedAppID] = useState(() => initialAppID())
  const [status, setStatus] = useState<AppStatus | null>(null)
  const [logs, setLogs] = useState<DevLogEntry[]>([])
  const [traces, setTraces] = useState<TraceSummary[]>([])
  const [outputs, setOutputs] = useState<ProcessOutput[]>([])
  const [events, setEvents] = useState<DashboardEvent[]>([])
  const [selectedDatabase, setSelectedDatabase] = useState('')
  const [selectedTable, setSelectedTable] = useState('')
  const [postgresTables, setPostgresTables] = useState<PostgresTable[]>([])
  const [postgresSchema, setPostgresSchema] = useState<PostgresColumn[]>([])
  const [postgresRows, setPostgresRows] = useState<PostgresRows | null>(null)
  const [databaseError, setDatabaseError] = useState('')
  const navRef = useRef<HTMLElement | null>(null)

  const databases = useMemo(() => status?.meta?.sql_databases ?? [], [status])
  const serviceLinks = useMemo(() => routeLinks(status), [status])
  const selectedApp = useMemo(
    () => apps.find((app) => app.id === selectedAppID) ?? null,
    [apps, selectedAppID],
  )
  const selectedAppIDRef = useRef(selectedAppID)

  useEffect(() => {
    selectedAppIDRef.current = selectedAppID
  }, [selectedAppID])

  useEffect(() => {
    let frame = 0
    const alignActiveTab = () => {
      window.cancelAnimationFrame(frame)
      frame = window.requestAnimationFrame(() => {
        const nav = navRef.current
        const activeTab = Array.from(nav?.querySelectorAll<HTMLElement>('[data-scenery-ui]') ?? [])
          .find((element) => element.dataset.sceneryUi === `ConsoleTab:${page}`)
        if (!nav || !activeTab) {
          return
        }
        const gutter = 8
        const navRect = nav.getBoundingClientRect()
        const tabRect = activeTab.getBoundingClientRect()
        if (tabRect.right > navRect.right - gutter) {
          nav.scrollLeft += tabRect.right - navRect.right + gutter
        } else if (tabRect.left < navRect.left + gutter) {
          nav.scrollLeft -= navRect.left - tabRect.left + gutter
        }
      })
    }

    alignActiveTab()
    window.addEventListener('resize', alignActiveTab)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('resize', alignActiveTab)
    }
  }, [page])

  const refreshDashboard = useCallback(
    async (requestedAppID?: string, showLoading = false) => {
      if (showLoading) {
        setLoading(true)
      }
      setError('')
      try {
        const nextApps = await rpc.call<AppSummary[]>('list-apps')
        const appList = Array.isArray(nextApps) ? nextApps : []
        setApps(appList)
        const appID = chooseAppID(appList, requestedAppID ?? selectedAppID)
        setSelectedAppID(appID)
        if (appID === '') {
          setStatus(null)
          setLogs([])
          setTraces([])
          setOutputs([])
          return
        }

        const [statusResult, logResult, traceResult, outputResult] = await Promise.allSettled([
          rpc.call<AppStatus>('status', { app_id: appID }),
          rpc.call<DevLogEntry[]>('logs/list', { app_id: appID, limit: 100 }),
          rpc.call<TraceSummary[]>('traces/list', { app_id: appID }),
          rpc.call<ProcessOutput[]>('process/output/list', { app_id: appID, limit: 300 }),
        ])

        if (statusResult.status === 'fulfilled') {
          setStatus(statusResult.value)
        } else {
          setStatus(null)
          setError(statusResult.reason instanceof Error ? statusResult.reason.message : 'status failed')
        }
        if (logResult.status === 'fulfilled') {
          setLogs(Array.isArray(logResult.value) ? logResult.value : [])
        }
        if (traceResult.status === 'fulfilled') {
          setTraces(Array.isArray(traceResult.value) ? traceResult.value : [])
        }
        if (outputResult.status === 'fulfilled') {
          setOutputs(Array.isArray(outputResult.value) ? outputResult.value : [])
        }
      } catch (nextError) {
        setError(nextError instanceof Error ? nextError.message : 'dashboard refresh failed')
      } finally {
        if (showLoading) {
          setLoading(false)
        }
      }
    },
    [rpc, selectedAppID],
  )

  const clearTraces = useCallback(async () => {
    if (selectedAppID === '') {
      return
    }
    setLoading(true)
    setError('')
    try {
      await rpc.call('traces/clear', { app_id: selectedAppID })
      setTraces([])
      await refreshDashboard(selectedAppID)
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not clear traces')
    } finally {
      setLoading(false)
    }
  }, [refreshDashboard, rpc, selectedAppID])

  useEffect(() => {
    const stopConnection = rpc.onConnection(setConnected)
    const stopEvents = rpc.onEvent((event) => {
      setEvents((current) => [event, ...current].slice(0, 16))
      switch (event.method) {
        case 'process/start':
        case 'process/reload':
        case 'process/compile-start':
        case 'process/compile-error':
        case 'process/stop':
          setStatus(event.params as AppStatus)
          void rpc.call<AppSummary[]>('list-apps').then(setApps).catch(() => undefined)
          break
        case 'process/output': {
          const output = event.params as ProcessOutput
          if (output.appID === selectedAppIDRef.current) {
            setOutputs((current) => [...current.slice(-299), output])
          }
          break
        }
        case 'trace/new': {
          const params = event.params as { app_id?: string; span?: TraceSummary }
          if (params.app_id && params.app_id !== selectedAppIDRef.current) {
            break
          }
          const span = params.span
          if (span) {
            setTraces((current) => upsertTrace(current, span))
          }
          break
        }
        default:
          break
      }
    })
    rpc.connect()
    return () => {
      stopEvents()
      stopConnection()
      rpc.dispose()
    }
  }, [rpc])

  useEffect(() => {
    void refreshDashboard()
    const timer = window.setInterval(() => {
      void refreshDashboard()
    }, 5000)
    return () => window.clearInterval(timer)
  }, [refreshDashboard])

  useEffect(() => {
    const fallback = databases[0]?.name ?? ''
    if (selectedDatabase === fallback) {
      return
    }
    if (selectedDatabase === '' || !databases.some((database) => database.name === selectedDatabase)) {
      setSelectedDatabase(fallback)
      setSelectedTable('')
      setPostgresRows(null)
      setPostgresSchema([])
    }
  }, [databases, selectedDatabase])

  useEffect(() => {
    let active = true
    setPostgresTables([])
    setSelectedTable('')
    setPostgresRows(null)
    setPostgresSchema([])
    setDatabaseError('')
    if (selectedAppID === '' || selectedDatabase === '') {
      return () => {
        active = false
      }
    }
    rpc
      .call<PostgresTable[]>('postgres/tables', {
        app_id: selectedAppID,
        database: selectedDatabase,
      })
      .then((tables) => {
        if (!active) {
          return
        }
        setPostgresTables(tables)
        setSelectedTable(tables[0]?.name ?? '')
      })
      .catch((nextError) => {
        if (active) {
          setDatabaseError(nextError instanceof Error ? nextError.message : 'could not load tables')
        }
      })
    return () => {
      active = false
    }
  }, [rpc, selectedAppID, selectedDatabase])

  useEffect(() => {
    let active = true
    setPostgresRows(null)
    setPostgresSchema([])
    setDatabaseError('')
    if (selectedAppID === '' || selectedDatabase === '' || selectedTable === '') {
      return () => {
        active = false
      }
    }
    Promise.all([
      rpc.call<PostgresColumn[]>('postgres/schema', {
        app_id: selectedAppID,
        database: selectedDatabase,
        table: selectedTable,
      }),
      rpc.call<PostgresRows>('postgres/rows', {
        app_id: selectedAppID,
        database: selectedDatabase,
        table: selectedTable,
        limit: 100,
        offset: 0,
      }),
    ])
      .then(([schema, rows]) => {
        if (!active) {
          return
        }
        setPostgresSchema(schema)
        setPostgresRows(rows)
      })
      .catch((nextError) => {
        if (active) {
          setDatabaseError(nextError instanceof Error ? nextError.message : 'could not load rows')
        }
      })
    return () => {
      active = false
    }
  }, [rpc, selectedAppID, selectedDatabase, selectedTable])

  const statusLabel = dashboardStatusLabel(status, selectedApp, connected)
  const appName = status?.appID ?? selectedApp?.name ?? 'Development session'
  const subtitle = status?.appRoot ?? selectedApp?.app_root ?? 'No app selected'
  const compileError = status?.compileError ?? selectedApp?.compileError ?? ''

  return (
    <Theme theme={neutralTheme}>
      <main {...stylex.props(styles.shell)}>
        <header {...stylex.props(styles.header)}>
          <section {...stylex.props(styles.headerInner)} aria-label="Console navigation">
            <HStack gap={2} vAlign="center" xstyle={styles.appPicker}>
              <StatusDot
                variant={connected ? 'success' : 'neutral'}
                label={connected ? 'dashboard connected' : 'dashboard disconnected'}
              />
              <Selector
                label="App"
                isLabelHidden
                value={selectedAppID}
                options={appOptions(apps)}
                placeholder="No app"
                isDisabled={apps.length === 0}
                hasSearch={apps.length > 8}
                onChange={(value) => {
                  setSelectedAppID(value)
                  writeLocationState(value, page)
                  setSelectedDatabase('')
                  setSelectedTable('')
                  void refreshDashboard(value, true)
                }}
              />
            </HStack>

            <nav ref={navRef} {...stylex.props(styles.nav)} aria-label="Console sections" data-scenery-ui="ConsoleHeaderNav">
              <section {...stylex.props(styles.navContent)}>
                <TabList
                  value={page}
                  onChange={(value) => {
                    const nextPage = parsePage(value)
                    setPage(nextPage)
                    writeLocationState(selectedAppID, nextPage)
                  }}
                  size="sm"
                >
                  {pages.map((item) => (
                    <Tab key={item} value={item} label={item} endContent={tabCount(item, logs, traces, databases, outputs)} data-scenery-ui={`ConsoleTab:${item}`} />
                  ))}
                </TabList>
              </section>
            </nav>

            <Button label="Refresh" size="sm" variant="secondary" isLoading={loading} onClick={() => void refreshDashboard(undefined, true)} />
          </section>
        </header>

        <section {...stylex.props(styles.page)}>
          <section {...stylex.props(styles.pageHeading)}>
            <VStack gap={2} hAlign="start" as="section">
              <HStack gap={2} vAlign="center">
                <Badge label={statusLabel.label} variant={statusLabel.variant} />
                {status?.sessionID ? <Text type="supporting">session {shortID(status.sessionID)}</Text> : null}
              </HStack>
              <Heading level={1}>{appName}</Heading>
              <Text type="body" color="secondary">
                {subtitle}
              </Text>
              {compileError !== '' ? (
                <Text type="body" xstyle={styles.errorText}>
                  {compileError}
                </Text>
              ) : null}
              {error !== '' ? (
                <Text type="body" xstyle={styles.errorText}>
                  {error}
                </Text>
              ) : null}
            </VStack>
            <VStack gap={1} hAlign="end" as="section">
              <Text type="supporting" color="secondary">
                {status?.pid ? `pid ${status.pid}` : connected ? 'live dashboard rpc' : 'waiting for rpc'}
              </Text>
              <Text type="supporting" color="secondary">
                {status?.sessionStatusReason ?? status?.sessionStatus ?? ''}
              </Text>
            </VStack>
          </section>

          {page === 'Overview' ? (
            <OverviewPage
              connected={connected}
              status={status}
              logs={logs}
              traces={traces}
              databases={databases}
              serviceLinks={serviceLinks}
              events={events}
            />
          ) : null}
          {page === 'API' ? (
            <ApiExplorerPage appID={selectedAppID} rpc={rpc} status={status} traces={traces} outputs={outputs} />
          ) : null}
          {page === 'Catalog' ? <ServiceCatalogPage status={status} /> : null}
          {page === 'Logs' ? <LogsPage logs={logs} /> : null}
          {page === 'Output' ? <LogsAndOutputPage outputs={outputs} /> : null}
          {page === 'Traces' ? <TracesWorkbenchPage traces={traces} onClear={clearTraces} loading={loading} /> : null}
          {page === 'Databases' ? (
            <DatabasesWorkbenchPage
              appID={selectedAppID}
              rpc={rpc}
              databases={databases}
              selectedDatabase={selectedDatabase}
              onDatabaseChange={(value) => {
                setSelectedDatabase(value)
                setSelectedTable('')
              }}
              tables={postgresTables}
              selectedTable={selectedTable}
              onTableChange={setSelectedTable}
              schema={postgresSchema}
              rows={postgresRows}
              error={databaseError}
            />
          ) : null}
          {page === 'Cron' ? <CronPage status={status} traces={traces} /> : null}
          {page === 'Symphony' ? <SymphonyPage appID={selectedAppID} rpc={rpc} /> : null}
        </section>
      </main>
    </Theme>
  )
}

function initialAppID(): string {
  return new URLSearchParams(window.location.search).get('app') ?? ''
}

function initialPage(): Page {
  return parsePage(new URLSearchParams(window.location.search).get('page'))
}

function parsePage(value: string | null): Page {
  return pages.includes(value as Page) ? (value as Page) : 'Overview'
}

function writeLocationState(appID: string, page: Page) {
  const url = new URL(window.location.href)
  if (appID === '') {
    url.searchParams.delete('app')
  } else {
    url.searchParams.set('app', appID)
  }
  if (page === 'Overview') {
    url.searchParams.delete('page')
  } else {
    url.searchParams.set('page', page)
  }
  window.history.replaceState(null, '', `${url.pathname}${url.search}${url.hash}`)
}

function tabCount(
  page: Page,
  logs: DevLogEntry[],
  traces: TraceSummary[],
  databases: SQLDatabase[],
  outputs: ProcessOutput[],
) {
  if (page === 'Logs' && logs.length > 0) {
    return <Badge label={logs.length} variant="neutral" />
  }
  if (page === 'Output' && outputs.length > 0) {
    return <Badge label={outputs.length} variant="neutral" />
  }
  if (page === 'Traces' && traces.length > 0) {
    return <Badge label={traces.length} variant={traces.some((trace) => trace.is_error) ? 'error' : 'neutral'} />
  }
  if (page === 'Databases' && databases.length > 0) {
    return <Badge label={databases.length} variant="neutral" />
  }
  return null
}

export default App
