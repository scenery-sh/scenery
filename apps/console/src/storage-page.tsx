import { useEffect, useMemo, useRef, useState } from 'react'
import { Banner } from '@astryxdesign/core/Banner'
import { Button } from '@astryxdesign/core/Button'
import { FileInput } from '@astryxdesign/core/FileInput'
import { Heading } from '@astryxdesign/core/Heading'
import { HStack } from '@astryxdesign/core/HStack'
import { Selector } from '@astryxdesign/core/Selector'
import { Table, proportional } from '@astryxdesign/core/Table'
import { Text } from '@astryxdesign/core/Text'
import { TextInput } from '@astryxdesign/core/TextInput'
import { VStack } from '@astryxdesign/core/VStack'
import type { DashboardRPC } from './scenery'
import { downloadStorage, inspectStorage, storageTarget, uploadStorage } from './storage-client'
import type { StorageInspection, StorageObject, StoragePage, StorageSelection, StorageTarget } from './storage-client'

export function StorageBrowserPage({ rpc, appID }: { rpc: DashboardRPC; appID: string }) {
  const [inspection, setInspection] = useState<StorageInspection | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [stats, setStats] = useState(false)
  const [store, setStore] = useState('')
  const [tenantDraft, setTenantDraft] = useState('')
  const [tenant, setTenant] = useState('')
  useEffect(() => {
    let active = true
    setInspection(null)
    setError('')
    void inspectStorage(rpc, appID, stats).then((result) => {
      if (active) setInspection(result)
    }).catch((cause: unknown) => { if (active) setError(String(cause)) })
    return () => { active = false }
  }, [rpc, appID, revision, stats])
  const selectedStore = inspection?.stores.find((item) => item.name === store) ?? inspection?.stores[0]
  const target = useMemo(() => inspection && selectedStore
    ? storageTarget(appID, inspection.storage.scope, selectedStore.name, selectedStore.tenant_scoped ? tenant : '') : null,
  [appID, inspection, selectedStore, tenant])
  return <VStack gap={4}>
    <HStack gap={3} wrap="wrap">
      <Button label="Refresh storage scope" onClick={() => { setStats(false); setRevision((value) => value + 1) }} />
      <Button label="Calculate totals" onClick={() => { setStats(true); setRevision((value) => value + 1) }} />
    </HStack>
    {error && <Banner status="error" title="Storage unavailable" description={error} />}
    {!inspection && !error && <Text>Loading storage scope…</Text>}
    {inspection && <>
      <Text type="code">{inspection.storage.scope.app_root}</Text>
      <Text type="supporting">Worktree: {inspection.storage.scope.worktree_key} · {inspection.storage.readiness}</Text>
      <Text type="supporting">Incarnation: {inspection.storage.scope.incarnation ?? 'unallocated'} · Generation: {inspection.storage.scope.generation ?? 'unallocated'}</Text>
      <Text>{inspection.storage.totals ? `Worktree totals at last calculation (all stores and tenants): ${inspection.storage.totals.objects} objects · ${inspection.storage.totals.bytes} bytes` : 'Totals not calculated.'}</Text>
      {selectedStore ? <>
        <Selector label="Store" value={selectedStore.name} options={inspection.stores.map((item) => ({ label: `${item.name} (${item.access})`, value: item.name }))} onChange={(value) => { setStore(value); setTenant(''); setTenantDraft('') }} />
        {selectedStore.tenant_scoped && <HStack gap={3}>
          <TextInput label="Tenant" value={tenantDraft} onChange={setTenantDraft} />
          <Button label="Apply tenant" onClick={() => setTenant(tenantDraft)} />
        </HStack>}
        {target && target.incarnation && target.generation && (!selectedStore.tenant_scoped || tenant !== '')
          ? <StorageNamespace key={JSON.stringify(target)} rpc={rpc} target={target} />
          : <Text>Select a tenant and start the app runtime if storage is not allocated.</Text>}
      </> : <Text>No object stores are declared.</Text>}
    </>}
  </VStack>
}

function StorageNamespace({ rpc, target }: { rpc: DashboardRPC; target: StorageTarget }) {
  const [draft, setDraft] = useState('')
  const [prefix, setPrefix] = useState('')
  return <VStack gap={4}>
    <HStack gap={3}>
      <TextInput label="Key prefix" value={draft} onChange={setDraft} />
      <Button label="Apply prefix" onClick={() => setPrefix(draft)} />
    </HStack>
    <StorageObjects key={prefix} rpc={rpc} target={target} prefix={prefix} />
  </VStack>
}

