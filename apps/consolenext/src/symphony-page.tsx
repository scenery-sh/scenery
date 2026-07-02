import { useCallback, useEffect, useState } from 'react'
import * as stylex from '@stylexjs/stylex'
import { Badge } from '@astryxdesign/core/Badge'
import { Button } from '@astryxdesign/core/Button'
import { Card } from '@astryxdesign/core/Card'
import { HStack } from '@astryxdesign/core/HStack'
import { Heading } from '@astryxdesign/core/Heading'
import { Section } from '@astryxdesign/core/Section'
import { Selector } from '@astryxdesign/core/Selector'
import { Text } from '@astryxdesign/core/Text'
import { TextArea } from '@astryxdesign/core/TextArea'
import { TextInput } from '@astryxdesign/core/TextInput'
import { VStack } from '@astryxdesign/core/VStack'
import {
  DashboardRPC,
  type SymphonyState,
  type SymphonyStatus,
  type SymphonyTask,
  type SymphonyTaskInput,
} from './scenery'

type TaskForm = SymphonyTaskInput & {
  labelsText: string
}

const styles = stylex.create({
  toolbar: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 'var(--spacing-3)',
    flexWrap: 'wrap',
  },
  board: {
    display: 'grid',
    gridTemplateColumns: {
      default: '1fr',
      '@media (min-width: 760px)': 'repeat(2, minmax(16rem, 1fr))',
      '@media (min-width: 1180px)': 'repeat(4, minmax(14rem, 1fr))',
    },
    gap: 'var(--spacing-3)',
    alignItems: 'start',
  },
  column: {
    minWidth: 0,
    display: 'grid',
    gap: 'var(--spacing-2)',
  },
  columnHeader: {
    minHeight: '2rem',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 'var(--spacing-2)',
  },
  cardList: {
    minHeight: '8rem',
    display: 'grid',
    alignContent: 'start',
    gap: 'var(--spacing-2)',
  },
  taskButton: {
    width: '100%',
    padding: 0,
    borderWidth: 0,
    backgroundColor: 'transparent',
    color: 'inherit',
    textAlign: 'left',
    cursor: 'pointer',
  },
  cardMeta: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 'var(--spacing-2)',
    flexWrap: 'wrap',
  },
  labels: {
    display: 'flex',
    gap: 'var(--spacing-1)',
    flexWrap: 'wrap',
  },
  hiddenList: {
    display: 'grid',
    gap: 'var(--spacing-2)',
  },
  empty: {
    minHeight: '5rem',
    display: 'grid',
    placeItems: 'center',
    borderWidth: 'var(--border-width)',
    borderStyle: 'dashed',
    borderColor: 'var(--color-border)',
    borderRadius: 'var(--radius-2)',
    padding: 'var(--spacing-3)',
  },
  overlay: {
    position: 'fixed',
    inset: 0,
    zIndex: 5,
    display: 'grid',
    placeItems: 'center',
    padding: 'var(--spacing-4)',
    backgroundColor: 'rgba(0, 0, 0, 0.56)',
  },
  modal: {
    width: 'min(42rem, 100%)',
    maxHeight: 'calc(100vh - 2rem)',
    overflowY: 'auto',
  },
  formGrid: {
    display: 'grid',
    gridTemplateColumns: {
      default: '1fr',
      '@media (min-width: 680px)': 'repeat(2, minmax(0, 1fr))',
    },
    gap: 'var(--spacing-3)',
  },
  fullWidth: {
    gridColumn: '1 / -1',
  },
  errorText: {
    color: 'var(--color-error)',
  },
})

