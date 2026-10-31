<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { FileEntry } from '@/types/files'
import FileVisual from '@/components/FileVisual.vue'
import LevelUpIcon from '@/components/LevelUpIcon.vue'
import { acceptsFileDrop } from '@/composables/useExplorerWorkspace'
import { captureListingScroll, restoreListingScroll, type ListingScrollPosition } from '@/utils/listingScroll'

const props = defineProps<{
  entries: FileEntry[]
  selectedPaths: string[]
  cutPaths: string[]
  focusedPath: string
  selectionControlsVisible: boolean
  size: 'small' | 'medium' | 'large'
  thumbnailPendingPaths: string[]
  parentPath?: string
  showLocation?: boolean
  canDrag?: boolean
  dropTargetPath?: string
}>()

const emit = defineEmits<{
  select: [entry: FileEntry, event: MouseEvent]
  focus: [entry: FileEntry, event: MouseEvent]
  activate: [entry: FileEntry, input: 'mouse' | 'touch']
  context: [entry: FileEntry, event: MouseEvent | PointerEvent]
  dropfiles: [entry: FileEntry, event: DragEvent]
  'columns-change': [columns: number]
  parent: [input: 'mouse' | 'touch']
  dragentry: [entry: FileEntry, event: DragEvent]
}>()

const viewport = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(400)
const width = ref(800)
const gap = 8
const overscanRows = 3
let observer: ResizeObserver | null = null
let ignoreTouchActivationUntil = 0
let touchPointerUntil = 0

const selected = computed(() => new Set(props.selectedPaths))
const cut = computed(() => new Set(props.cutPaths))
const thumbnailPending = computed(() => new Set(props.thumbnailPendingPaths))
const metrics = computed(() => ({
  small: { cardMinWidth: 118, rowHeight: 132, cellHeight: 124 },
  medium: { cardMinWidth: 170, rowHeight: 176, cellHeight: 168 },
  large: { cardMinWidth: 238, rowHeight: 232, cellHeight: 224 },
})[props.size])
const locationHeight = computed(() => props.showLocation ? 32 : 0)
const rowHeight = computed(() => metrics.value.rowHeight + locationHeight.value)
function containingFolder(entry: FileEntry) { return entry.path.slice(0, entry.path.lastIndexOf('/')) || '/' }
const columns = computed(() => Math.max(1, Math.floor((width.value + gap) / (metrics.value.cardMinWidth + gap))))
const rowCount = computed(() => Math.ceil(props.entries.length / columns.value))
const startRow = computed(() => Math.max(0, Math.floor(scrollTop.value / rowHeight.value) - overscanRows))
const endRow = computed(() => Math.min(rowCount.value, Math.ceil((scrollTop.value + viewportHeight.value) / rowHeight.value) + overscanRows))
const visibleRows = computed(() => {
  const rows: Array<{ row: number; entries: Array<{ entry: FileEntry; index: number }> }> = []
  for (let row = startRow.value; row < endRow.value; row++) {
    const first = row * columns.value
    const rowEntries = props.entries.slice(first, first + columns.value).map((entry, offset) => ({ entry, index: first + offset }))
    rows.push({ row, entries: rowEntries })
  }
  return rows
})
const totalHeight = computed(() => rowCount.value * rowHeight.value)

onMounted(() => {
  observer = new ResizeObserver(entries => {
    const rect = entries[0]?.contentRect
    if (!rect) return
    viewportHeight.value = rect.height
    width.value = rect.width
    emit('columns-change', columns.value)
  })
  if (viewport.value) observer.observe(viewport.value)
})
onBeforeUnmount(() => observer?.disconnect())

watch(() => props.size, () => {
  void nextTick(() => emit('columns-change', columns.value))
})

function onScroll() {
  scrollTop.value = viewport.value?.scrollTop ?? 0
}

function scrollToIndex(index: number) {
  const element = viewport.value
  if (!element || index < 0) return
  const row = Math.floor(index / columns.value)
  const top = row * rowHeight.value
  const bottom = top + rowHeight.value
  if (top < element.scrollTop) element.scrollTop = top
  else if (bottom > element.scrollTop + element.clientHeight) element.scrollTop = bottom - element.clientHeight
}

function scrollToIndexAtTop(index: number) {
  const element = viewport.value
  if (!element || index < 0 || element.scrollTop <= 1) return
  element.scrollTop = Math.floor(index / columns.value) * rowHeight.value
  scrollTop.value = element.scrollTop
}

function captureScrollPosition() {
  return props.showLocation ? null : captureListingScroll(viewport.value, props.entries, columns.value, rowHeight.value)
}

