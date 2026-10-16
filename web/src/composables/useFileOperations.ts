import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { FileOperation } from '@/types/files'

const activeStates = ['queued', 'preparing', 'awaiting_input', 'running', 'cancelling', 'cleaning_up']
const terminalStates = ['completed', 'failed', 'cancelled']

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

export function useFileOperations(onTerminal: (operation: FileOperation, previous?: FileOperation) => void) {
  const operations = ref<FileOperation[]>([])
  const activeOperations = computed(() => operations.value.filter(operation => activeStates.includes(operation.state)))
  const announcedTerminalStates = new Set<string>()
  let historyRevision = 0

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
          return existing && updatedAfter(existing, operation) ? existing : operation
        }),
      ])
    } catch {
      // Operation history is supplementary; realtime updates can still populate it.
    }
  }

  function update(operation: FileOperation) {
    const previous = operations.value.find(item => item.id === operation.id)
    if (previous && updatedAfter(previous, operation)) return
    if (previous && terminalStates.includes(previous.state) && !terminalStates.includes(operation.state)) {
      if (previous.kind !== 'upload' || operation.kind !== 'upload' || !activeStates.includes(operation.state) || !updatedAfter(operation, previous)) return
      for (const state of terminalStates) announcedTerminalStates.delete(`${operation.id}:${state}`)
    }
    operations.value = retainOperations([operation, ...operations.value.filter(item => item.id !== operation.id)])
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
