import { onBeforeUnmount, ref } from 'vue'
import { api } from '@/api/client'
import type { FileEntry, SearchRequest } from '@/types/files'

export function useFilenameSearch() {
  const request = ref<SearchRequest | null>(null)
  const results = ref<FileEntry[]>([])
  const busy = ref(false), message = ref(''), error = ref('')
  const scanned = ref(0), skipped = ref(0)
  let controller: AbortController | null = null

  function cancel() { controller?.abort() }
  function close() {
    cancel()
    controller = null
    request.value = null
    results.value = []
    busy.value = false
  }
  async function start(options: SearchRequest) {
    cancel()
    const active = new AbortController()
    controller = active
    request.value = { ...options }
    results.value = []
    scanned.value = 0; skipped.value = 0; error.value = ''
    busy.value = true; message.value = 'Searching…'
    try {
      await api.search(options, update => {
        if (controller !== active) return
        if (update.entry) results.value.push(update.entry)
        scanned.value = update.scanned; skipped.value = update.skipped
        if (update.done) message.value = update.truncated
          ? 'Partial results: result, traversal or time limit reached. Refine the search or choose a smaller folder.'
          : 'Search complete'
      }, active.signal)
    } catch (err) {
      if (controller !== active) return
      if (active.signal.aborted) message.value = 'Search cancelled — results are partial'
      else {
        error.value = err instanceof Error ? err.message : String(err)
        message.value = 'Search stopped — results are partial'
      }
    } finally {
      if (controller === active) { busy.value = false; controller = null }
    }
  }
  onBeforeUnmount(cancel)
  return { request, results, busy, message, error, scanned, skipped, start, cancel, close }
}
