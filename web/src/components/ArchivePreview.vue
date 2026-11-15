<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import Button from 'primevue/button'
import { api, ApiError, type ArchivePreviewEntry } from '@/api/client'
import ArchivePasswordDialog from '@/components/ArchivePasswordDialog.vue'

const props = defineProps<{ path: string }>()
const entries = ref<ArchivePreviewEntry[]>([])
const loading = ref(false)
const error = ref('')
const passwordVisible = ref(false)
const passwordError = ref('')
const folder = ref('')
let controller: AbortController | null = null
const name = computed(() => props.path.split('/').pop() || props.path)
const rows = computed(() => {
  const result = new Map<string, ArchivePreviewEntry>()
  const prefix = folder.value ? folder.value + '/' : ''
  for (const entry of entries.value) {
    const normalized = entry.path.replaceAll('\\', '/').replace(/\/$/, '')
    if (!normalized.startsWith(prefix)) continue
    const remaining = normalized.slice(prefix.length)
    if (!remaining) continue
    const [child, ...rest] = remaining.split('/')
    if (!child || child === '.' || child === '..') continue
    const path = prefix + child
    const row = { ...entry, path, is_dir: rest.length > 0 || entry.is_dir }
    if (!result.has(path) || row.is_dir) result.set(path, row)
  }
  return [...result.values()].sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.path.localeCompare(b.path, undefined, { numeric: true }))
})

async function load(password = '') {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  passwordError.value = ''
  try {
    const result = await api.previewArchive(props.path, password, current.signal)
    if (controller !== current) return
    entries.value = result.entries ?? []
    passwordVisible.value = false
  } catch (err) {
    if (current.signal.aborted || controller !== current) return
    if (err instanceof ApiError && ['archive_password_required', 'archive_password_incorrect'].includes(err.code ?? '')) {
      passwordVisible.value = true
      if (err.code === 'archive_password_incorrect') passwordError.value = err.message
    } else {
      passwordVisible.value = false
      error.value = err instanceof Error ? err.message : 'Could not open the archive. Please try again.'
    }
  } finally {
    password = ''
    if (controller === current) loading.value = false
  }
}

function cancelPassword() {
  passwordVisible.value = false
  error.value = 'Archive preview cancelled. Retry to enter the password.'
}

watch(() => props.path, () => {
  entries.value = []
  folder.value = ''
  passwordVisible.value = false
  void load()
}, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); controller = null })
</script>

<template>
  <div class="flex h-full w-full min-h-0 flex-col self-stretch text-zinc-200" aria-label="Archive contents" :aria-busy="loading">
    <div v-if="loading" class="m-auto flex items-center gap-2" role="status"><i class="pi pi-spinner pi-spin" aria-hidden="true" />Opening archive…</div>
    <div v-else-if="error" class="m-auto max-w-lg p-6 text-center" role="alert">
      <i class="pi pi-exclamation-triangle mb-4 text-4xl webfile-accent-danger" aria-hidden="true" />
      <h2 class="mb-2 font-semibold">Could not open archive</h2>
      <p class="mb-4 text-sm">{{ error }}</p>
      <Button label="Retry" severity="secondary" @click="load()" />
    </div>
    <template v-else-if="!passwordVisible">
      <div class="flex items-center gap-3 border-b border-zinc-800 p-3">
        <Button v-if="folder" icon="pi pi-arrow-up" aria-label="Up within archive" severity="secondary" text @click="folder = folder.split('/').slice(0, -1).join('/')" />
        <span class="min-w-0 truncate text-sm">{{ folder || 'Archive contents' }}</span>
        <span class="ml-auto shrink-0 text-xs text-zinc-400">{{ rows.length }} items</span>
      </div>
      <div class="min-h-0 flex-1 overflow-auto p-3">
        <p class="mb-3 text-xs text-zinc-400">Browse folders to see their contents. Extract the archive to open individual files.</p>
        <p v-if="!rows.length" class="p-6 text-center text-zinc-400">This folder is empty.</p>
        <ul v-else class="divide-y divide-zinc-800" aria-label="Archive entries">
          <li v-for="entry in rows" :key="entry.path" class="flex min-h-11 items-center gap-3 py-2">
            <i :class="entry.is_dir ? 'pi pi-folder webfile-accent-warning' : 'pi pi-file text-zinc-400'" aria-hidden="true" />
            <button v-if="entry.is_dir" class="min-w-0 flex-1 truncate text-left hover:underline" @click="folder = entry.path">{{ entry.path.split('/').pop() }}</button>
            <span v-else class="min-w-0 flex-1 truncate">{{ entry.path.split('/').pop() }}</span>
            <i v-if="entry.encrypted" class="pi pi-lock text-xs text-zinc-400" aria-label="Encrypted" />
            <span v-if="!entry.is_dir" class="shrink-0 text-xs text-zinc-400">{{ entry.size.toLocaleString() }} bytes</span>
          </li>
        </ul>
      </div>
    </template>
    <ArchivePasswordDialog v-model:visible="passwordVisible" :archive-name="name" purpose="preview" :busy="loading" :error="passwordError" @submit="load" @cancel="cancelPassword" />
  </div>
</template>
