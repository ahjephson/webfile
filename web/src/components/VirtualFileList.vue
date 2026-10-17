<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { FileEntry } from '@/types/files'
import FileVisual from '@/components/FileVisual.vue'
import LevelUpIcon from '@/components/LevelUpIcon.vue'
import type { SortField, SortDirection } from '@/utils/sorting'
import { acceptsFileDrop } from '@/composables/useExplorerWorkspace'

const props = defineProps<{
  entries: FileEntry[]
  selectedPaths: string[]
  cutPaths: string[]
  focusedPath: string
  selectionControlsVisible: boolean
  parentPath?: string
  sortField?: SortField
  sortDirection?: SortDirection
  showLocation?: boolean
  compact?: boolean
  canDrag?: boolean
  dropTargetPath?: string
}>()

const emit = defineEmits<{
  select: [entry: FileEntry, event: MouseEvent]
  focus: [entry: FileEntry, event: MouseEvent]
  activate: [entry: FileEntry, input: 'mouse' | 'touch']
  context: [entry: FileEntry, event: MouseEvent | PointerEvent]
  dropfiles: [entry: FileEntry, event: DragEvent]
  parent: [input: 'mouse' | 'touch']
  sort: [field: SortField]
  dragentry: [entry: FileEntry, event: DragEvent]
}>()

const viewport = ref<HTMLElement | null>(null)
const headerViewport = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(400)
const rowHeight = 44
const overscan = 10
let observer: ResizeObserver | null = null
let ignoreTouchActivationUntil = 0
let touchPointerUntil = 0

const selected = computed(() => new Set(props.selectedPaths))
const columnsStyle = computed(() => ({ gridTemplateColumns: props.compact
  ? (props.showLocation ? 'minmax(10rem,1fr) minmax(8rem,1fr) 6rem' : 'minmax(10rem,1fr) 6rem')
  : props.showLocation ? 'minmax(18rem,1fr) minmax(16rem,1fr) 8rem 13rem 8rem' : 'minmax(18rem,1fr) 8rem 13rem 8rem' }))
const minimumWidth = computed(() => props.compact ? (props.showLocation ? '26rem' : '16rem') : props.showLocation ? '65rem' : '49rem')
function containingFolder(entry: FileEntry) { return entry.path.slice(0, entry.path.lastIndexOf('/')) || '/' }
const cut = computed(() => new Set(props.cutPaths))
const startIndex = computed(() => Math.max(0, Math.floor(scrollTop.value / rowHeight) - overscan))
const endIndex = computed(() => Math.min(props.entries.length, Math.ceil((scrollTop.value + viewportHeight.value) / rowHeight) + overscan))
const visible = computed(() => props.entries.slice(startIndex.value, endIndex.value).map((entry, offset) => ({ entry, index: startIndex.value + offset })))
const totalHeight = computed(() => props.entries.length * rowHeight)

onMounted(() => {
  observer = new ResizeObserver(entries => {
    viewportHeight.value = entries[0]?.contentRect.height ?? viewportHeight.value
  })
  if (viewport.value) observer.observe(viewport.value)
})
onBeforeUnmount(() => observer?.disconnect())
watch(() => props.entries.length, () => {
  const max = Math.max(0, totalHeight.value - viewportHeight.value)
  if (scrollTop.value > max && viewport.value) viewport.value.scrollTop = max
})

function onScroll() {
  scrollTop.value = viewport.value?.scrollTop ?? 0
  if (headerViewport.value) headerViewport.value.scrollLeft = viewport.value?.scrollLeft ?? 0
}

function scrollToIndex(index: number) {
  const element = viewport.value
  if (!element || index < 0) return
  const top = index * rowHeight
  const bottom = top + rowHeight
  if (top < element.scrollTop) element.scrollTop = top
  else if (bottom > element.scrollTop + element.clientHeight) element.scrollTop = bottom - element.clientHeight
}

function scrollToIndexAtTop(index: number) {
  const element = viewport.value
  if (!element || index < 0 || element.scrollTop <= 1) return
  element.scrollTop = index * rowHeight
  scrollTop.value = element.scrollTop
}

