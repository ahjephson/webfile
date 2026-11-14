<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import Button from 'primevue/button'
import Drawer from 'primevue/drawer'
import type { FileOperation, VideoThumbnailJob } from '@/types/files'

const props = defineProps<{
  visible: boolean
  operations: FileOperation[]
  thumbnailJobs: VideoThumbnailJob[]
}>()

const emit = defineEmits<{
  undo: [operation: FileOperation]
  pause: [operation: FileOperation]
  'update:visible': [value: boolean]
  cancel: [operation: FileOperation]
  reorder: [id: string, direction: 'up' | 'down']
  'clear-history': []
  'remove-history': [operation: FileOperation]
  download: [operation: FileOperation]
  resolve: [operation: FileOperation]
  password: [operation: FileOperation]
  'cancel-thumbnail': [id: string]
}>()

const ordered = computed(() => [...props.operations].sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at)))
const activeTasks = computed(() => props.operations.filter(active).sort((a, b) => {
  if (a.state === 'queued' && b.state === 'queued') return (a.queue_position ?? Number.MAX_SAFE_INTEGER) - (b.queue_position ?? Number.MAX_SAFE_INTEGER)
  if (a.state === 'queued') return 1
  if (b.state === 'queued') return -1
  return Date.parse(a.started_at) - Date.parse(b.started_at)
}))
const history = computed(() => ordered.value.filter(operation => !active(operation)))
const maxQueuePosition = computed(() => Math.max(0, ...activeTasks.value.map(operation => operation.queue_position ?? 0)))
const processingThumbnailStates = ['probing', 'generating', 'publishing']
const visibleThumbnailJobs = computed(() => props.thumbnailJobs
  .filter(job => !['settling', 'ready', 'cancelled'].includes(job.state))
  .sort((a, b) => {
    const stateDifference = thumbnailStateRank(a) - thumbnailStateRank(b)
    if (stateDifference !== 0) return stateDifference
    if (a.state === 'queued' && b.state === 'queued') {
      const positionDifference = (a.queue_position ?? Number.MAX_SAFE_INTEGER) - (b.queue_position ?? Number.MAX_SAFE_INTEGER)
      if (positionDifference !== 0) return positionDifference
    }
    const aTime = Date.parse(a.started_at ?? a.updated_at)
    const bTime = Date.parse(b.started_at ?? b.updated_at)
    return a.state === 'failed' ? bTime - aTime : aTime - bTime
  }))
const displayedThumbnailJobs = computed(() => visibleThumbnailJobs.value.slice(0, 10))
const remainingThumbnailJobs = computed(() => Math.max(0, visibleThumbnailJobs.value.length - displayedThumbnailJobs.value.length))
const thumbnailSummary = computed(() => {
  const generating = visibleThumbnailJobs.value.filter(job => processingThumbnailStates.includes(job.state)).length
  const queued = visibleThumbnailJobs.value.filter(job => job.state === 'queued').length
  return [generating ? `${generating} generating` : '', queued ? `${queued} queued` : ''].filter(Boolean).join(', ')
})
const thumbnailsExpanded = ref(true)
const activeExpanded = ref(true)
const historyExpanded = ref(true)
const undoClock = ref(Date.now())
const undoClockTimer = window.setInterval(() => { undoClock.value = Date.now() }, 1000)
onBeforeUnmount(() => window.clearInterval(undoClockTimer))

function canUndo(operation: FileOperation) {
  return operation.undo_available && operation.state === 'completed' && !!operation.undo_expires_at && Date.parse(operation.undo_expires_at) > undoClock.value
}
const taskPanelContent = ref<HTMLElement | null>(null)
let indeterminateFrame: number | null = null

watch(() => props.visible, visible => {
  if (visible) startIndeterminateProgress()
  else stopIndeterminateProgress()
}, { immediate: true, flush: 'post' })

onBeforeUnmount(stopIndeterminateProgress)

function thumbnailStateRank(job: VideoThumbnailJob): number {
  if (processingThumbnailStates.includes(job.state)) return 0
  if (job.state === 'queued') return 1
  if (job.state === 'failed') return 2
  return 3
}

function startIndeterminateProgress() {
  if (indeterminateFrame !== null) return
  indeterminateFrame = window.requestAnimationFrame(updateIndeterminateProgress)
}

function stopIndeterminateProgress() {
  if (indeterminateFrame !== null) window.cancelAnimationFrame(indeterminateFrame)
  indeterminateFrame = null
}

