import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { FileOperation } from '@/types/files'

const activeStates = ['queued', 'preparing', 'awaiting_input', 'running', 'cancelling', 'cleaning_up']
const terminalStates = ['completed', 'failed', 'cancelled']

export function useFileOperations(onTerminal: (operation: FileOperation, previous?: FileOperation) => void) {
  const operations = ref<FileOperation[]>([])
  const activeOperations = computed(() => operations.value.filter(operation => activeStates.includes(operation.state)))
  const announcedTerminalStates = new Set<string>()

  async function load() {
    const idsBeforeLoad = new Set(operations.value.map(operation => operation.id))
    try {
      const loaded = (await api.operations()).operations
      const current = new Map(operations.value.map(operation => [operation.id, operation]))
      const loadedIds = new Set(loaded.map(operation => operation.id))
      operations.value = [
        ...operations.value.filter(operation => !idsBeforeLoad.has(operation.id) && !loadedIds.has(operation.id)),
        ...loaded.map(operation => {
          const existing = current.get(operation.id)
          return existing && Date.parse(existing.updated_at) > Date.parse(operation.updated_at) ? existing : operation
        }),
      ].slice(0, 50)
    } catch {
      // Operation history is supplementary; realtime updates can still populate it.
    }
  }

  function update(operation: FileOperation) {
    const previous = operations.value.find(item => item.id === operation.id)
    if (previous && Date.parse(previous.updated_at) > Date.parse(operation.updated_at)) return
    if (previous && terminalStates.includes(previous.state) && !terminalStates.includes(operation.state)) return
    operations.value = [operation, ...operations.value.filter(item => item.id !== operation.id)].slice(0, 50)
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
    operations.value = operations.value.filter(operation => activeStates.includes(operation.state))
    return result.removed
  }

  async function removeHistory(id: string) {
    await api.removeOperationHistory(id)
    operations.value = operations.value.filter(operation => operation.id !== id)
  }

  return { operations, activeOperations, load, update, start, startExtractions, reorder, clearHistory, removeHistory }
}