function onDropFolder(entry: FileEntry, event: DragEvent) {
  if (!entry.is_dir || !acceptsFileDrop(event)) return
  event.preventDefault()
  event.stopPropagation()
  emit('dropfiles', entry, event)
}

function onDragOverFolder(entry: FileEntry, event: DragEvent) {
  if (entry.is_dir && acceptsFileDrop(event)) event.preventDefault()
}

function onEntryPointerDown(event: PointerEvent) {
  if ((event.target as Element).closest('[data-selection-control]')) return
  if (event.pointerType === 'touch' || event.pointerType === 'pen') touchPointerUntil = Date.now() + 1000
}

function isTouchActivation(event: MouseEvent | PointerEvent) {
  const directTouch = 'pointerType' in event && (event.pointerType === 'touch' || event.pointerType === 'pen')
  const followsTouchPointer = Date.now() < touchPointerUntil
  if (directTouch || followsTouchPointer) touchPointerUntil = 0
  return directTouch || followsTouchPointer
}

function onEntryClick(entry: FileEntry, event: MouseEvent | PointerEvent) {
  const isTouch = isTouchActivation(event)
  if (isTouch) {
    if (Date.now() < ignoreTouchActivationUntil) return
    ignoreTouchActivationUntil = Date.now() + 600
    if (props.selectionControlsVisible) {
      emit('select', entry, event as MouseEvent)
      return
    }
    emit('activate', entry, 'touch')
    return
  }
  if (Date.now() < ignoreTouchActivationUntil) return
  if (event.detail > 0 && event.detail % 2 === 0) emit('activate', entry, 'mouse')
  else emit('focus', entry, event as MouseEvent)
}

function onParentPointerDown(event: PointerEvent) {
  if (event.pointerType === 'touch' || event.pointerType === 'pen') touchPointerUntil = Date.now() + 1000
}

function onParentClick(event: MouseEvent | PointerEvent) {
  const isTouch = isTouchActivation(event)
  if (isTouch) {
    if (Date.now() < ignoreTouchActivationUntil) return
    ignoreTouchActivationUntil = Date.now() + 600
    emit('parent', 'touch')
    return
  }
  if (Date.now() < ignoreTouchActivationUntil) return
  emit('parent', 'mouse')
}

function onSelectionClick(entry: FileEntry, event: MouseEvent) {
  emit('select', entry, event)
}

function onContextMenu(entry: FileEntry, event: MouseEvent | PointerEvent) {
  emit('context', entry, event)
}

