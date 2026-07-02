import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as stylex from '@stylexjs/stylex'
import { Badge } from '@astryxdesign/core/Badge'
import { Button } from '@astryxdesign/core/Button'
import { Card } from '@astryxdesign/core/Card'
import { CheckboxInput } from '@astryxdesign/core/CheckboxInput'
import { CodeBlock } from '@astryxdesign/core/CodeBlock'
import { Grid } from '@astryxdesign/core/Grid'
import { HStack } from '@astryxdesign/core/HStack'
import { Heading } from '@astryxdesign/core/Heading'
import { Section } from '@astryxdesign/core/Section'
import { Selector } from '@astryxdesign/core/Selector'
import { StatusDot } from '@astryxdesign/core/StatusDot'
import { Table, pixel, proportional } from '@astryxdesign/core/Table'
import type { TableColumn } from '@astryxdesign/core/Table'
import { Text } from '@astryxdesign/core/Text'
import { TextArea } from '@astryxdesign/core/TextArea'
import { TextInput } from '@astryxdesign/core/TextInput'
import { VStack } from '@astryxdesign/core/VStack'
import {
  DashboardRPC,
  type ApiCallResponse,
  type AppStatus,
  type ProcessOutput,
  type SQLDatabase,
  type SQLiteColumn,
  type SQLiteRows,
  type SQLiteTable,
  type StoredRequest,
  type StoredRequestInput,
  type TraceSummary,
} from './scenery'
import {
  apiResponseBody,
  endpointOptions,
  filterServices,
  formatDuration,
  formatJSON,
  formatTime,
  formatTimestamp,
  inferColumns,
  materializePath,
  middlewareForService,
  parseJSONInput,
  processOutputText,
  tryParseJSON,
  type EndpointOption,
} from './dashboard-utils'

interface TraceRow extends Record<string, unknown> {
  id: string
  started: string
  name: string
  duration: string
  status: string
}

interface OutputRow extends Record<string, unknown> {
  id: string
  time: string
  stream: string
  pid: string
  output: string
}

interface DBRow extends Record<string, unknown> {
  __id: string
}

const styles = stylex.create({
  actionBar: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 'var(--spacing-3)',
    flexWrap: 'wrap',
  },
  twoColumn: {
    display: 'grid',
    gridTemplateColumns: {
      default: '1fr',
      '@media (min-width: 980px)': '18rem minmax(0, 1fr)',
    },
    gap: 'var(--spacing-4)',
    alignItems: 'start',
  },
  formGrid: {
    display: 'grid',
    gridTemplateColumns: {
      default: '1fr',
      '@media (min-width: 760px)': 'repeat(2, minmax(0, 1fr))',
    },
    gap: 'var(--spacing-3)',
  },
  list: {
    display: 'grid',
    gap: 'var(--spacing-2)',
  },
  rowButton: {
    width: '100%',
    borderWidth: 'var(--border-width)',
    borderStyle: 'solid',
    borderColor: 'var(--color-border)',
    borderRadius: 'var(--radius-2)',
    backgroundColor: 'var(--color-background-surface)',
    color: 'inherit',
    textAlign: 'left',
    padding: 'var(--spacing-3)',
    cursor: 'pointer',
  },
  rowButtonActive: {
    borderColor: 'var(--color-border-strong)',
    backgroundColor: 'var(--color-background-muted)',
  },
  tableFrame: {
    minWidth: 0,
    overflowX: 'auto',
  },
  errorText: {
    color: 'var(--color-error)',
  },
  codeText: {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  },
  compactList: {
    display: 'grid',
    gap: 'var(--spacing-1)',
  },
})

const traceColumns: TableColumn<TraceRow>[] = [
  { key: 'started', header: 'Started', width: pixel(152) },
  { key: 'name', header: 'Name', width: proportional(2) },
  { key: 'duration', header: 'Duration', width: pixel(112) },
  { key: 'status', header: 'Status', width: pixel(96) },
]

const outputColumns: TableColumn<OutputRow>[] = [
  { key: 'time', header: 'Time', width: pixel(150) },
  { key: 'stream', header: 'Stream', width: pixel(96) },
  { key: 'pid', header: 'PID', width: pixel(96) },
  { key: 'output', header: 'Output', width: proportional(3) },
]

