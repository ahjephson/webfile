<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'
import { api } from '@/api/client'
import type { FileDetails } from '@/types/files'

const props = defineProps<{ visible: boolean; path: string }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  error: [message: string]
  'size-ready': [value: { path: string; size: number; calculatedAt?: string }]
}>()

const details = ref<FileDetails | null>(null)
const loading = ref(false)
let sizePoll: number | null = null
let loadRequest = 0
let detailsController: AbortController | null = null
let scan = ''

const permissionRows = [
  { label: 'Owner', read: 0o400, write: 0o200, execute: 0o100 },
  { label: 'Group', read: 0o040, write: 0o020, execute: 0o010 },
  { label: 'Other', read: 0o004, write: 0o002, execute: 0o001 },
]

watch([() => props.visible, () => props.path], ([visible], _previous, onCleanup) => {
  loadRequest++
  stopSizePoll()
  if (visible && props.path) {
    details.value = null
    detailsController = new AbortController()
    scan = crypto.randomUUID()
    const active = scan
    onCleanup(() => {
      detailsController?.abort()
      void api.cancelDetails(active).catch(() => undefined)
    })
    void load(false)
  }
}, { immediate: true })

onBeforeUnmount(() => { loadRequest++; stopSizePoll() })

async function load(refresh: boolean) {
  const request = ++loadRequest
  const path = props.path
  const current = () => request === loadRequest && props.visible && props.path === path
  if (!details.value || refresh) loading.value = true
  try {
    const result = await api.details(path, refresh, detailsController?.signal, scan)
    if (!current()) return
    details.value = result
    if (result.is_dir && result.size_status === 'ready') emit('size-ready', { path: result.path, size: result.size, calculatedAt: result.size_calculated_at })
    if (result.is_dir && (result.size_status === 'calculating' || result.size_refreshing)) {
      sizePoll = window.setTimeout(() => void load(false), 500)
    } else if (result.is_dir && result.size_status === 'ready') {
      const scanned = Date.parse(result.size_calculated_at ?? '')
      const remaining = Number.isFinite(scanned) ? scanned + 5 * 60_000 - Date.now() : 5 * 60_000
      sizePoll = window.setTimeout(() => void load(false), Math.max(1000, remaining))
    }
  } catch (err) {
    if (current()) emit('error', err instanceof Error ? err.message : String(err))
  } finally {
    if (current()) loading.value = false
  }
}

function stopSizePoll() {
  if (sizePoll !== null) window.clearTimeout(sizePoll)
  sizePoll = null
}

function checked(bit: number) {
  if (!details.value) return false
  return (Number.parseInt(details.value.mode_octal, 8) & bit) !== 0
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let size = value / 1024
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unit]}`
}
</script>

<template>
  <aside v-if="visible" class="flex h-full w-[min(28rem,42vw)] min-w-80 shrink-0 flex-col border-l border-zinc-800 bg-zinc-900 text-zinc-100 shadow-2xl">
    <header class="flex h-12 shrink-0 items-center justify-between border-b border-zinc-800 px-4">
      <h2 class="font-semibold">Details</h2>
      <Button v-tooltip.bottom="'Close details'" icon="pi pi-times" severity="secondary" text rounded aria-label="Close details" @click="emit('update:visible', false)" />
    </header>
    <div v-if="loading" class="grid min-h-56 flex-1 place-items-center"><ProgressSpinner class="h-9 w-9" /></div>
    <div v-else-if="details" class="min-h-0 flex-1 space-y-6 overflow-auto p-4">
      <div class="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2 text-sm">
        <span class="text-zinc-500">Name</span><span class="break-all">{{ details.name }}</span>
        <span class="text-zinc-500">Path</span><span class="break-all font-mono text-xs">{{ details.path }}</span>
        <span class="text-zinc-500">Type</span><span>{{ details.is_symlink ? 'Symbolic link' : details.is_dir ? 'Folder' : 'File' }}</span>
        <span class="text-zinc-500">Size</span>
        <span v-if="details.size_status === 'calculating'" class="inline-flex items-center gap-2 text-zinc-400"><i class="pi pi-spinner pi-spin" /> Calculating</span>
        <span v-else-if="details.size_status === 'error'" class="webfile-text-error" :title="details.size_error">Unavailable</span>
        <span v-else>{{ formatBytes(details.size) }}</span>
        <span class="text-zinc-500">Modified</span><span>{{ new Date(details.modified).toLocaleString() }}</span>
        <span v-if="details.link_target" class="text-zinc-500">Target</span><span v-if="details.link_target" class="break-all font-mono text-xs">{{ details.link_target }}</span>
      </div>

      <section class="space-y-3">
        <h3 class="font-semibold">Ownership</h3>
        <div class="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2 text-sm">
          <span class="text-zinc-500">Owner</span><span>{{ details.owner }} <span class="text-zinc-500">(UID {{ details.uid }})</span></span>
          <span class="text-zinc-500">Group</span><span>{{ details.group }} <span class="text-zinc-500">(GID {{ details.gid }})</span></span>
        </div>
      </section>

      <section v-if="!details.is_symlink" class="space-y-3">
        <div class="flex items-center justify-between">
          <h3 class="font-semibold">Permissions</h3>
          <span class="font-mono text-sm text-zinc-400">{{ details.mode_octal }}</span>
        </div>
        <div class="grid grid-cols-[1fr_repeat(3,5rem)] items-center gap-y-3 text-sm">
          <span />
          <span class="text-center text-zinc-500">Read</span>
          <span class="text-center text-zinc-500">Write</span>
          <span class="text-center text-zinc-500">Execute</span>
          <template v-for="row in permissionRows" :key="row.label">
            <span>{{ row.label }}</span>
            <span class="text-center" :class="checked(row.read) ? 'webfile-text-success' : 'text-zinc-700'">{{ checked(row.read) ? 'Yes' : '—' }}</span>
            <span class="text-center" :class="checked(row.write) ? 'webfile-text-success' : 'text-zinc-700'">{{ checked(row.write) ? 'Yes' : '—' }}</span>
            <span class="text-center" :class="checked(row.execute) ? 'webfile-text-success' : 'text-zinc-700'">{{ checked(row.execute) ? 'Yes' : '—' }}</span>
          </template>
        </div>
      </section>
    </div>
  </aside>
</template>
