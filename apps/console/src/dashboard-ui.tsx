import { useMemo } from 'react'
import * as stylex from '@stylexjs/stylex'
import { Badge } from '@astryxdesign/core/Badge'
import type { BadgeVariant } from '@astryxdesign/core/Badge'
import { Card } from '@astryxdesign/core/Card'
import { Grid } from '@astryxdesign/core/Grid'
import { HStack } from '@astryxdesign/core/HStack'
import { Heading } from '@astryxdesign/core/Heading'
import { Link } from '@astryxdesign/core/Link'
import { Section } from '@astryxdesign/core/Section'
import { StatusDot } from '@astryxdesign/core/StatusDot'
import type { StatusDotVariant } from '@astryxdesign/core/StatusDot'
import { Table, pixel, proportional } from '@astryxdesign/core/Table'
import type { TableColumn } from '@astryxdesign/core/Table'
import { Text } from '@astryxdesign/core/Text'
import { VStack } from '@astryxdesign/core/VStack'
import type {
  AppStatus,
  DashboardEvent,
  DevLogEntry,
  ObservabilitySignal,
  SQLDatabase,
  TraceSummary,
} from './scenery'
import type { RouteLink } from './dashboard-model'

interface LogRow extends Record<string, unknown> {
  id: string
  time: string
  source: string
  level: string
  message: string
}

const styles = stylex.create({
  cardContent: {
    minHeight: '8rem',
    display: 'grid',
    alignContent: 'space-between',
    gap: 'var(--spacing-4)',
  },
  cardTop: {
    display: 'flex',
    justifyContent: 'space-between',
    gap: 'var(--spacing-3)',
  },
  tableFrame: {
    minWidth: 0,
  },
  codeText: {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  },
  routeList: {
    display: 'grid',
    gap: 'var(--spacing-2)',
  },
  routeRow: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) auto',
    alignItems: 'center',
    gap: 'var(--spacing-3)',
    paddingBlock: 'var(--spacing-2)',
    borderBottomWidth: 'var(--border-width)',
    borderBottomStyle: 'solid',
    borderBottomColor: 'var(--color-border-subtle)',
  },
  activityList: {
    display: 'grid',
    gap: 'var(--spacing-2)',
  },
  activityRow: {
    display: 'grid',
    gridTemplateColumns: 'auto minmax(0, 1fr) auto',
    alignItems: 'center',
    gap: 'var(--spacing-3)',
  },
})

const logColumns: TableColumn<LogRow>[] = [
  { key: 'time', header: 'Time', width: pixel(152) },
  { key: 'source', header: 'Source', width: proportional(1) },
  { key: 'level', header: 'Level', width: pixel(96) },
  { key: 'message', header: 'Message', width: proportional(3) },
]

export function OverviewPage({
  connected,
  status,
  logs,
  traces,
  databases,
  serviceLinks,
  events,
}: {
  connected: boolean
  status: AppStatus | null
  logs: DevLogEntry[]
  traces: TraceSummary[]
  databases: SQLDatabase[]
  serviceLinks: RouteLink[]
  events: DashboardEvent[]
}) {
  return (
    <VStack gap={4} as="section" data-scenery-ui="ConsoleOverview">
      <Section aria-labelledby="observability-title" padding={4}>
        <VStack gap={4} as="section">
          <SectionHeading
            id="observability-title"
            title="Observability"
            description={status?.observability?.message ?? observabilitySummary(status)}
          />
          <Grid columns={{ minWidth: 240, max: 3 }} gap={2}>
            {observabilityCards(status).map((signal) => (
              <SignalRow key={signal.label} signal={signal} />
            ))}
          </Grid>
        </VStack>
      </Section>

      <Grid columns={{ minWidth: 260, max: 4 }} gap={4}>
        <MetricCard
          title="Dashboard RPC"
          detail={connected ? 'WebSocket connected' : 'Waiting for connection'}
          value={connected ? 'online' : 'offline'}
          variant={connected ? 'success' : 'neutral'}
        />
        <MetricCard
          title="App API"
          detail={firstRoute(serviceLinks)?.href ?? 'No route'}
          value={firstRoute(serviceLinks)?.label ?? 'none'}
          variant={firstRoute(serviceLinks) ? 'success' : 'neutral'}
        />
        <MetricCard
          title="Services"
          detail="discovered app services"
          value={String(status?.meta?.svcs?.length ?? 0)}
          variant={(status?.meta?.svcs?.length ?? 0) > 0 ? 'success' : 'neutral'}
        />
        <MetricCard
          title="Databases"
          detail="Postgres databases"
          value={String(databases.length)}
          variant={databases.length > 0 ? 'success' : 'neutral'}
        />
      </Grid>

      <Grid columns={{ minWidth: 320, max: 2 }} gap={4}>
        <ServiceLinksPanel links={serviceLinks} />
        <ActivityPanel logs={logs} traces={traces} events={events} />
      </Grid>
    </VStack>
  )
}