export function ApiExplorerPage({
  appID,
  rpc,
  status,
  traces,
  outputs,
}: {
  appID: string
  rpc: DashboardRPC
  status: AppStatus | null
  traces: TraceSummary[]
  outputs: ProcessOutput[]
}) {
  const options = useMemo(() => endpointOptions(status), [status])
  const [selectedKey, setSelectedKey] = useState('')
  const selected = useMemo(() => options.find((item) => item.key === selectedKey) ?? options[0] ?? null, [options, selectedKey])
  const [method, setMethod] = useState('GET')
  const [path, setPath] = useState('/')
  const [payloadText, setPayloadText] = useState('{}')
  const [authToken, setAuthToken] = useState('')
  const [title, setTitle] = useState('')
  const [shared, setShared] = useState(false)
  const [storedID, setStoredID] = useState('')
  const [storedRequests, setStoredRequests] = useState<StoredRequest[]>([])
  const [response, setResponse] = useState<ApiCallResponse | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [requestSeq, setRequestSeq] = useState(1)
  const [correlationID, setCorrelationID] = useState('')
  const initializedEndpointKeyRef = useRef('')
  const selectedInitializationKey = selected ? `${appID}:${selected.key}` : ''

  const refreshStoredRequests = useCallback(async () => {
    if (appID === '') {
      return
    }
    try {
      const requests = await rpc.listStoredRequests(appID)
      setStoredRequests(Array.isArray(requests) ? requests : [])
      setError('')
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not load stored requests')
    }
  }, [appID, rpc])

  useEffect(() => {
    if (selected && selected.key !== selectedKey) {
      setSelectedKey(selected.key)
    }
  }, [selected, selectedKey])

  useEffect(() => {
    if (!selected || initializedEndpointKeyRef.current === selectedInitializationKey) {
      return
    }
    initializedEndpointKeyRef.current = selectedInitializationKey
    setMethod(selected.method)
    setPath(selected.path)
    setPayloadText(formatJSON(schemaExampleValue(selected.rpc?.request_schema)))
    setTitle(selected.key)
    setStoredID('')
    setResponse(null)
    setError('')
  }, [selected, selectedInitializationKey])

  useEffect(() => {
    if (appID === '') {
      setStoredRequests([])
      return
    }
    void refreshStoredRequests()
  }, [appID, refreshStoredRequests])

  const recentOutput = useMemo(() => {
    if (correlationID === '') {
      return []
    }
    return outputs
      .flatMap((item) =>
        processOutputText(item)
          .split('\n')
          .filter((line) => line.includes(correlationID))
          .map((line) => `${formatTime(item.created_at)} ${item.stream} ${line}`),
      )
      .slice(-12)
  }, [correlationID, outputs])

  const responseTrace = useMemo(
    () => traces.find((trace) => trace.trace_id === response?.trace_id) ?? null,
    [response?.trace_id, traces],
  )

  if (!selected) {
    return (
      <VStack gap={4} as="section" data-scenery-ui="ConsoleNextAPIExplorer">
        <EmptyPanel title="API Explorer" message="No callable endpoints were discovered for this app." />
      </VStack>
    )
  }

  return (
    <section {...stylex.props(styles.twoColumn)} data-scenery-ui="ConsoleNextAPIExplorer">
      <Section padding={4}>
        <VStack gap={4} as="section">
          <SectionHeading title="Stored Requests" description={`${storedRequests.length} saved locally`} />
          <Button label="Refresh" size="sm" variant="secondary" clickAction={refreshStoredRequests} />
          <section {...stylex.props(styles.list)}>
            {storedRequests.map((item) => (
              <button key={item.id} type="button" {...stylex.props(styles.rowButton, item.id === storedID && styles.rowButtonActive)} onClick={() => openStoredRequest(item)}>
                <VStack gap={1} as="section">
                  <Text type="label" weight="semibold">{item.title}</Text>
                  <Text type="supporting" color="secondary">{item.svcName}.{item.rpcName}</Text>
                </VStack>
              </button>
            ))}
            {storedRequests.length === 0 ? <Text type="body" color="secondary">No stored requests yet.</Text> : null}
          </section>
        </VStack>
      </Section>

      <VStack gap={4} as="section">
        <Section padding={4}>
          <VStack gap={4} as="section">
            <section {...stylex.props(styles.actionBar)}>
              <SectionHeading title="Call Endpoint" description={selected.key} />
              <HStack gap={2} vAlign="center">
                <Button label="Send" variant="primary" isLoading={busy} clickAction={callEndpoint} />
                <Button label={storedID ? 'Update' : 'Save'} variant="secondary" isLoading={busy} clickAction={saveRequest} />
                {storedID ? <Button label="Delete" variant="destructive" isLoading={busy} clickAction={deleteSelectedRequest} /> : null}
              </HStack>
            </section>
            <Selector
              label="Endpoint"
              value={selected.key}
              options={options.map((item) => ({ value: item.key, label: `${item.method} ${item.key}` }))}
              hasSearch={options.length > 8}
              onChange={setSelectedKey}
            />
            <section {...stylex.props(styles.formGrid)}>
              <TextInput label="Method" value={method} onChange={setMethod} width="100%" />
              <TextInput label="Path" value={path} onChange={setPath} width="100%" />
              <TextInput label="Auth token" value={authToken} onChange={setAuthToken} width="100%" />
              <TextInput label="Title" value={title} onChange={setTitle} width="100%" />
            </section>
            <CheckboxInput label="Shared request" value={shared} onChange={setShared} />
            <TextArea label="Payload JSON" value={payloadText} onChange={setPayloadText} rows={8} hasSpellCheck={false} />
            {error !== '' ? <Text type="body" xstyle={styles.errorText}>{error}</Text> : null}
          </VStack>
        </Section>

        {response ? (
          <Section padding={4}>
            <VStack gap={3} as="section">
              <section {...stylex.props(styles.actionBar)}>
                <SectionHeading title="Response" description={`HTTP ${response.status_code}`} />
                {response.trace_id ? <Badge label={`trace ${response.trace_id.slice(0, 10)}`} variant={responseTrace?.is_error ? 'error' : 'neutral'} /> : null}
              </section>
              <Grid columns={{ minWidth: 180, max: 3 }} gap={3}>
                <Metric label="Status" value={response.status} />
                <Metric label="Code" value={String(response.status_code)} />
                <Metric label="Duration" value={responseTrace ? formatDuration(responseTrace.duration_nanos) : 'pending'} />
              </Grid>
              <CodeBlock code={formatJSON(tryParseJSON(apiResponseBody(response)))} language="json" width="100%" maxHeight={360} hasCopyButton />
              {recentOutput.length > 0 ? <CodeBlock title="Correlated Output" code={recentOutput.join('\n')} language="log" width="100%" maxHeight={220} /> : null}
            </VStack>
          </Section>
        ) : null}
      </VStack>
    </section>
  )

  async function callEndpoint() {
    setBusy(true)
    setError('')
    const nextCorrelationID = `dash-call-${requestSeq}`
    setRequestSeq((current) => current + 1)
    setCorrelationID(nextCorrelationID)
    try {
      const payload = parseJSONInput(payloadText)
      const result = await rpc.call<ApiCallResponse>('api-call', {
        app_id: appID,
        service: selected.svcName,
        endpoint: selected.rpcName,
        path,
        method,
        payload: JSON.stringify(payload),
        auth_token: authToken,
        correlation_id: nextCorrelationID,
      })
      setResponse(result)
    } catch (nextError) {
      setResponse(null)
      setError(nextError instanceof Error ? nextError.message : 'request failed')
    } finally {
      setBusy(false)
    }
  }

  async function saveRequest() {
    setBusy(true)
    setError('')
    try {
      const input = storedRequestInput(selected, title, method, payloadText, shared)
      if (storedID) {
        await rpc.updateStoredRequest(appID, storedID, input)
      } else {
        setStoredID(await rpc.createStoredRequest(appID, input))
      }
      await refreshStoredRequests()
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not save request')
    } finally {
      setBusy(false)
    }
  }

  async function deleteSelectedRequest() {
    if (!storedID) {
      return
    }
    setBusy(true)
    setError('')
    try {
      await rpc.deleteStoredRequest(appID, storedID)
      setStoredID('')
      await refreshStoredRequests()
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not delete request')
    } finally {
      setBusy(false)
    }
  }

  function openStoredRequest(item: StoredRequest) {
    const option = options.find((candidate) => candidate.svcName === item.svcName && candidate.rpcName === item.rpcName)
    if (option) {
      initializedEndpointKeyRef.current = `${appID}:${option.key}`
      setSelectedKey(option.key)
      setMethod(item.data.method || option.method)
      setPath(materializePath(option.path, item.data.pathParams))
    }
    setStoredID(item.id)
    setTitle(item.title)
    setShared(item.shared)
    setPayloadText(formatJSON(item.data.payload ?? {}))
    setResponse(null)
    setError('')
  }
}

