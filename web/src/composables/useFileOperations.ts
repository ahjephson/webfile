import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { FileOperation } from '@/types/files'

const activeStates = ['queued', 'preparing', 'awaiting_input', 'running', 'cancelling', 'cleaning_up']

export function useFileOperations(onTerminal: (operation: FileOperation, previous?: FileOperation) => void) {
  const operations = ref<FileOperation[]>([])
  const activeOperations = computed(() => operations.value.filter(operation => activeStates.includes(operation.state)))

  async function load() {
    try {
      operations.value = (await api.operations()).operations
    } catch {
      // Operation history is supplementary; realtime updates can still populate it.
    }
  }

  function update(operation: FileOperation) {
    const previous = operations.value.find(item => item.id === operation.id)
    if (previous && Date.parse(previous.updated_at) > Date.parse(operation.updated_at)) return
    operations.value = [operation, ...operations.value.filter(item => item.id !== operation.id)].slice(0, 50)
    if (previous?.state !== operation.state && ['completed', 'failed', 'cancelled'].includes(operation.state)) {
      onTerminal(operation, previous)
    }
  }

  async function start(kind: 'copy' | 'move' | 'trash' | 'extract', sources: string[], destination?: string) {
    const operation = await api.startOperation(kind, sources, destination)
    update(operation)
    return operation
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

  return { operations, activeOperations, load, update, start, clearHistory, removeHistory }
}