function updateIndeterminateProgress(timestamp: number) {
  const duration = 1100
  const linearProgress = timestamp % duration / duration
  const easedProgress = linearProgress < 0.5
    ? 2 * linearProgress * linearProgress
    : 1 - Math.pow(-2 * linearProgress + 2, 2) / 2
  taskPanelContent.value?.style.setProperty('--webfile-indeterminate-offset', `${-120 + 440 * easedProgress}%`)
  indeterminateFrame = window.requestAnimationFrame(updateIndeterminateProgress)
}

function percent(operation: FileOperation): number | null {
  if (operation.progress_total <= 0) return null
  return Math.max(0, Math.min(100, operation.progress_done / operation.progress_total * 100))
}

function transferNotice(operation: FileOperation): string {
  if (operation.paused) return ''
  if (operation.state !== 'running' || !['copy', 'move'].includes(operation.kind)) return ''
  if (!['copying', 'flushing', 'publishing', 'removing_source'].includes(operation.phase ?? '')) return ''
  if (operation.bytes_total > 0 && operation.bytes_done >= operation.bytes_total) return 'Finishing…'
  if (operation.bytes_per_second === 0) return 'Waiting for disk…'
  return ''
}

function active(operation: FileOperation) {
  return ['queued', 'preparing', 'awaiting_input', 'running', 'cancelling', 'cleaning_up'].includes(operation.state)
}

function label(operation: FileOperation) {
  const noun = operation.sources?.length === 1 ? operation.sources[0].split('/').pop() : `${operation.sources?.length ?? 0} items`
  const kind = operation.kind.replaceAll('_', ' ')
  const operationKind = `${kind[0]?.toUpperCase() ?? ''}${kind.slice(1)}`
  return `${operationKind} ${noun ?? ''}`.trim()
}

function stateLabel(operation: FileOperation) {
  if (operation.paused) return 'paused'
  if (operation.kind === 'archive' && (operation.phase === 'downloaded' || operation.phase === 'unavailable')) return operation.phase
  return operation.state.replaceAll('_', ' ')
}