export function ServiceCatalogPage({
  status,
}: {
  status: AppStatus | null
}) {
  const services = useMemo(() => status?.meta?.svcs ?? [], [status?.meta?.svcs])
  const options = useMemo(() => endpointOptions(status), [status])
  const [search, setSearch] = useState('')
  const [selectedKey, setSelectedKey] = useState('')
  const selected = useMemo(() => options.find((item) => item.key === selectedKey) ?? options[0] ?? null, [options, selectedKey])
  const visibleServices = useMemo(() => filterServices(services, search), [search, services])
  const middleware = useMemo(() => middlewareForService(status?.meta?.middleware ?? [], selected?.svcName), [selected?.svcName, status])

  useEffect(() => {
    if (selected && selected.key !== selectedKey) {
      setSelectedKey(selected.key)
    }
  }, [selected, selectedKey])

  if (services.length === 0) {
    return <EmptyPanel title="Service Catalog" message="No services discovered for this app." />
  }

  return (
    <section {...stylex.props(styles.twoColumn)} data-scenery-ui="ConsoleNextServiceCatalog">
      <Section padding={4}>
        <VStack gap={4} as="section">
          <TextInput label="Search" value={search} onChange={setSearch} width="100%" />
          <section {...stylex.props(styles.list)}>
            {visibleServices.map((service) => (
              <Card key={service.name} padding={3}>
                <VStack gap={2} as="section">
                  <Text type="label" weight="semibold">{service.name}</Text>
                  {(service.rpcs ?? []).map((endpoint) => {
                    const key = `${service.name}.${endpoint.name}`
                    return (
                      <Button
                        key={key}
                        label={endpoint.name}
                        size="sm"
                        variant={key === selected?.key ? 'primary' : 'secondary'}
                        onClick={() => setSelectedKey(key)}
                        endContent={<Badge label={endpoint.access_type ?? 'public'} variant="neutral" />}
                      />
                    )
                  })}
                </VStack>
              </Card>
            ))}
          </section>
        </VStack>
      </Section>

      <VStack gap={4} as="section">
        <Section padding={4}>
          <VStack gap={4} as="section">
            <section {...stylex.props(styles.actionBar)}>
              <SectionHeading title={selected?.key ?? 'Endpoint'} description={selected?.path ?? 'No endpoint selected'} />
              <Badge label={selected?.accessType ?? 'unknown'} variant="neutral" />
            </section>
            <Grid columns={{ minWidth: 180, max: 4 }} gap={3}>
              <Metric label="Services" value={String(services.length)} />
              <Metric label="Endpoints" value={String(options.length)} />
              <Metric label="Middleware" value={String(status?.meta?.middleware?.length ?? 0)} />
              <Metric label="API" value={status?.addr ?? 'n/a'} />
            </Grid>
            {status?.meta?.auth_handler ? (
              <Card padding={3}>
                <Text type="body">Auth handler: {status.meta.auth_handler.name} {status.meta.auth_handler.pkg_path ?? ''}</Text>
              </Card>
            ) : null}
            {middleware.length > 0 ? (
              <Section variant="muted" padding={3}>
                <VStack gap={2} as="section">
                  <Text type="label" weight="semibold">Middleware</Text>
                  {middleware.map((item) => (
                    <Text key={`${item.name.pkg}.${item.name.name}.${item.service_name ?? 'global'}`} type="supporting">
                      {item.name.name} ({item.global ? 'global' : item.service_name ?? 'scoped'})
                    </Text>
                  ))}
                </VStack>
              </Section>
            ) : null}
          </VStack>
        </Section>

        {selected ? (
          <Grid columns={{ minWidth: 320, max: 2 }} gap={4}>
            <Section padding={4}>
              <VStack gap={3} as="section">
                <SectionHeading title="Endpoint Details" description={selected.path} />
                <Grid columns={{ minWidth: 140, max: 2 }} gap={3}>
                  <Metric label="Method" value={selected.method} />
                  <Metric label="Access" value={selected.accessType} />
                  <Metric label="Service" value={selected.svcName} />
                  <Metric label="Endpoint" value={selected.rpcName} />
                </Grid>
                {selected.rpc?.loc ? (
                  <Card padding={3}>
                    <Text type="body" xstyle={styles.codeText}>
                      {selected.rpc.loc.filename ?? selected.rpc.loc.pkg_path ?? 'source'}:{selected.rpc.loc.src_line_start ?? 0}
                    </Text>
                  </Card>
                ) : null}
              </VStack>
            </Section>
            <Section padding={4}>
              <VStack gap={3} as="section">
                <SectionHeading title="Schemas" description={selected.rpc?.wire?.available ? 'wire available' : selected.rpc?.wire?.unsupported_reason ?? 'JSON'} />
                <CodeBlock title="Request" code={formatJSON(selected.rpc?.request_schema ?? { type: 'empty' })} language="json" width="100%" maxHeight={260} />
                <CodeBlock title="Response" code={formatJSON(selected.rpc?.response_schema ?? { type: 'empty' })} language="json" width="100%" maxHeight={260} />
              </VStack>
            </Section>
          </Grid>
        ) : null}
      </VStack>
    </section>
  )
}