export function LogsPage({ logs }: { logs: DevLogEntry[] }) {
  const rows = useMemo<LogRow[]>(
    () =>
      logs.map((entry) => ({
        id: String(entry.id),
        time: formatTime(entry.time),
        source: entry.source.name ?? entry.source.role ?? entry.source.id,
        level: entry.level || 'info',
        message: entry.message || entry.raw || '',
      })),
    [logs],
  )

  if (rows.length === 0) {
    return <EmptyPanel title="Logs" message="No runtime logs have arrived yet." />
  }

  return (
    <Section padding={0} xstyle={styles.tableFrame}>
      <Table
        data={rows}
        columns={logColumns}
        idKey="id"
        density="compact"
        dividers="rows"
        hasHover
        textOverflow="truncate"
      />
    </Section>
  )
}

function ServiceLinksPanel({ links }: { links: RouteLink[] }) {
  return (
    <Section padding={4}>
      <VStack gap={4} as="section">
        <SectionHeading title="Service Links" description="Routes and app services for quick navigation" />
        {links.length === 0 ? (
          <Text type="body" color="secondary">
            No routes discovered yet.
          </Text>
        ) : (
          <section {...stylex.props(styles.routeList)}>
            {links.map((link) => (
              <section key={link.id} {...stylex.props(styles.routeRow)}>
                <VStack gap={0.5} as="section">
                  <Text type="label" weight="semibold">
                    {link.label}
                  </Text>
                  <Text type="supporting" color="secondary" maxLines={1} xstyle={styles.codeText}>
                    {link.href}
                  </Text>
                </VStack>
                <Link href={link.href} target="_blank" isExternalLink isStandalone>
                  Open
                </Link>
              </section>
            ))}
          </section>
        )}
      </VStack>
    </Section>
  )
}

function ActivityPanel({
  logs,
  traces,
  events,
}: {
  logs: DevLogEntry[]
  traces: TraceSummary[]
  events: DashboardEvent[]
}) {
  const items = [
    ...events.slice(0, 3).map((event) => ({
      id: `event-${event.method}`,
      badge: 'rpc',
      text: event.method,
      detail: '',
      variant: 'info' as BadgeVariant,
    })),
    ...logs.slice(0, 4).map((log) => ({
      id: `log-${log.id}`,
      badge: log.level || 'log',
      text: log.message || log.raw || 'log entry',
      detail: formatTime(log.time),
      variant: (log.level === 'error' ? 'error' : 'neutral') as BadgeVariant,
    })),
    ...traces.slice(0, 3).map((trace) => ({
      id: `trace-${trace.trace_id}-${trace.span_id}`,
      badge: trace.is_error ? 'trace error' : 'trace',
      text: trace.endpoint_name ?? trace.service_name ?? trace.trace_id,
      detail: formatDuration(trace.duration_nanos),
      variant: (trace.is_error ? 'error' : 'neutral') as BadgeVariant,
    })),
  ].slice(0, 8)

  return (
    <Section padding={4}>
      <VStack gap={4} as="section">
        <SectionHeading title="Activity" description={`${traces.length} traces, ${logs.length} logs`} />
        {items.length === 0 ? (
          <Text type="body" color="secondary">
            No dashboard events yet.
          </Text>
        ) : (
          <section {...stylex.props(styles.activityList)}>
            {items.map((item) => (
              <section key={item.id} {...stylex.props(styles.activityRow)}>
                <Badge label={item.badge} variant={item.variant} />
                <Text type="body" maxLines={1}>
                  {item.text}
                </Text>
                <Text type="supporting" color="secondary">
                  {item.detail}
                </Text>
              </section>
            ))}
          </section>
        )}
      </VStack>
    </Section>
  )
}