function longPressEntry(entry: FileEntry) {
  return (event: PointerEvent) => onContextMenu(entry, event)
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let value = bytes / 1024
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[index]}`
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

defineExpose({ scrollToIndex, scrollToIndexAtTop, columnCount: 1 })
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div ref="headerViewport" class="shrink-0 overflow-hidden border-b border-zinc-800" aria-label="File sorting">
    <div class="grid px-3 py-2 text-xs text-zinc-500" :style="{ ...columnsStyle, minWidth: minimumWidth }">
      <button class="text-left" aria-label="Sort by Name" @click="emit('sort', 'name')">Name <i v-if="sortField === 'name'" class="pi text-[0.6rem]" :class="sortDirection === 'desc' ? 'pi-arrow-down' : 'pi-arrow-up'" /></button>
      <span v-if="showLocation">Location</span>
      <button v-for="column in (compact ? [{ field: 'size', label: 'Size' }] : [{ field: 'size', label: 'Size' }, { field: 'modified', label: 'Modified' }])" :key="column.field" class="text-left" :aria-label="`Sort by ${column.label}`" @click="emit('sort', column.field as SortField)">
        {{ column.label }} <i v-if="sortField === column.field" class="pi text-[0.6rem]" :class="sortDirection === 'desc' ? 'pi-arrow-down' : 'pi-arrow-up'" />
      </button>
      <span v-if="!compact">Permissions</span>
    </div>
    </div>
    <button
      v-if="parentPath"
      class="grid h-11 shrink-0 grid-cols-[minmax(18rem,1fr)_8rem_13rem_8rem] items-center border-b border-zinc-900 px-3 text-left text-sm hover:bg-zinc-900/70"
      aria-label="Parent directory"
      @click="onParentClick($event)"
      @pointerdown="onParentPointerDown($event)"
    >
      <span class="flex min-w-0 items-center gap-3">
        <span class="h-5 w-5 shrink-0" />
        <LevelUpIcon class="h-5 w-5 text-zinc-300" />
      </span>
      <span class="text-zinc-500">—</span><span /><span />
    </button>
    <div ref="viewport" class="min-h-0 flex-1 overflow-auto overscroll-contain" data-file-viewport @scroll="onScroll">
    <div class="relative select-none" :style="{ height: `${totalHeight}px`, minWidth: minimumWidth }">
      <div
        v-for="row in visible"
        :key="row.entry.path"
        class="absolute left-0 right-0 grid cursor-default items-center border-b border-zinc-900 px-3 text-sm transition-opacity hover:bg-zinc-900/70"
        :class="[
          focusedPath === row.entry.path && selected.has(row.entry.path) ? 'webfile-focus-tint' : selected.has(row.entry.path) ? 'webfile-selection-tint' : '',
          selected.has(row.entry.path) || focusedPath === row.entry.path ? 'outline outline-1 -outline-offset-1 outline-sky-500/70' : '',
          cut.has(row.entry.path) ? 'opacity-50' : '',
          dropTargetPath === row.entry.path ? 'webfile-drop-target' : '',
        ]"
        :style="{ ...columnsStyle, height: `${rowHeight}px`, transform: `translateY(${row.index * rowHeight}px)` }"
        :data-cut="cut.has(row.entry.path) ? 'true' : undefined"
        data-file-entry
        :draggable="canDrag"
        :data-drop-directory="row.entry.is_dir ? row.entry.path : undefined"
        :data-drop-target="dropTargetPath === row.entry.path ? 'true' : undefined"
        @dragstart="emit('dragentry', row.entry, $event)"
        v-longpress="longPressEntry(row.entry)"
        @click="onEntryClick(row.entry, $event)"
        @pointerdown="onEntryPointerDown($event)"
        @contextmenu="onContextMenu(row.entry, $event)"
        @dragover="onDragOverFolder(row.entry, $event)"
        @drop="onDropFolder(row.entry, $event)"
      >
        <div class="flex min-w-0 items-center gap-3 py-2">
          <span class="relative h-5 w-5 shrink-0">
            <button
              class="absolute left-1/2 top-1/2 grid h-10 w-10 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-md text-sm"
              :class="selectionControlsVisible ? 'visible pointer-events-auto' : 'invisible pointer-events-none'"
              :aria-label="`${selected.has(row.entry.path) ? 'Unselect' : 'Select'} ${row.entry.name}`"
              :aria-pressed="selected.has(row.entry.path)"
              :aria-hidden="!selectionControlsVisible"
              :tabindex="selectionControlsVisible ? 0 : -1"
              data-selection-control
              @click.stop="onSelectionClick(row.entry, $event)"
              @dblclick.stop
            >
              <span
                class="webfile-selection-box grid h-6 w-6 place-items-center rounded border border-zinc-500 shadow-sm"
                :class="selected.has(row.entry.path) ? 'webfile-check-active' : 'bg-zinc-800 text-transparent'"
              >
                <i class="pi pi-check text-xs" />
              </span>
            </button>
          </span>
          <FileVisual :entry="row.entry" />
          <span class="truncate" :title="row.entry.name">{{ row.entry.name }}</span>
        </div>
        <span v-if="showLocation" class="truncate pr-3 text-xs text-zinc-500" :title="containingFolder(row.entry)" data-result-location>{{ containingFolder(row.entry) }}</span>
        <span class="text-zinc-400">{{ row.entry.is_dir && row.entry.size_status !== 'ready' ? '—' : formatBytes(row.entry.size) }}</span>
        <span v-if="!compact" class="text-zinc-400">{{ formatDate(row.entry.modified) }}</span>
        <span v-if="!compact" class="font-mono text-xs text-zinc-500">{{ row.entry.mode }}</span>
      </div>
    </div>
    </div>
  </div>
</template>