export function TracesWorkbenchPage({
  traces,
  onClear,
  loading,
}: {
  traces: TraceSummary[]
  onClear: () => void
  loading: boolean
}) {
  const [selectedID, setSelectedID] = useState('')
  const rows = useMemo<TraceRow[]>(
    () =>
      traces.map((trace) => ({
        id: traceKey(trace),
        started: formatTimestamp(trace.started_at),
        name: traceName(trace),
        duration: formatDuration(trace.duration_nanos),
        status: trace.is_error ? 'error' : 'ok',
      })),
    [traces],
  )
  const selected = traces.find((trace) => traceKey(trace) === selectedID) ?? traces[0] ?? null

  useEffect(() => {
    if (selected && selectedID !== traceKey(selected)) {
      setSelectedID(traceKey(selected))
    }
  }, [selected, selectedID])

  return (
    <VStack gap={4} as="section" data-scenery-ui="ConsoleNextTraces">
      <Section padding={4}>
        <section {...stylex.props(styles.actionBar)}>
          <SectionHeading title="Traces" description={`${traces.length} spans loaded`} />
          <Button label="Clear traces" size="sm" variant="secondary" isLoading={loading} isDisabled={traces.length === 0} onClick={onClear} />
        </section>
      </Section>
      {traces.length === 0 ? <EmptyPanel title="Traces" message="No local traces recorded yet." /> : (
        <Grid columns={{ minWidth: 320, max: 2 }} gap={4}>
          <Section padding={0} xstyle={styles.tableFrame}>
            <Table data={rows} columns={traceColumns} idKey="id" density="compact" dividers="rows" hasHover textOverflow="truncate" />
          </Section>
          <Section padding={4}>
            <VStack gap={3} as="section">
              <Selector
                label="Selected trace"
                value={selected ? traceKey(selected) : ''}
                options={traces.map((trace) => ({ value: traceKey(trace), label: `${trace.is_error ? 'error' : 'ok'} ${traceName(trace)}` }))}
                hasSearch={traces.length > 8}
                onChange={setSelectedID}
              />
              {selected ? <TraceDetail trace={selected} /> : null}
            </VStack>
          </Section>
        </Grid>
      )}
    </VStack>
  )
}

