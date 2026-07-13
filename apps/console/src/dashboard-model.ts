import type { BadgeVariant } from '@astryxdesign/core/Badge'
import type { AppStatus, AppSummary } from './scenery'

export type Page = 'Overview' | 'API' | 'Catalog' | 'Logs' | 'Output' | 'Traces' | 'Databases' | 'Cron' | 'Symphony'

export type RouteLink = {
  id: string
  label: string
  href: string
  kind: 'route' | 'alias'
}

export const pages: Page[] = ['Overview', 'API', 'Catalog', 'Logs', 'Output', 'Traces', 'Databases', 'Cron', 'Symphony']

export function appOptions(apps: AppSummary[]) {
  return apps.map((app) => ({
    value: app.id,
    label: appLabel(app),
  }))
}

function appLabel(app: AppSummary): string {
  return app.name || app.base_app_id || app.id || 'unnamed app'
}

export function chooseAppID(apps: AppSummary[], requested: string): string {
  if (requested !== '' && apps.some((app) => app.id === requested)) {
    return requested
  }
  return apps.find((app) => app.offline !== true)?.id ?? apps[0]?.id ?? ''
}

export function dashboardStatusLabel(
  status: AppStatus | null,
  selectedApp: AppSummary | null,
  connected: boolean,
): { label: string; variant: BadgeVariant } {
  if (!connected) {
    return { label: 'waiting for dashboard', variant: 'neutral' }
  }
  if (status?.compileError || selectedApp?.compileError) {
    return { label: 'compile error', variant: 'error' }
  }
  if (status?.compiling) {
    return { label: 'compiling', variant: 'warning' }
  }
  if (status?.running) {
    return { label: 'running', variant: 'success' }
  }
  if (selectedApp?.offline) {
    return { label: 'offline', variant: 'warning' }
  }
  return { label: 'connected', variant: 'info' }
}

export function routeLinks(status: AppStatus | null): RouteLink[] {
  const items: RouteLink[] = []
  for (const [label, href] of Object.entries(status?.routes ?? {})) {
    items.push({ id: `route:${label}:${href}`, label, href: absoluteURL(href), kind: 'route' })
  }
  for (const [label, href] of Object.entries(status?.aliases ?? {})) {
    items.push({ id: `alias:${label}:${href}`, label, href: absoluteURL(href), kind: 'alias' })
  }
  const seen = new Set<string>()
  return items.filter((item) => {
    if (seen.has(item.href)) {
      return false
    }
    seen.add(item.href)
    return true
  })
}

function absoluteURL(value: string): string {
  if (value.startsWith('http://') || value.startsWith('https://')) {
    return value
  }
  return new URL(value, window.location.origin).toString()
}

export function shortID(value: string): string {
  if (value.length <= 12) {
    return value
  }
  return value.slice(0, 12)
}