async function restoreScrollPosition(position: ListingScrollPosition | null) {
  const element = viewport.value
  if (!element) return
  // A remounted grid has not necessarily received its first ResizeObserver
  // callback. Measure before calculating the anchor's row in the new layout.
  const style = getComputedStyle(element)
  width.value = element.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  viewportHeight.value = element.clientHeight
  await nextTick()
  restoreListingScroll(viewport.value, props.entries, columns.value, rowHeight.value, position)
  onScroll()
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

defineExpose({ scrollToIndex, scrollToIndexAtTop, captureScrollPosition, restoreScrollPosition, columnCount: columns })
</script>

<template>
  <div class="flex h-full min-h-0 flex-col" :data-grid-size="size">
    <button
      v-if="parentPath"
      class="mx-2 mt-2 flex h-11 shrink-0 items-center gap-3 rounded-lg border border-zinc-800 px-3 text-left text-sm hover:bg-zinc-900/70"
      aria-label="Parent directory"
      @click="onParentClick($event)"
      @pointerdown="onParentPointerDown($event)"
    >
      <span class="h-5 w-5 shrink-0" />
      <LevelUpIcon class="h-5 w-5 text-zinc-300" />
    </button>
    <div ref="viewport" class="min-h-0 flex-1 overflow-auto overscroll-contain p-2" data-file-viewport @scroll="onScroll">
    <div class="relative select-none" :style="{ height: `${totalHeight}px` }">
      <div
        v-for="row in visibleRows"
        :key="row.row"
        class="absolute left-0 right-0 grid gap-2"
        :style="{ top: `${row.row * rowHeight}px`, gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, height: `${rowHeight}px` }"
      >
        <div
          v-for="cell in row.entries"
          :key="cell.entry.path"
          class="group relative flex cursor-default flex-col items-center justify-center gap-1.5 overflow-hidden rounded-lg border border-transparent p-2 text-center transition-opacity hover:bg-zinc-900/70"
          :style="{ height: `${metrics.cellHeight + locationHeight}px` }"
          :class="[
            focusedPath === cell.entry.path && selected.has(cell.entry.path) ? 'webfile-focus-tint' : selected.has(cell.entry.path) ? 'webfile-selection-tint' : '',
            selected.has(cell.entry.path) || focusedPath === cell.entry.path ? 'outline outline-1 outline-sky-500/70' : '',
            cut.has(cell.entry.path) ? 'opacity-50' : '',
            dropTargetPath === cell.entry.path ? 'webfile-drop-target' : '',
          ]"
          :data-cut="cut.has(cell.entry.path) ? 'true' : undefined"
          data-file-entry
          v-longpress="longPressEntry(cell.entry)"
          @click="onEntryClick(cell.entry, $event)"
          @pointerdown="onEntryPointerDown($event)"
          @contextmenu="onContextMenu(cell.entry, $event)"
          @dragover="onDragOverFolder(cell.entry, $event)"
          :draggable="canDrag"
          :data-drop-directory="cell.entry.is_dir ? cell.entry.path : undefined"
          :data-drop-target="dropTargetPath === cell.entry.path ? 'true' : undefined"
          @dragstart="emit('dragentry', cell.entry, $event)"
          @drop="onDropFolder(cell.entry, $event)"
        >
          <button
            class="absolute right-1 top-1 z-10 grid h-10 w-10 place-items-center rounded-md text-sm"
            :class="selectionControlsVisible ? 'visible pointer-events-auto' : 'invisible pointer-events-none'"
            :aria-label="`${selected.has(cell.entry.path) ? 'Unselect' : 'Select'} ${cell.entry.name}`"
            :aria-pressed="selected.has(cell.entry.path)"
            :aria-hidden="!selectionControlsVisible"
            :tabindex="selectionControlsVisible ? 0 : -1"
            data-selection-control
            @click.stop="onSelectionClick(cell.entry, $event)"
            @dblclick.stop
          >
            <span
              class="webfile-selection-box grid h-6 w-6 place-items-center rounded border border-zinc-500 shadow-sm"
              :class="selected.has(cell.entry.path) ? 'webfile-check-active' : 'bg-zinc-800 text-transparent'"
            >
              <i class="pi pi-check text-xs" />
            </span>
          </button>
          <FileVisual :entry="cell.entry" tile :tile-size="size" :thumbnail-enabled="!thumbnailPending.has(cell.entry.path)" />
          <span class="w-full truncate text-sm" :title="cell.entry.name">{{ cell.entry.name }}</span>
          <span v-if="showLocation" class="line-clamp-2 w-full break-all text-xs text-zinc-500" :title="containingFolder(cell.entry)" data-result-location>{{ containingFolder(cell.entry) }}</span>
          <span class="h-[17px] w-full text-xs text-zinc-500">
            {{ !cell.entry.is_dir || cell.entry.size_status === 'ready' ? formatBytes(cell.entry.size) : '' }}
          </span>
        </div>
      </div>
    </div>
    </div>
  </div>
</template>