export function DatabasesWorkbenchPage({
  appID,
  rpc,
  databases,
  selectedDatabase,
  onDatabaseChange,
  tables,
  selectedTable,
  onTableChange,
  schema,
  rows,
  error,
}: {
  appID: string
  rpc: DashboardRPC
  databases: SQLDatabase[]
  selectedDatabase: string
  onDatabaseChange: (value: string) => void
  tables: SQLiteTable[]
  selectedTable: string
  onTableChange: (value: string) => void
  schema: SQLiteColumn[]
  rows: SQLiteRows | null
  error: string
}) {
  const [sql, setSQL] = useState('select name from sqlite_schema where type = "table" order by name;')
  const [paramsText, setParamsText] = useState('[]')
  const [arrayMode, setArrayMode] = useState(false)
  const [queryRows, setQueryRows] = useState<unknown[]>([])
  const [queryError, setQueryError] = useState('')
  const [querying, setQuerying] = useState(false)
  const dataRows = useMemo(() => sqliteDataRows(rows), [rows])
  const dataColumns = useMemo(() => sqliteDataColumns(rows), [rows])
  const queryDataRows = useMemo(() => queryRows.map(rowToRecord), [queryRows])
  const queryColumns = useMemo(() => inferColumns(queryRows).map((column) => ({ key: column, header: column, width: proportional(1) })), [queryRows])

  if (databases.length === 0) {
    return (
      <VStack gap={4} as="section" data-scenery-ui="ConsoleNextDatabases">
        <EmptyPanel title="Databases" message="No SQLite databases discovered for this app." />
      </VStack>
    )
  }

  return (
    <VStack gap={4} as="section" data-scenery-ui="ConsoleNextDatabases">
      <Grid columns={{ minWidth: 260, max: 3 }} gap={4}>
        {databases.map((database) => (
          <Card key={database.name} padding={4}>
            <VStack gap={2} as="section">
              <HStack gap={2} vAlign="center">
                <StatusDot variant={database.exists === false ? 'warning' : 'success'} label={database.name} />
                <Heading level={2} accessibilityLevel={3}>{database.name}</Heading>
              </HStack>
              <Text type="supporting" color="secondary">{database.file_label ?? database.path ?? 'sqlite database'}</Text>
            </VStack>
          </Card>
        ))}
      </Grid>

      <Grid columns={{ minWidth: 320, max: 2 }} gap={4}>
        <Section padding={4}>
          <VStack gap={4} as="section">
            <SectionHeading title="Browse Tables" description="Read-only table preview" />
            <Selector label="Database" value={selectedDatabase} options={databases.map((database) => ({ value: database.name, label: database.name }))} onChange={onDatabaseChange} />
            <Selector label="Table" value={selectedTable} options={tables.map((table) => ({ value: table.name, label: `${table.name} (${table.type})` }))} placeholder="No tables" isDisabled={tables.length === 0} onChange={onTableChange} />
            {error !== '' ? <Text type="body" xstyle={styles.errorText}>{error}</Text> : null}
            {schema.length > 0 ? <CodeBlock title="Schema" code={formatJSON(schema)} language="json" width="100%" maxHeight={220} /> : null}
          </VStack>
        </Section>

        <Section padding={4}>
          <VStack gap={4} as="section">
            <SectionHeading title="Run SQL" description="Dashboard db/query RPC" />
            <TextArea label="SQL" value={sql} onChange={setSQL} rows={8} hasSpellCheck={false} />
            <TextArea label="Params JSON array" value={paramsText} onChange={setParamsText} rows={3} hasSpellCheck={false} />
            <CheckboxInput label="Return row arrays" value={arrayMode} onChange={setArrayMode} />
            <Button label="Run query" variant="primary" isLoading={querying} clickAction={runQuery} />
            {queryError !== '' ? <Text type="body" xstyle={styles.errorText}>{queryError}</Text> : null}
          </VStack>
        </Section>
      </Grid>

      {dataRows.length > 0 && dataColumns.length > 0 ? (
        <Section padding={0} xstyle={styles.tableFrame}>
          <Table data={dataRows} columns={dataColumns} idKey="__id" density="compact" dividers="grid" hasHover textOverflow="truncate" />
        </Section>
      ) : null}

      {queryDataRows.length > 0 && queryColumns.length > 0 ? (
        <Section padding={0} xstyle={styles.tableFrame}>
          <Table data={queryDataRows} columns={queryColumns} idKey="__id" density="compact" dividers="grid" hasHover textOverflow="truncate" />
        </Section>
      ) : null}
    </VStack>
  )

  async function runQuery() {
    setQuerying(true)
    setQueryError('')
    try {
      const result = await rpc.call<unknown[]>('db/query', {
        appId: appID,
        dbId: selectedDatabase,
        query: sql,
        params: parseDBParams(paramsText),
        arrayMode,
      })
      setQueryRows(Array.isArray(result) ? result : [])
    } catch (nextError) {
      setQueryRows([])
      setQueryError(nextError instanceof Error ? nextError.message : 'query failed')
    } finally {
      setQuerying(false)
    }
  }
}