function EmptyPanel({ title, message }: { title: string; message: string }) {
  return (
    <Section padding={4}>
      <VStack gap={2} as="section">
        <Heading level={2}>{title}</Heading>
        <Text type="body" color="secondary">
          {message}
        </Text>
      </VStack>
    </Section>
  )
}

function SectionHeading({ id, title, description }: { id?: string; title: string; description: string }) {
  return (
    <VStack gap={1} as="section">
      <Heading level={2} accessibilityLevel={2} id={id}>
        {title}
      </Heading>
      <Text type="supporting" color="secondary">
        {description}
      </Text>
    </VStack>
  )
}

function SignalRow({ signal }: { signal: { label: string; status: string; detail: string; variant: BadgeVariant } }) {
  return (
    <Card padding={3}>
      <HStack gap={3} vAlign="center">
        <Badge label={signal.status} variant={signal.variant} />
        <VStack gap={0.5} as="section">
          <Text type="label" weight="semibold">
            {signal.label}
          </Text>
          <Text type="supporting" color="secondary">
            {signal.detail}
          </Text>
        </VStack>
      </HStack>
    </Card>
  )
}

function MetricCard({
  title,
  detail,
  value,
  variant,
}: {
  title: string
  detail: string
  value: string
  variant: StatusDotVariant
}) {
  return (
    <Card padding={4}>
      <section {...stylex.props(styles.cardContent)}>
        <section {...stylex.props(styles.cardTop)}>
          <VStack gap={1} as="section">
            <Heading level={2} accessibilityLevel={3}>
              {title}
            </Heading>
            <Text type="supporting" color="secondary" maxLines={1}>
              {detail}
            </Text>
          </VStack>
          <StatusDot variant={variant} label={title} />
        </section>
        <VStack gap={1} as="section">
          <Text type="display-3" weight="bold" maxLines={1}>
            {value}
          </Text>
          <Text type="supporting" color="secondary">
            current signal
          </Text>
        </VStack>
      </section>
    </Card>
  )
}

function observabilitySummary(status: AppStatus | null): string {
  if (status?.observability?.backend) {
    return `${status.observability.backend} telemetry signals for this session`
  }
  return 'Telemetry signals for this session'
}

function observabilityCards(status: AppStatus | null) {
  return [
    signalCard('Metrics', status?.observability?.metrics),
    signalCard('Logs', status?.observability?.logs),
    signalCard('Traces', status?.observability?.traces),
  ]
}

function signalCard(label: string, signal: ObservabilitySignal | undefined) {
  if (signal === undefined) {
    return { label, status: 'unknown', detail: 'not configured', variant: 'neutral' as BadgeVariant }
  }
  const detail = signal.message ?? signal.url ?? signal.query_path ?? signal.dialect ?? 'configured'
  return {
    label,
    status: signal.status || (signal.available ? 'available' : 'unavailable'),
    detail,
    variant: signalVariant(signal),
  }
}

function signalVariant(signal: ObservabilitySignal): BadgeVariant {
  if (!signal.enabled) {
    return 'neutral'
  }
  if (signal.available) {
    return 'success'
  }
  if (signal.status === 'degraded') {
    return 'warning'
  }
  return 'error'
}

function firstRoute(links: RouteLink[]): RouteLink | undefined {
  return links.find((link) => link.label.toLowerCase().includes('api')) ?? links[0]
}

function formatTime(value: string): string {
  if (value === '') {
    return ''
  }
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) {
    return value
  }
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatDuration(nanos: number): string {
  if (!Number.isFinite(nanos) || nanos <= 0) {
    return '0ms'
  }
  const millis = nanos / 1_000_000
  if (millis < 1) {
    return `${Math.round(nanos / 1000)}us`
  }
  if (millis < 1000) {
    return `${millis.toFixed(millis < 10 ? 1 : 0)}ms`
  }
  return `${(millis / 1000).toFixed(2)}s`
}