function phaseLabel(operation: FileOperation) {
  if (operation.paused) return 'Paused'
  const value = operation.phase?.replaceAll('_', ' ') ?? stateLabel(operation)
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`
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

function formatProgress(operation: FileOperation): string {
  if (operation.progress_unit === 'percent') return `${Math.round(operation.progress_done)}%`
  if (operation.progress_unit === 'bytes') {
    const progress = `${formatBytes(operation.progress_done)} / ${formatBytes(operation.progress_total)}`
    if (operation.state === 'running' && !operation.paused && (operation.kind === 'upload' || ['copying', 'flushing', 'publishing', 'removing_source'].includes(operation.phase ?? '')) && operation.bytes_per_second != null && Number.isFinite(operation.bytes_per_second) && operation.bytes_per_second >= 0) {
      return `${progress} · ${formatBytes(operation.bytes_per_second)}/s`
    }
    return progress
  }
  if (operation.progress_unit === 'items') {
    const progress = operation.progress_total > 0 ? `${operation.progress_done} / ${operation.progress_total} items` : `${operation.progress_done} items`
    const rate = operation.items_per_second
    if (operation.state === 'running' && !operation.paused && rate != null && Number.isFinite(rate) && rate >= 0) {
      const speed = rate > 0 && rate < 0.01 ? '<0.01' : rate.toLocaleString(undefined, { maximumFractionDigits: rate < 1 ? 2 : 1 })
      return `${progress} · ${speed} items/s`
    }
    return progress
  }
  if (operation.kind === 'copy' || operation.kind === 'move') return ''
  return phaseLabel(operation)
}

function etaLabel(operation: FileOperation): string {
  const value = operation.eta_seconds
  if (operation.state !== 'running' || operation.paused || operation.progress_total <= 0 || operation.progress_done >= operation.progress_total || value == null || !Number.isFinite(value) || value <= 0) return ''
  const seconds = Math.ceil(value)
  if (seconds < 60) return `About ${seconds}s remaining`
  const minutes = Math.ceil(seconds / 60)
  if (minutes < 60) return `About ${minutes}m remaining`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `About ${hours}h ${minutes % 60}m remaining`
  return `About ${Math.floor(hours / 24)}d ${hours % 24}h remaining`
}

function formatDate(value?: string) {
  if (!value) return ''
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
</script>

<template>
  <Drawer :visible="visible" position="right" header="Tasks" :style="{ width: 'min(32rem, 94vw)' }" @update:visible="emit('update:visible', $event)">
    <div ref="taskPanelContent">
      <div v-if="ordered.length === 0 && visibleThumbnailJobs.length === 0" class="py-12 text-center text-sm text-zinc-500">No tasks yet.</div>
      <div v-else class="space-y-5">
      <section v-if="visibleThumbnailJobs.length" aria-labelledby="thumbnail-tasks-heading">
        <div class="mb-2 flex items-center justify-between gap-3">
          <h3 id="thumbnail-tasks-heading">
            <button
              class="webfile-text-queued flex items-center gap-2 rounded px-1 py-1 text-xs font-semibold uppercase tracking-wide hover:bg-zinc-800"
              :aria-expanded="thumbnailsExpanded"
              aria-controls="thumbnail-tasks-content"
              :aria-label="`${thumbnailsExpanded ? 'Collapse' : 'Expand'} Video thumbnails (${visibleThumbnailJobs.length})`"
              @click="thumbnailsExpanded = !thumbnailsExpanded"
            >
              <i class="pi text-[0.65rem]" :class="thumbnailsExpanded ? 'pi-chevron-down' : 'pi-chevron-right'" />
              <span>Video thumbnails</span>
              <span class="webfile-status-queued rounded-full px-1.5 py-0.5 text-[0.65rem] tabular-nums">{{ visibleThumbnailJobs.length }}</span>
            </button>
          </h3>
          <span class="text-xs text-zinc-500">{{ thumbnailSummary }}</span>
        </div>
        <div v-if="thumbnailsExpanded" id="thumbnail-tasks-content" class="space-y-2">
          <article
            v-for="job in displayedThumbnailJobs" :key="job.id" class="rounded-lg border p-3"
            :class="processingThumbnailStates.includes(job.state) ? 'webfile-task-processing' : 'webfile-task-queued'"
            :data-thumbnail-job="job.id"
            :data-thumbnail-processing="processingThumbnailStates.includes(job.state) ? 'true' : undefined"
          >
            <div class="flex items-start gap-3">
              <span
                v-if="job.state !== 'failed'"
                class="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center" :class="processingThumbnailStates.includes(job.state) ? 'webfile-text-info' : 'webfile-text-queued'"
                role="status"
                :aria-label="`${job.name} thumbnail task ${job.state}`"
                data-task-spinner
              >
                <i class="pi pi-spinner text-xs motion-safe:animate-spin" aria-hidden="true" />
              </span>
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium">{{ job.name }}</div>
                <div class="mt-1 truncate text-xs text-zinc-500">
                  <span class="capitalize">{{ job.state }}</span><span v-if="job.queue_position"> · Queue position {{ job.queue_position }}</span>
                </div>
              </div>
              <Button v-if="job.state === 'failed'" label="Dismiss" size="small" severity="secondary" text @click="emit('cancel-thumbnail', job.id)" />
              <Button v-else-if="job.cancellable" label="Cancel" size="small" severity="secondary" text @click="emit('cancel-thumbnail', job.id)" />
            </div>
            <p v-if="job.error" class="webfile-text-error mt-2 text-xs">{{ job.error }}</p>
          </article>
          <p v-if="remainingThumbnailJobs" class="rounded-lg border border-dashed border-violet-900/70 px-3 py-2 text-center text-xs text-zinc-500">and {{ remainingThumbnailJobs }} more</p>
        </div>
      </section>

      <section v-if="activeTasks.length" aria-labelledby="active-tasks-heading">
        <h3 id="active-tasks-heading" class="mb-2">
          <button
            class="flex items-center gap-2 rounded px-1 py-1 text-xs font-semibold uppercase tracking-wide text-zinc-500 hover:bg-zinc-800"
            :aria-expanded="activeExpanded"
            aria-controls="active-tasks-content"
            :aria-label="`${activeExpanded ? 'Collapse' : 'Expand'} Active (${activeTasks.length})`"
            @click="activeExpanded = !activeExpanded"
          >
            <i class="pi text-[0.65rem]" :class="activeExpanded ? 'pi-chevron-down' : 'pi-chevron-right'" />
            <span>Active</span>
            <span class="rounded-full bg-zinc-800 px-1.5 py-0.5 text-[0.65rem] tabular-nums text-zinc-300">{{ activeTasks.length }}</span>
          </button>
        </h3>
        <div v-if="activeExpanded" id="active-tasks-content" class="space-y-3">
          <article v-for="operation in activeTasks" :key="operation.id" class="rounded-lg border p-3" :class="operation.state === 'running' && !operation.paused ? 'webfile-task-processing' : 'border-zinc-800 bg-zinc-900/60'" :data-task-state="operation.paused ? 'paused' : operation.state">
            <div class="flex items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium">{{ label(operation) }}</div>
                <div class="mt-1 truncate text-xs capitalize text-zinc-500">{{ operation.message || phaseLabel(operation) }}</div>
              </div>
              <span class="webfile-status-processing rounded px-2 py-0.5 text-xs capitalize">{{ stateLabel(operation) }}</span>
              <Button v-if="operation.input && operation.state === 'awaiting_input'" label="Review conflict" size="small" severity="warn" @click="emit('resolve', operation)" />
            </div>
            <div class="mt-3">
              <div v-if="operation.state === 'queued'" class="flex items-center justify-between gap-3 text-xs text-zinc-500">
                <span>{{ operation.kind === 'upload' ? 'Waiting to upload' : `Queue position ${operation.queue_position}` }}</span>
                <div class="flex items-center gap-1">
                  <Button v-if="operation.cancellable" :label="operation.paused ? 'Resume' : 'Pause'" :icon="operation.paused ? 'pi pi-play' : 'pi pi-pause'" :aria-label="`${operation.paused ? 'Resume' : 'Pause'} ${label(operation)}`" size="small" severity="secondary" text @click="emit('pause', operation)" />
                  <Button v-if="operation.kind !== 'upload'" v-tooltip.top="'Move up'" icon="pi pi-arrow-up" size="small" severity="secondary" text :disabled="(operation.queue_position ?? 0) <= 1" :aria-label="`Move ${label(operation)} up`" @click="emit('reorder', operation.id, 'up')" />
                  <Button v-if="operation.kind !== 'upload'" v-tooltip.top="'Move down'" icon="pi pi-arrow-down" size="small" severity="secondary" text :disabled="(operation.queue_position ?? 0) >= maxQueuePosition" :aria-label="`Move ${label(operation)} down`" @click="emit('reorder', operation.id, 'down')" />
                  <Button label="Cancel" size="small" severity="secondary" text @click="emit('cancel', operation)" />
                </div>
              </div>
              <div v-else-if="percent(operation) !== null && (operation.state === 'running' || operation.kind === 'delete')" class="h-2 overflow-hidden rounded bg-zinc-800" role="progressbar" :aria-label="`${label(operation)} progress`" :aria-valuenow="percent(operation) ?? undefined" :aria-valuetext="formatProgress(operation)" aria-valuemin="0" aria-valuemax="100">
                <div class="h-full bg-sky-600 transition-[width]" :style="{ width: `${percent(operation)}%` }" />
              </div>
              <div v-else-if="!operation.paused && operation.kind === 'delete' && percent(operation) === null" class="h-2 overflow-hidden rounded bg-zinc-800" role="progressbar" :aria-label="`${label(operation)} progress`" :aria-valuetext="formatProgress(operation)">
                <div class="webfile-indeterminate h-full w-1/3 rounded bg-sky-600" />
              </div>
              <div v-else class="flex items-center gap-2 text-xs text-zinc-500" role="status" :aria-label="`${label(operation)} ${phaseLabel(operation)}`">
                <i class="pi text-xs" :class="operation.paused ? 'pi-pause' : 'pi-spinner motion-safe:animate-spin'" aria-hidden="true" />
                <span>{{ formatProgress(operation) }}</span>
              </div>
              <div v-if="operation.state !== 'queued'" class="mt-2 flex items-center justify-between gap-3 text-xs text-zinc-500">
                <Button v-if="operation.kind === 'extract' && operation.state === 'awaiting_input' && operation.phase === 'awaiting_password'" label="Enter password" icon="pi pi-key" size="small" severity="secondary" outlined @click="emit('password', operation)" />
                <span v-else-if="operation.progress_total > 0 || (operation.kind === 'delete' && operation.progress_unit === 'items')">{{ formatProgress(operation) }}</span>
                <span v-else />
                <Button v-if="operation.cancellable && ['preparing', 'running', 'awaiting_input'].includes(operation.state)" :label="operation.paused ? 'Resume' : 'Pause'" :icon="operation.paused ? 'pi pi-play' : 'pi pi-pause'" :aria-label="`${operation.paused ? 'Resume' : 'Pause'} ${label(operation)}`" size="small" severity="secondary" text @click="emit('pause', operation)" />
                <Button v-if="operation.cancellable" label="Cancel" size="small" severity="secondary" text @click="emit('cancel', operation)" />
              </div>
              <p v-if="transferNotice(operation)" class="mt-1 text-xs text-zinc-400" role="status">{{ transferNotice(operation) }}</p>
              <p v-if="etaLabel(operation)" class="mt-1 text-xs text-zinc-400" role="status" data-task-eta>{{ etaLabel(operation) }}</p>
            </div>
            <details v-if="operation.destination" class="mt-2 text-xs text-zinc-500">
              <summary class="cursor-pointer select-none">Details</summary>
              <dl class="mt-2 space-y-1">
                <div v-if="operation.destination"><dt class="inline font-medium text-zinc-400">Destination:</dt> <dd class="inline break-all">{{ operation.destination }}</dd></div>
              </dl>
            </details>
          </article>
        </div>
      </section>

      <section v-if="history.length" aria-labelledby="task-history-heading">
        <div class="mb-2 flex min-h-8 items-center justify-between gap-2">
          <h3 id="task-history-heading">
            <button
              class="flex items-center gap-2 rounded px-1 py-1 text-xs font-semibold uppercase tracking-wide text-zinc-500 hover:bg-zinc-800"
              :aria-expanded="historyExpanded"
              aria-controls="task-history-content"
              :aria-label="`${historyExpanded ? 'Collapse' : 'Expand'} History (${history.length})`"
              @click="historyExpanded = !historyExpanded"
            >
              <i class="pi text-[0.65rem]" :class="historyExpanded ? 'pi-chevron-down' : 'pi-chevron-right'" />
              <span>History</span>
              <span class="rounded-full bg-zinc-800 px-1.5 py-0.5 text-[0.65rem] tabular-nums text-zinc-300">{{ history.length }}</span>
            </button>
          </h3>
          <Button v-tooltip.left="'Clear history'" icon="pi pi-trash" size="small" severity="secondary" text aria-label="Clear task history" @click="emit('clear-history')" />
        </div>
        <div v-if="historyExpanded" id="task-history-content" class="space-y-3">
          <article v-for="operation in history" :key="operation.id" class="rounded-lg border border-zinc-800 bg-zinc-900/60 p-3">
            <div class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3">
              <div class="min-w-0">
                <div class="truncate text-sm font-medium">{{ label(operation) }}</div>
                <div class="mt-1 text-xs text-zinc-500">{{ formatDate(operation.finished_at || operation.updated_at) }}</div>
                <p v-if="operation.error" class="webfile-text-error text-xs">{{ operation.error }}</p>
                <p v-if="operation.cleanup_error" class="webfile-text-error text-xs">Cleanup: {{ operation.cleanup_error }}</p>
                <p v-if="operation.undo_reason" class="mt-1 text-xs text-zinc-500">{{ operation.undo_reason }}</p>
                <p v-if="canUndo(operation)" class="mt-1 text-xs text-zinc-500">Undo available until {{ formatDate(operation.undo_expires_at) }}</p>
                <details v-if="operation.message || operation.destination" class="text-xs text-zinc-500">
                  <summary class="cursor-pointer select-none">Details</summary>
                  <dl class="mt-2 space-y-1">
                    <div v-if="operation.message"><dt class="inline font-medium text-zinc-400">Result:</dt> <dd class="inline">{{ operation.message }}</dd></div>
                    <div v-if="operation.destination"><dt class="inline font-medium text-zinc-400">Destination:</dt> <dd class="inline break-all">{{ operation.destination }}</dd></div>
                  </dl>
                </details>
              </div>
              <div class="flex shrink-0 flex-col items-end gap-2">
                <div class="flex items-start justify-end gap-1" data-task-actions>
                  <Button v-if="canUndo(operation)" label="Undo" icon="pi pi-undo" size="small" severity="secondary" text :aria-label="`Undo ${label(operation)}`" @click="emit('undo', operation)" />
                  <Button v-if="operation.kind === 'archive' && operation.state === 'completed' && operation.phase === 'completed'" v-tooltip.left="'Download archive'" icon="pi pi-download" size="small" severity="success" text :aria-label="`Download ${label(operation)}`" @click="emit('download', operation)" />
                  <Button v-tooltip.left="'Remove from history'" icon="pi pi-times" size="small" severity="secondary" text :aria-label="`Remove ${label(operation)} from history`" @click="emit('remove-history', operation)" />
                </div>
                <span class="self-end rounded px-2 py-0.5 text-xs capitalize" data-task-status :class="{
                    'webfile-status-success': operation.state === 'completed',
                    'webfile-status-error': operation.state === 'failed',
                    'webfile-status-warning': operation.state === 'interrupted',
                    'webfile-status-neutral': operation.state === 'cancelled',
                  }">{{ stateLabel(operation) }}</span>
              </div>
            </div>
          </article>
        </div>
      </section>
      </div>
    </div>
  </Drawer>
</template>