export function CronPage({ status, traces }: { status: AppStatus | null; traces: TraceSummary[] }) {
  const jobs = status?.meta?.cron_jobs ?? []
  const items = jobs.map((job) => {
    const recent = traces
      .filter((trace) => trace.service_name === job.endpoint?.service_name && trace.endpoint_name === job.endpoint?.rpc_name)
      .slice(0, 5)
    return { job, recent, last: recent[0] ?? null }
  })
  return (
    <VStack gap={4} as="section" data-scenery-ui="ConsoleNextCron">
      <Grid columns={{ minWidth: 220, max: 3 }} gap={4}>
        <Metric label="Jobs" value={String(jobs.length)} />
        <Metric label="Jobs With Runs" value={String(items.filter((item) => item.recent.length > 0).length)} />
        <Metric label="Recent Executions" value={String(items.reduce((count, item) => count + item.recent.length, 0))} />
      </Grid>
      {items.length === 0 ? <EmptyPanel title="Cron" message="No cron jobs discovered in this app." /> : (
        <Grid columns={{ minWidth: 300, max: 2 }} gap={4}>
          {items.map(({ job, last, recent }) => (
            <Card key={job.id} padding={4}>
              <VStack gap={3} as="section">
                <section {...stylex.props(styles.actionBar)}>
                  <Heading level={2} accessibilityLevel={3}>{job.title || job.id}</Heading>
                  <Badge label={last ? (last.is_error ? 'error' : 'ok') : 'idle'} variant={last?.is_error ? 'error' : 'neutral'} />
                </section>
                <Text type="supporting" color="secondary">{job.schedule || job.every || 'unspecified schedule'}</Text>
                <Text type="body" xstyle={styles.codeText}>{job.endpoint?.service_name ?? '?'}.{job.endpoint?.rpc_name ?? '?'}</Text>
                <section {...stylex.props(styles.compactList)}>
                  {recent.map((trace) => (
                    <Text key={traceKey(trace)} type="supporting">{formatTime(trace.started_at)} {formatDuration(trace.duration_nanos)} {trace.is_error ? 'error' : 'ok'}</Text>
                  ))}
                  {recent.length === 0 ? <Text type="supporting" color="secondary">No matching local traces yet.</Text> : null}
                </section>
              </VStack>
            </Card>
          ))}
        </Grid>
      )}
    </VStack>
  )
}

