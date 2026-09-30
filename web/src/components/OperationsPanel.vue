<script setup lang="ts">
import { computed } from 'vue'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import type { FileOperation } from '@/types/files'

const props = defineProps<{
  visible: boolean
  operations: FileOperation[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  cancel: [id: string]
  'clear-history': []
  'remove-history': [operation: FileOperation]
}>()

const ordered = computed(() => props.operations
  .filter(operation => !['preparing', 'awaiting_input'].includes(operation.state))
  .sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at)))
const activeTasks = computed(() => ordered.value.filter(active))
const history = computed(() => ordered.value.filter(operation => !active(operation)))

function percent(operation: FileOperation): number | null {
  if (operation.bytes_total > 0) return Math.max(0, Math.min(100, Math.round(operation.bytes_done / operation.bytes_total * 100)))
  if (operation.items_total > 0) return Math.max(0, Math.min(100, Math.round(operation.items_done / operation.items_total * 100)))
  return null
}

function active(operation: FileOperation) {
  return ['queued', 'running', 'cancelling', 'cleaning_up'].includes(operation.state)
}

function label(operation: FileOperation) {
  const noun = operation.sources?.length === 1 ? operation.sources[0].split('/').pop() : `${operation.sources?.length ?? 0} items`
  const kind = operation.kind === 'trash' ? 'Delete' : `${operation.kind[0]?.toUpperCase() ?? ''}${operation.kind.slice(1)}`
  return `${kind} ${noun ?? ''}`.trim()
}

function stateLabel(operation: FileOperation) {
  return operation.state.replaceAll('_', ' ')
}

function phaseLabel(operation: FileOperation) {
  return operation.phase?.replaceAll('_', ' ') ?? stateLabel(operation)
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes / 1024
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[index]}`
}

function formatDate(value?: string) {
  if (!value) return ''
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
</script>

<template>
  <Drawer :visible="visible" position="right" header="Tasks" :style="{ width: 'min(32rem, 94vw)' }" @update:visible="emit('update:visible', $event)">
    <div v-if="ordered.length === 0" class="py-12 text-center text-sm text-zinc-500">No tasks yet.</div>
    <div v-else class="space-y-5">
      <section v-if="activeTasks.length" aria-labelledby="active-tasks-heading">
        <h3 id="active-tasks-heading" class="mb-2 text-xs font-semibold uppercase tracking-wide text-zinc-500">Active</h3>
        <div class="space-y-3">
          <article v-for="operation in activeTasks" :key="operation.id" class="rounded-lg border border-zinc-800 bg-zinc-900/60 p-3">
            <div class="flex items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium">{{ label(operation) }}</div>
                <div class="mt-1 truncate text-xs capitalize text-zinc-500">{{ operation.message || phaseLabel(operation) }}</div>
              </div>
              <span class="rounded bg-sky-950 px-2 py-0.5 text-xs capitalize text-sky-300">{{ stateLabel(operation) }}</span>
            </div>
            <div class="mt-3">
              <div v-if="percent(operation) !== null && operation.state === 'running'" class="h-2 overflow-hidden rounded bg-zinc-800" role="progressbar" :aria-label="`${label(operation)} progress`" :aria-valuenow="percent(operation) ?? undefined" aria-valuemin="0" aria-valuemax="100">
                <div class="h-full bg-sky-600 transition-[width]" :style="{ width: `${percent(operation)}%` }" />
              </div>
              <div v-else class="h-2 overflow-hidden rounded bg-zinc-800" role="progressbar" :aria-label="`${label(operation)} progress`">
                <div class="webfile-indeterminate h-full w-1/3 bg-sky-600" />
              </div>
              <div class="mt-2 flex items-center justify-between gap-3 text-xs text-zinc-500">
                <span v-if="operation.bytes_total > 0">{{ formatBytes(operation.bytes_done) }} / {{ formatBytes(operation.bytes_total) }}</span>
                <span v-else-if="operation.items_total > 0">{{ operation.items_done }} / {{ operation.items_total }} items</span>
                <span v-else class="capitalize">{{ phaseLabel(operation) }}</span>
                <Button v-if="operation.cancellable" label="Cancel" size="small" severity="secondary" text @click="emit('cancel', operation.id)" />
              </div>
            </div>
            <details v-if="operation.cleanup_policy || operation.destination" class="mt-2 text-xs text-zinc-500">
              <summary class="cursor-pointer select-none">Details</summary>
              <dl class="mt-2 space-y-1">
                <div v-if="operation.destination"><dt class="inline font-medium text-zinc-400">Destination:</dt> <dd class="inline break-all">{{ operation.destination }}</dd></div>
                <div v-if="operation.cleanup_policy"><dt class="inline font-medium text-zinc-400">On cancel:</dt> <dd class="inline">{{ operation.cleanup_policy }}</dd></div>
              </dl>
            </details>
          </article>
        </div>
      </section>

      <section v-if="history.length" aria-labelledby="task-history-heading">
        <div class="mb-2 flex min-h-8 items-center justify-between gap-2">
          <h3 id="task-history-heading" class="text-xs font-semibold uppercase tracking-wide text-zinc-500">History</h3>
          <Button v-tooltip.left="'Clear history'" icon="pi pi-trash" size="small" severity="secondary" text aria-label="Clear task history" @click="emit('clear-history')" />
        </div>
        <div class="space-y-3">
          <article v-for="operation in history" :key="operation.id" class="rounded-lg border border-zinc-800 bg-zinc-900/60 p-3">
            <div class="flex items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium">{{ label(operation) }}</div>
                <div class="mt-1 text-xs text-zinc-500">{{ formatDate(operation.finished_at || operation.updated_at) }}</div>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <span class="rounded px-2 py-0.5 text-xs capitalize" :class="{
                  'bg-emerald-950 text-emerald-300': operation.state === 'completed',
                  'bg-red-950 text-red-300': operation.state === 'failed',
                  'bg-amber-950 text-amber-300': operation.state === 'interrupted',
                  'bg-zinc-800 text-zinc-400': operation.state === 'cancelled',
                }">{{ stateLabel(operation) }}</span>
                <Button v-tooltip.left="'Remove from history'" icon="pi pi-times" size="small" severity="secondary" text :aria-label="`Remove ${label(operation)} from history`" @click="emit('remove-history', operation)" />
              </div>
            </div>
            <p v-if="operation.error" class="mt-2 text-xs text-red-300">{{ operation.error }}</p>
            <p v-if="operation.cleanup_error" class="mt-2 text-xs text-red-300">Cleanup: {{ operation.cleanup_error }}</p>
            <details v-if="operation.message || operation.cleanup_policy || operation.destination" class="mt-2 text-xs text-zinc-500">
              <summary class="cursor-pointer select-none">Details</summary>
              <dl class="mt-2 space-y-1">
                <div v-if="operation.message"><dt class="inline font-medium text-zinc-400">Result:</dt> <dd class="inline">{{ operation.message }}</dd></div>
                <div v-if="operation.destination"><dt class="inline font-medium text-zinc-400">Destination:</dt> <dd class="inline break-all">{{ operation.destination }}</dd></div>
                <div v-if="operation.cleanup_policy"><dt class="inline font-medium text-zinc-400">Cancellation:</dt> <dd class="inline">{{ operation.cleanup_policy }}</dd></div>
              </dl>
            </details>
          </article>
        </div>
      </section>
    </div>
  </Drawer>
</template>
