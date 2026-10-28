import { computed, inject, onBeforeUnmount, provide, ref, watch, type InjectionKey } from 'vue'
import { api } from '@/api/client'
import type { FileOperation } from '@/types/files'

const activeStates = ['queued', 'preparing', 'awaiting_input', 'running', 'cancelling', 'cleaning_up']
const terminalStates = ['completed', 'failed', 'cancelled', 'interrupted']

function retainOperations(operations: FileOperation[]): FileOperation[] {
  let history = 0
  return operations.filter(operation => activeStates.includes(operation.state) || history++ < 50)
}

function updatedAfter(left: FileOperation, right: FileOperation): boolean {
  const milliseconds = Date.parse(left.updated_at) - Date.parse(right.updated_at)
  if (milliseconds !== 0) return milliseconds > 0
  // Go timestamps can distinguish events within the same JavaScript millisecond.
  const fraction = (value: string) => (value.match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/)?.[1] ?? '').padEnd(9, '0')
  return fraction(left.updated_at) > fraction(right.updated_at)
}

type TerminalHandler = (operation: FileOperation, previous?: FileOperation) => void
const operationsKey: InjectionKey<{ store: ReturnType<typeof createFileOperations>; listeners: Set<TerminalHandler> }> = Symbol('workspace-operations')

export function provideFileOperations() {
  const listeners = new Set<TerminalHandler>()
  const store = createFileOperations((operation, previous) => listeners.forEach(listener => listener(operation, previous)))
  provide(operationsKey, { store, listeners })
}

export function useFileOperations(onTerminal: TerminalHandler) {
  const shared = inject(operationsKey, null)
  if (!shared) return createFileOperations(onTerminal)
  shared.listeners.add(onTerminal)
  onBeforeUnmount(() => shared.listeners.delete(onTerminal))
  return shared.store
}

function createFileOperations(onTerminal: TerminalHandler) {
  const operations = ref<FileOperation[]>([])
  const activeOperations = computed(() => operations.value.filter(operation => activeStates.includes(operation.state)))
  const announcedTerminalStates = new Set<string>()
  let historyRevision = 0
  let reconciliationTimer: ReturnType<typeof setInterval> | undefined
  let reconciling = false

  // WebSocket delivery is best effort. Reconcile while work is active so a
  // dropped terminal event cannot leave any kind of task pending indefinitely.
  watch(() => activeOperations.value.length > 0, active => {
    if (reconciliationTimer !== undefined) clearInterval(reconciliationTimer)
    reconciliationTimer = active ? setInterval(async () => {
      if (reconciling) return
      reconciling = true
      try { await load() } finally { reconciling = false }
    }, 5000) : undefined
  })
  onBeforeUnmount(() => {
    if (reconciliationTimer !== undefined) clearInterval(reconciliationTimer)
  })

  async function load() {
    const revision = historyRevision
    const idsBeforeLoad = new Set(operations.value.map(operation => operation.id))
    try {
      const loaded = (await api.operations()).operations
      if (revision !== historyRevision) return
      const current = new Map(operations.value.map(operation => [operation.id, operation]))
      const loadedIds = new Set(loaded.map(operation => operation.id))
      operations.value = retainOperations([
        ...operations.value.filter(operation => !idsBeforeLoad.has(operation.id) && !loadedIds.has(operation.id)),
        ...loaded.map(operation => {
          const existing = current.get(operation.id)
          if (existing && !acceptUpdate(operation, existing)) return existing
          resetAnnouncements(operation, existing)
          return operation
        }),
      ])
      for (const operation of operations.value) {
        const previous = current.get(operation.id)
        if (previous && activeStates.includes(previous.state)) announceTerminal(operation, previous)
      }
    } catch {
      // Operation history is supplementary; realtime updates can still populate it.
    }
  }

  function update(operation: FileOperation) {
    const previous = operations.value.find(item => item.id === operation.id)
    if (previous && !acceptUpdate(operation, previous)) return
    resetAnnouncements(operation, previous)
    operations.value = retainOperations([operation, ...operations.value.filter(item => item.id !== operation.id)])
    announceTerminal(operation, previous)
  }

  function acceptUpdate(operation: FileOperation, previous: FileOperation) {
    if (updatedAfter(previous, operation)) return false
    if (terminalStates.includes(previous.state) && !terminalStates.includes(operation.state)) {
      return previous.kind === 'upload' && operation.kind === 'upload' && activeStates.includes(operation.state) && updatedAfter(operation, previous)
    }
    return true
  }

  function resetAnnouncements(operation: FileOperation, previous?: FileOperation) {
    if (previous && terminalStates.includes(previous.state) && activeStates.includes(operation.state)) {
      for (const state of terminalStates) announcedTerminalStates.delete(`${operation.id}:${state}`)
    }
  }

  function announceTerminal(operation: FileOperation, previous?: FileOperation) {
    if (previous?.state === operation.state || !terminalStates.includes(operation.state)) return
    const terminalKey = `${operation.id}:${operation.state}`
    if (announcedTerminalStates.has(terminalKey)) return
    announcedTerminalStates.add(terminalKey)
    onTerminal(operation, previous)
  }

  async function start(kind: 'copy' | 'move' | 'delete' | 'extract' | 'archive', sources: string[], destination?: string, copyChunkMiB?: number) {
    const operation = await api.startOperation(kind, sources, destination, copyChunkMiB)
    update(operation)
    return operation
  }

  async function startExtractions(sources: string[], destination: string, layout: 'separate_directories' | 'here', existingFiles: 'skip' | 'overwrite') {
    const result = await api.startExtractions(sources, destination, layout, existingFiles)
    for (const operation of result.operations) update(operation)
    return result.operations
  }

  async function reorder(id: string, direction: 'up' | 'down') {
    await api.reorderOperation(id, direction)
    await load()
  }

  async function clearHistory() {
    const result = await api.clearOperationHistory()
    historyRevision++
    operations.value = operations.value.filter(operation => activeStates.includes(operation.state))
    return result.removed
  }

  async function removeHistory(id: string) {
    await api.removeOperationHistory(id)
    historyRevision++
    operations.value = operations.value.filter(operation => operation.id !== id)
  }

  return { operations, activeOperations, load, update, start, startExtractions, reorder, clearHistory, removeHistory }
}