export function LogsAndOutputPage({ outputs }: { outputs: ProcessOutput[] }) {
  const rows = outputs.map((item, index) => ({
    id: `${item.created_at}-${item.pid}-${item.stream}-${index}`,
    time: formatTimestamp(item.created_at),
    stream: item.stream,
    pid: item.pid,
    output: processOutputText(item),
  }))
  if (rows.length === 0) {
    return <EmptyPanel title="Process Output" message="No process output recorded yet." />
  }
  return (
    <Section padding={0} xstyle={styles.tableFrame} data-scenery-ui="ConsoleNextProcessOutput">
      <Table data={rows} columns={outputColumns} idKey="id" density="compact" dividers="rows" hasHover textOverflow="truncate" />
    </Section>
  )
}

function SectionHeading({ title, description }: { title: string; description: string }) {
  return (
    <VStack gap={1} as="section">
      <Heading level={2}>{title}</Heading>
      <Text type="supporting" color="secondary">{description}</Text>
    </VStack>
  )
}

function EmptyPanel({ title, message }: { title: string; message: string }) {
  return (
    <Section padding={4}>
      <VStack gap={2} as="section">
        <Heading level={2}>{title}</Heading>
        <Text type="body" color="secondary">{message}</Text>
      </VStack>
    </Section>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Card padding={3}>
      <VStack gap={1} as="section">
        <Text type="supporting" color="secondary">{label}</Text>
        <Text type="body" weight="semibold" maxLines={1}>{value}</Text>
      </VStack>
    </Card>
  )
}

