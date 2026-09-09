import type { DashboardRPC } from './scenery'

export type StorageScope = {
  app_id: string
  app_root: string
  worktree_key: string
  incarnation: string | null
  generation: string | null
  store?: string
  tenant?: string
}

export type StorageObject = {
  store: string
  tenant?: string
  key: string
  size_bytes: number
  content_type?: string
  etag: string
  sha256: string
  modified_at: string
  metadata?: Record<string, string>
}

export type StorageInspection = {
  storage: {
    readiness: string
    scope: StorageScope
    totals?: { objects: number; bytes: number }
  }
  stores: {
    name: string
    access: string
    tenant_scoped: boolean
    max_object_bytes?: number
    object_count?: number
    total_bytes?: number
  }[]
}

export type StoragePage = {
  scope: StorageScope
  page: { objects: StorageObject[]; next_cursor?: string }
}

export type StorageSelection = {
  scope: StorageScope
  preview: { objects: number; bytes: number; sample: string[]; selection_revision: string }
}

export type StorageTarget = {
  app_id: string
  worktree_key: string
  incarnation: string
  generation: string
  store: string
  tenant: string
}

export function storageTarget(appID: string, scope: StorageScope, store: string, tenant: string): StorageTarget {
  return {
    app_id: appID,
    worktree_key: scope.worktree_key,
    incarnation: scope.incarnation ?? '',
    generation: scope.generation ?? '',
    store,
    tenant,
  }
}

export function inspectStorage(rpc: DashboardRPC, appID: string, stats = false): Promise<StorageInspection> {
  return rpc.call('storage/inspect', { app_id: appID, stats })
}

function transferURL(target: StorageTarget, key: string): string {
  const prefix = window.location.pathname.startsWith('/console') ? '/console' : ''
  return `${prefix}/__storage?${new URLSearchParams({ ...target, key })}`
}

async function requireTransfer(response: Response): Promise<void> {
  if (response.ok) return
  let message = `Storage transfer failed (${response.status})`
  try {
    const failure = await response.json() as { diagnostic?: string; message?: string; report_token?: string }
    if (failure.message) message = `${failure.diagnostic ?? 'Storage'}: ${failure.message}`
    if (failure.report_token) message += ` (${failure.report_token})`
  } catch { /* Retain HTTP status when an interrupted response is not JSON. */ }
  throw new Error(message)
}

export async function uploadStorage(target: StorageTarget, key: string, file: File, metadata: Record<string, string>, etag: string | undefined, signal: AbortSignal): Promise<StorageObject> {
  const encoded = new TextEncoder().encode(JSON.stringify(metadata))
  if (encoded.length > 16 * 1024) throw new Error('Metadata exceeds 16 KiB.')
  const headers: Record<string, string> = {
    'X-Scenery-Storage-Request': '1',
    'Content-Type': file.type || 'application/octet-stream',
    'X-Scenery-Storage-Metadata': btoa(String.fromCharCode(...encoded)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', ''),
  }
  if (etag) headers['If-Match'] = etag
  else headers['If-None-Match'] = '*'
  const response = await fetch(transferURL(target, key), { method: 'PUT', headers, body: file, signal, cache: 'no-store' })
  await requireTransfer(response)
  const result = await response.json() as { data: { object: StorageObject } }
  return result.data.object
}

export async function downloadStorage(target: StorageTarget, object: StorageObject, signal: AbortSignal): Promise<void> {
  const response = await fetch(transferURL(target, object.key), {
    headers: { 'X-Scenery-Storage-Request': '1', 'If-Match': object.etag }, signal, cache: 'no-store',
  })
  await requireTransfer(response)
  const blob = await response.blob()
  if (signal.aborted) return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = object.key.split('/').at(-1) || 'download'
  link.click()
  // Keep the URL alive until the browser has accepted its download navigation.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