export function SymphonyPage({ appID, rpc }: { appID: string; rpc: DashboardRPC }) {
  const [state, setState] = useState<SymphonyState | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<SymphonyTask | null>(null)
  const [form, setForm] = useState<TaskForm | null>(null)

  const refresh = useCallback(async () => {
    if (appID === '') {
      setState(null)
      return
    }
    setLoading(true)
    setError('')
    try {
      setState(await rpc.symphonyState(appID))
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not load Symphony')
    } finally {
      setLoading(false)
    }
  }, [appID, rpc])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const statuses = state?.statuses ?? []
  const activeStatuses = statuses.filter((status) => !status.hidden)
  const hiddenStatuses = statuses.filter((status) => status.hidden)
  const tasks = state?.tasks ?? []
  const statusOptions = statuses.map((status) => ({ value: status.key, label: status.name }))

  const taskCounts = new Map<string, number>()
  for (const task of tasks) {
    taskCounts.set(task.status_key, (taskCounts.get(task.status_key) ?? 0) + 1)
  }

  function openCreate(statusKey = activeStatuses[0]?.key ?? 'backlog') {
    setEditing(null)
    setForm(emptyForm(statusKey))
  }

  function openEdit(task: SymphonyTask) {
    setEditing(task)
    setForm({
      title: task.title,
      description: task.description,
      status_key: task.status_key,
      priority: task.priority,
      assignee: task.assignee,
      estimate: task.estimate,
      branch_name: task.branch_name,
      url: task.url,
      source: task.source || 'manual',
      labels: task.labels ?? [],
      labelsText: (task.labels ?? []).join(', '),
    })
  }

  async function saveTask() {
    if (!form || appID === '') {
      return
    }
    setSaving(true)
    setError('')
    try {
      const input = formInput(form)
      if (editing) {
        await rpc.symphonyUpdateTask(appID, editing.id, input)
      } else {
        await rpc.symphonyCreateTask(appID, input)
      }
      setForm(null)
      setEditing(null)
      await refresh()
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not save task')
    } finally {
      setSaving(false)
    }
  }

  async function deleteTask() {
    if (!editing || appID === '') {
      return
    }
    setSaving(true)
    setError('')
    try {
      await rpc.symphonyDeleteTask(appID, editing.id)
      setForm(null)
      setEditing(null)
      await refresh()
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not delete task')
    } finally {
      setSaving(false)
    }
  }

  async function moveTask(task: SymphonyTask, direction: -1 | 1) {
    const index = activeStatuses.findIndex((status) => status.key === task.status_key)
    const nextStatus = activeStatuses[index + direction]
    if (!nextStatus || appID === '') {
      return
    }
    setSaving(true)
    setError('')
    try {
      const nextIndex = tasks.filter((item) => item.status_key === nextStatus.key).length
      setState(await rpc.symphonyMoveTask(appID, task.id, nextStatus.key, nextIndex))
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not move task')
    } finally {
      setSaving(false)
    }
  }

  async function toggleStatus(status: SymphonyStatus) {
    if (appID === '') {
      return
    }
    setSaving(true)
    setError('')
    try {
      const updates = statuses.map((item) => ({
        key: item.key,
        sort_order: item.sort_order,
        hidden: item.key === status.key ? !item.hidden : item.hidden,
      }))
      setState(await rpc.symphonyUpdateStatuses(appID, updates))
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : 'could not update columns')
    } finally {
      setSaving(false)
    }
  }

  if (appID === '') {
    return <EmptyPanel title="Symphony" message="No app selected." />
  }

  return (
    <VStack gap={4} as="section" data-scenery-ui="ConsoleNextSymphony">
      <section {...stylex.props(styles.toolbar)}>
        <VStack gap={1} as="section">
          <Heading level={2}>Symphony</Heading>
          <Text type="supporting" color="secondary">
            {tasks.length} tasks
          </Text>
        </VStack>
        <HStack gap={2} vAlign="center">
          <Button label="Refresh" size="sm" variant="secondary" isLoading={loading} onClick={() => void refresh()} />
          <Button label="New task" size="sm" onClick={() => openCreate()} />
        </HStack>
      </section>
      {error !== '' ? (
        <Text type="body" xstyle={styles.errorText}>
          {error}
        </Text>
      ) : null}
      <section {...stylex.props(styles.board)} data-scenery-ui="SymphonyBoard">
        {activeStatuses.map((status) => {
          const columnTasks = tasks.filter((task) => task.status_key === status.key)
          return (
            <section key={status.key} {...stylex.props(styles.column)} data-scenery-ui={`SymphonyColumn:${status.key}`}>
              <section {...stylex.props(styles.columnHeader)}>
                <HStack gap={2} vAlign="center">
                  <Badge label={status.name} variant={badgeVariant(status)} />
                  <Text type="supporting" color="secondary">
                    {columnTasks.length}
                  </Text>
                </HStack>
                <Button label="Add" size="sm" variant="ghost" onClick={() => openCreate(status.key)} />
              </section>
              <section {...stylex.props(styles.cardList)} data-scenery-state={columnTasks.length === 0 ? 'intentional-empty' : undefined}>
                {columnTasks.map((task) => (
                  <TaskCard
                    key={task.id}
                    task={task}
                    statuses={activeStatuses}
                    onOpen={() => openEdit(task)}
                    onMove={moveTask}
                    busy={saving}
                  />
                ))}
                {columnTasks.length === 0 ? (
                  <section {...stylex.props(styles.empty)}>
                    <Text type="supporting" color="secondary">
                      Empty
                    </Text>
                  </section>
                ) : null}
              </section>
            </section>
          )
        })}
      </section>
      <Section padding={4} data-scenery-ui="SymphonyHiddenColumns">
        <VStack gap={3} as="section">
          <HStack gap={2} vAlign="center">
            <Heading level={3}>Hidden columns</Heading>
            <Badge label={hiddenStatuses.length} variant="neutral" />
          </HStack>
          <section {...stylex.props(styles.hiddenList)}>
            {hiddenStatuses.map((status) => (
              <Card key={status.key} padding={3}>
                <HStack gap={2} vAlign="center" hAlign="between">
                  <VStack gap={1} as="section">
                    <Text type="label" weight="semibold">
                      {status.name}
                    </Text>
                    <Text type="supporting" color="secondary">
                      {taskCounts.get(status.key) ?? 0} tasks
                    </Text>
                  </VStack>
                  <Button label="Show" size="sm" variant="secondary" isDisabled={saving} onClick={() => void toggleStatus(status)} />
                </HStack>
              </Card>
            ))}
            {statuses.filter((status) => !status.hidden).map((status) => (
              <Button key={status.key} label={`Hide ${status.name}`} size="sm" variant="ghost" isDisabled={saving} onClick={() => void toggleStatus(status)} />
            ))}
          </section>
        </VStack>
      </Section>
      {form ? (
        <section {...stylex.props(styles.overlay)} role="presentation">
          <Section padding={4} xstyle={styles.modal} data-scenery-ui="SymphonyTaskModal">
            <VStack gap={4} as="section">
              <section {...stylex.props(styles.toolbar)}>
                <VStack gap={1} as="section">
                  <Heading level={2}>{editing ? editing.identifier : 'New task'}</Heading>
                  <Text type="supporting" color="secondary">
                    {editing ? 'Edit task' : 'Create task'}
                  </Text>
                </VStack>
                <Button label="Close" size="sm" variant="secondary" onClick={() => setForm(null)} />
              </section>
              <section {...stylex.props(styles.formGrid)}>
                <section {...stylex.props(styles.fullWidth)}>
                  <TextInput label="Title" value={form.title} onChange={(title) => setForm({ ...form, title })} width="100%" hasAutoFocus />
                </section>
                <section {...stylex.props(styles.fullWidth)}>
                  <TextArea label="Description" value={form.description} onChange={(description) => setForm({ ...form, description })} rows={5} />
                </section>
                <Selector label="Status" value={form.status_key} options={statusOptions} onChange={(status_key) => setForm({ ...form, status_key })} />
                <TextInput label="Priority" value={form.priority} onChange={(priority) => setForm({ ...form, priority })} width="100%" />
                <TextInput label="Assignee" value={form.assignee} onChange={(assignee) => setForm({ ...form, assignee })} width="100%" />
                <TextInput label="Estimate" value={form.estimate} onChange={(estimate) => setForm({ ...form, estimate })} width="100%" />
                <section {...stylex.props(styles.fullWidth)}>
                  <TextInput label="Labels" value={form.labelsText} onChange={(labelsText) => setForm({ ...form, labelsText })} width="100%" />
                </section>
              </section>
              <section {...stylex.props(styles.toolbar)}>
                <HStack gap={2}>
                  {editing ? <Button label="Delete" variant="secondary" isDisabled={saving} onClick={() => void deleteTask()} /> : null}
                </HStack>
                <HStack gap={2}>
                  <Button label="Cancel" variant="secondary" isDisabled={saving} onClick={() => setForm(null)} />
                  <Button label={editing ? 'Save' : 'Create'} isLoading={saving} onClick={() => void saveTask()} />
                </HStack>
              </section>
            </VStack>
          </Section>
        </section>
      ) : null}
    </VStack>
  )
}