function TraceDetail({ trace }: { trace: TraceSummary }) {
  return (
    <VStack gap={3} as="section">
      <SectionHeading title="Selected Trace" description={trace.trace_id} />
      <Grid columns={{ minWidth: 180, max: 2 }} gap={3}>
        <Metric label="Span" value={trace.span_id} />
        <Metric label="Name" value={traceName(trace)} />
        <Metric label="Status" value={trace.is_error ? 'error' : 'ok'} />
        <Metric label="Duration" value={formatDuration(trace.duration_nanos)} />
        <Metric label="Started" value={formatTimestamp(trace.started_at)} />
        <Metric label="Parent" value={trace.parent_span_id ?? 'n/a'} />
      </Grid>
    </VStack>
  )
}

function storedRequestInput(
  selected: EndpointOption,
  title: string,
  method: string,
  payloadText: string,
  shared: boolean,
): StoredRequestInput {
  return {
    title: title.trim() || selected.key,
    rpcName: selected.rpcName,
    svcName: selected.svcName,
    shared,
    data: {
      method,
      pathParams: [],
      payload: parseJSONInput(payloadText),
    },
  }
}

function schemaExampleValue(schema: unknown): unknown {
  if (!schema || typeof schema !== 'object') {
    return {}
  }
  const record = schema as Record<string, unknown>
  if (record.type === 'object' && record.properties && typeof record.properties === 'object') {
    const out: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(record.properties as Record<string, unknown>)) {
      out[key] = schemaExampleValue(value)
    }
    return out
  }
  if (record.type === 'array') {
    return []
  }
  if (record.type === 'number' || record.type === 'integer') {
    return 0
  }
  if (record.type === 'boolean') {
    return false
  }
  return {}
}

function parseDBParams(text: string): unknown[] {
  const trimmed = text.trim()
  if (trimmed === '') {
    return []
  }
  const parsed = JSON.parse(trimmed)
  return Array.isArray(parsed) ? parsed : []
}

function sqliteDataRows(rows: SQLiteRows | null): DBRow[] {
  return rows?.rows.map((values, index) => {
    const item: DBRow = { __id: String(index) }
    rows.columns.forEach((column, columnIndex) => {
      item[column] = cellText(values[columnIndex])
    })
    return item
  }) ?? []
}

function sqliteDataColumns(rows: SQLiteRows | null): TableColumn<DBRow>[] {
  return rows?.columns.map((column) => ({ key: column, header: column, width: proportional(1) })) ?? []
}

function rowToRecord(row: unknown, index: number): DBRow {
  if (Array.isArray(row)) {
    return row.reduce<DBRow>((out, value, columnIndex) => {
      out[String(columnIndex)] = cellText(value)
      return out
    }, { __id: String(index) })
  }
  if (row !== null && typeof row === 'object') {
    return Object.entries(row as Record<string, unknown>).reduce<DBRow>((out, [key, value]) => {
      out[key] = cellText(value)
      return out
    }, { __id: String(index) })
  }
  return { __id: String(index), value: cellText(row) }
}

function cellText(value: unknown): string {
  if (value === null || value === undefined) {
    return 'null'
  }
  if (typeof value === 'object') {
    return JSON.stringify(value)
  }
  return String(value)
}

function traceKey(trace: TraceSummary): string {
  return `${trace.trace_id}:${trace.span_id}`
}

function traceName(trace: TraceSummary): string {
  return `${trace.service_name ?? 'unknown'}.${trace.endpoint_name ?? trace.type}`
}
