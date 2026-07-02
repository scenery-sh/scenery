import type {
  APIEncoding,
  ApiCallResponse,
  AppStatus,
  MetadataPath,
  MiddlewareMeta,
  ProcessOutput,
  ServiceMeta,
  ServiceRPC,
  TraceSummary,
} from './scenery'

export type EndpointOption = {
  key: string
  svcName: string
  rpcName: string
  method: string
  path: string
  accessType: string
  service?: ServiceMeta
  rpc?: ServiceRPC
}

export function endpointOptions(status: AppStatus | null): EndpointOption[] {
  const combined = new Map<string, EndpointOption>()
  for (const service of status?.apiEncoding?.services ?? []) {
    for (const rpc of service.rpcs ?? []) {
      const key = `${service.name}.${rpc.name}`
      combined.set(key, {
        key,
        svcName: service.name,
        rpcName: rpc.name,
        method: rpc.methods?.[0] || 'GET',
        path: rpc.path || `/${service.name}.${rpc.name}`,
        accessType: rpc.access_type || 'public',
      })
    }
  }
  for (const service of status?.meta?.svcs ?? []) {
    for (const rpc of service.rpcs ?? []) {
      const key = `${service.name}.${rpc.name}`
      const current = combined.get(key)
      combined.set(key, {
        key,
        svcName: service.name,
        rpcName: rpc.name,
        method: current?.method || rpc.http_methods?.[0] || 'GET',
        path: renderMetadataPath(rpc.path) || current?.path || `/${service.name}.${rpc.name}`,
        accessType: current?.accessType || rpc.access_type || 'public',
        service,
        rpc,
      })
    }
  }
  return Array.from(combined.values()).sort((a, b) => a.svcName.localeCompare(b.svcName) || a.rpcName.localeCompare(b.rpcName))
}

export function filterServices(services: ServiceMeta[], search: string): ServiceMeta[] {
  const needle = search.trim().toLowerCase()
  if (needle === '') {
    return services
  }
  return services
    .map((service) => ({
      ...service,
      rpcs: (service.rpcs ?? []).filter((rpc) =>
        `${service.name} ${rpc.name} ${rpc.access_type ?? ''} ${(rpc.http_methods ?? []).join(' ')}`
          .toLowerCase()
          .includes(needle),
      ),
    }))
    .filter((service) => service.name.toLowerCase().includes(needle) || (service.rpcs ?? []).length > 0)
}

export function middlewareForService(middleware: MiddlewareMeta[], serviceName?: string): MiddlewareMeta[] {
  return middleware.filter((item) => item.global || item.service_name === serviceName)
}

export function renderMetadataPath(path?: MetadataPath): string {
  if (!path?.segments) {
    return ''
  }
  return `/${path.segments.map((segment) => (segment.type === 'PARAM' ? `:${segment.value}` : segment.value)).join('/')}`
}

export function materializePath(template: string, params: unknown): string {
  if (Array.isArray(params)) {
    let index = 0
    return template.replace(/:([^/]+)/g, () => {
      const value = params[index]
      index += 1
      return value === undefined ? '' : encodeURIComponent(String(value))
    })
  }
  if (params === null || typeof params !== 'object') {
    return template
  }
  let next = template
  for (const [key, value] of Object.entries(params as Record<string, unknown>)) {
    next = next.replace(`:${key}`, encodeURIComponent(String(value ?? '')))
  }
  return next
}

export function parseJSONInput(text: string): unknown {
  const trimmed = text.trim()
  if (trimmed === '') {
    return {}
  }
  return JSON.parse(trimmed)
}

export function tryParseJSON(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

export function formatJSON(value: unknown): string {
  if (value === undefined) {
    return ''
  }
  if (typeof value === 'string') {
    return value
  }
  return JSON.stringify(value, null, 2)
}

export function decodeBase64Utf8(input: string): string {
  if (input === '') {
    return ''
  }
  try {
    const binary = atob(input)
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return input
  }
}

export function processOutputText(item: ProcessOutput): string {
  return decodeBase64Utf8(item.output).replace(/\r\n/g, '\n')
}

export function apiResponseBody(response: ApiCallResponse): string {
  return decodeBase64Utf8(response.body)
}

export function upsertTrace(list: TraceSummary[], next: TraceSummary): TraceSummary[] {
  const filtered = list.filter((item) => item.trace_id !== next.trace_id || item.span_id !== next.span_id)
  return [next, ...filtered].sort((a, b) => b.started_at.localeCompare(a.started_at))
}

export function formatDuration(nanos?: number): string {
  if (!Number.isFinite(nanos) || !nanos || nanos <= 0) {
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

export function formatTimestamp(value?: string): string {
  if (!value) {
    return ''
  }
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) {
    return value
  }
  return date.toLocaleString()
}

export function formatTime(value?: string): string {
  if (!value) {
    return ''
  }
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) {
    return value
  }
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function inferColumns(rows: unknown[]): string[] {
  const first = rows[0]
  if (Array.isArray(first)) {
    return first.map((_, index) => String(index))
  }
  if (first !== null && typeof first === 'object') {
    return Object.keys(first as Record<string, unknown>)
  }
  return first === undefined ? [] : ['value']
}

export function normalizeAPIEncoding(value: APIEncoding | undefined): APIEncoding {
  return value ?? { services: [] }
}