function TaskCard({
  task,
  statuses,
  onOpen,
  onMove,
  busy,
}: {
  task: SymphonyTask
  statuses: SymphonyStatus[]
  onOpen: () => void
  onMove: (task: SymphonyTask, direction: -1 | 1) => void
  busy: boolean
}) {
  const statusIndex = statuses.findIndex((status) => status.key === task.status_key)
  return (
    <Card padding={3} data-scenery-ui="SymphonyTaskCard">
      <VStack gap={3} as="article">
        <button type="button" {...stylex.props(styles.taskButton)} onClick={onOpen}>
          <VStack gap={2} as="section">
            <section {...stylex.props(styles.cardMeta)}>
              <Badge label={task.identifier} variant="neutral" />
              <Text type="supporting" color="secondary">
                {formatDate(task.updated_at)}
              </Text>
            </section>
            <Text type="body" weight="semibold">
              {task.title}
            </Text>
            {task.description !== '' ? (
              <Text type="supporting" color="secondary">
                {task.description}
              </Text>
            ) : null}
          </VStack>
        </button>
        <section {...stylex.props(styles.labels)}>
          {(task.labels ?? []).map((label) => (
            <Badge key={label} label={label} variant="info" />
          ))}
          {task.priority !== '' ? <Badge label={task.priority} variant="warning" /> : null}
          {task.assignee !== '' ? <Badge label={task.assignee} variant="neutral" /> : null}
          {task.latest_run ? <Badge label={task.latest_run.status} variant="success" /> : null}
        </section>
        <HStack gap={2}>
          <Button label="Back" size="sm" variant="secondary" isDisabled={busy || statusIndex <= 0} onClick={() => onMove(task, -1)} />
          <Button label="Next" size="sm" variant="secondary" isDisabled={busy || statusIndex === -1 || statusIndex >= statuses.length - 1} onClick={() => onMove(task, 1)} />
        </HStack>
      </VStack>
    </Card>
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

function emptyForm(statusKey: string): TaskForm {
  return {
    title: '',
    description: '',
    status_key: statusKey,
    priority: '',
    assignee: '',
    estimate: '',
    branch_name: '',
    url: '',
    source: 'manual',
    labels: [],
    labelsText: '',
  }
}

function formInput(form: TaskForm): SymphonyTaskInput {
  return {
    title: form.title,
    description: form.description,
    status_key: form.status_key,
    priority: form.priority,
    assignee: form.assignee,
    estimate: form.estimate,
    branch_name: form.branch_name,
    url: form.url,
    source: form.source || 'manual',
    labels: form.labelsText.split(',').map((label) => label.trim()).filter(Boolean),
  }
}

function badgeVariant(status: SymphonyStatus) {
  switch (status.color) {
    case 'success':
      return 'success'
    case 'warning':
      return 'warning'
    case 'info':
      return 'info'
    default:
      return status.kind === 'terminal' ? 'neutral' : 'info'
  }
}

function formatDate(value: string) {
  const time = Date.parse(value)
  if (!Number.isFinite(time)) {
    return ''
  }
  return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(time)
}