function StorageObjects({ rpc, target, prefix }: { rpc: DashboardRPC; target: StorageTarget; prefix: string }) {
  const [objects, setObjects] = useState<StorageObject[]>([])
  const [cursor, setCursor] = useState('')
  const [next, setNext] = useState('')
  const [revision, setRevision] = useState(0)
  const [selected, setSelected] = useState<StorageObject | null>(null)
  const [preview, setPreview] = useState<StorageSelection['preview'] | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [file, setFile] = useState<File | null>(null)
  const [key, setKey] = useState('')
  const [metadata, setMetadata] = useState('{}')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const lifetime = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    lifetime.current = controller
    return () => controller.abort()
  }, [])
  useEffect(() => {
    let active = true
    setObjects([])
    setNext('')
    void rpc.call<StoragePage>('storage/list', { ...target, prefix, cursor, limit: 100 }).then((result) => {
      if (active) { setObjects(result.page.objects); setNext(result.page.next_cursor ?? '') }
    }).catch((cause: unknown) => { if (active) setError(String(cause)) })
    return () => { active = false }
  }, [rpc, target, prefix, cursor, revision])
  async function run(action: (signal: AbortSignal) => Promise<void>) {
    const signal = lifetime.current?.signal
    if (!signal || signal.aborted || busy) return
    setBusy(true)
    setError('')
    try { await action(signal) } catch (cause) { if (!signal.aborted) setError(String(cause)) }
    finally { if (!signal.aborted) setBusy(false) }
  }
  function refresh() {
    setCursor(''); setSelected(null); setPreview(null); setConfirmDelete(false)
    setRevision((value) => value + 1)
  }
  async function select(object: StorageObject, signal: AbortSignal) {
    const result = await rpc.call<{ object: StorageObject }>('storage/stat', { ...target, key: object.key })
    if (signal.aborted) return
    setSelected(result.object); setKey(result.object.key)
    setMetadata(JSON.stringify(result.object.metadata ?? {})); setFile(null); setConfirmDelete(false)
  }
  return <VStack gap={4}>
    {error && <Banner status="error" title="Storage operation failed" description={error} />}
    <Text type="supporting">Store: {target.store} · Tenant: {target.tenant || '(unscoped)'} · Prefix: {prefix || '(all keys)'}</Text>
    <Table data={objects.map((object) => ({ ...object }))} idKey="key" columns={[
      { key: 'key', header: 'Key', width: proportional(3) },
      { key: 'size_bytes', header: 'Bytes', width: proportional(1) },
      { key: 'modified_at', header: 'Modified', width: proportional(2) },
      { key: 'actions', header: 'Details', width: proportional(1), renderCell: (row) => <Button label="Select" isDisabled={busy} onClick={() => void run((signal) => select(row, signal))} /> },
    ]} />
    <HStack gap={3}>
      <Button label="First page / refresh" isDisabled={busy} onClick={refresh} />
      <Button label="Next page" isDisabled={busy || !next} onClick={() => { setCursor(next); setSelected(null); setConfirmDelete(false) }} />
      <Button label="Preview prefix deletion" isDisabled={busy} onClick={() => void run(async (signal) => {
        const result = await rpc.call<StorageSelection>('storage/delete-preview', { ...target, prefix })
        if (!signal.aborted) setPreview(result.preview)
      })} />
    </HStack>
    {preview && <Banner status="warning" title={`Delete ${preview.objects} objects (${preview.bytes} bytes)?`} description={`This exact preview applies only to the scope above. Sample: ${preview.sample.join(', ') || '(empty)'}`} endContent={
      <Button label="Confirm preview deletion" variant="destructive" isDisabled={busy || preview.objects === 0} onClick={() => void run(async (signal) => {
        await rpc.call('storage/delete-selection', { ...target, prefix, selection_revision: preview.selection_revision })
        if (!signal.aborted) refresh()
      })} />
    } />}
    {selected && <VStack gap={2}>
      <Heading level={3}>{selected.key}</Heading>
      <Text type="code">ETag: {selected.etag}</Text>
      <Text type="code">SHA-256: {selected.sha256}</Text>
      <Text>{selected.size_bytes} bytes · {selected.content_type || 'application/octet-stream'} · {selected.modified_at}</Text>
      <Text type="code">Metadata: {JSON.stringify(selected.metadata ?? {})}</Text>
      <HStack gap={3}>
        <Button label="Download displayed version" isDisabled={busy} onClick={() => void run((signal) => downloadStorage(target, selected, signal))} />
        <Button label={confirmDelete ? 'Confirm delete displayed version' : 'Delete object'} variant="destructive" isDisabled={busy} onClick={() => {
          if (!confirmDelete) { setConfirmDelete(true); return }
          void run(async (signal) => {
            await rpc.call('storage/delete', { ...target, key: selected.key, if_match: selected.etag })
            if (!signal.aborted) refresh()
          })
        }} />
      </HStack>
    </VStack>}
    <Heading level={3}>Upload object</Heading>
    <TextInput label="Object key" value={key} onChange={setKey} isDisabled={busy} />
    <FileInput label="File" value={file} onChange={(value) => setFile(Array.isArray(value) ? value[0] ?? null : value)} isDisabled={busy} />
    <TextInput label="Metadata (JSON string map)" value={metadata} onChange={setMetadata} isDisabled={busy} />
    <Text>{selected?.key === key ? `Replace only ETag ${selected.etag}` : 'Create only: an existing key will not be overwritten.'}</Text>
    <Button label="Upload" variant="primary" isDisabled={busy || !file || !key} onClick={() => void run(async (signal) => {
      if (!file) return
      const parsed: unknown = JSON.parse(metadata)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || Object.values(parsed).some((value) => typeof value !== 'string')) throw new Error('Metadata must be a JSON object containing only string values.')
      await uploadStorage(target, key, file, parsed as Record<string, string>, selected?.key === key ? selected.etag : undefined, signal)
      if (!signal.aborted) { refresh(); setFile(null) }
    })} />
  </VStack>
}
