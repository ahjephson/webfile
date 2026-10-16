<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ProgressSpinner from 'primevue/progressspinner'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import NameDialog from '@/components/NameDialog.vue'
import LevelUpIcon from '@/components/LevelUpIcon.vue'
import { isEntryVisible } from '@/utils/entryVisibility'

const props = defineProps<{
  visible: boolean
  mode: 'copy' | 'move'
  count: number
  initialPath: string
  showHidden: boolean
  hideInaccessible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [path: string]
}>()

const current = ref('/')
const entries = ref<FileEntry[]>([])
const loading = ref(false)
const error = ref('')
const focusedPath = ref('')
const singleRootPath = ref('')
const newFolderVisible = ref(false)
const createError = ref('')
let loadRequest = 0
const viewport = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(352)
const rowHeight = 48
let viewportObserver: ResizeObserver | null = null

const folders = computed(() => entries.value.filter(entry => entry.is_dir && isEntryVisible(entry, {
  showHidden: props.showHidden, hideInaccessible: props.hideInaccessible,
})))
const parentPath = computed(() => {
  if (current.value === '/' || current.value === singleRootPath.value) return ''
  const parts = current.value.split('/').filter(Boolean)
  parts.pop()
  const parent = parts.length ? `/${parts.join('/')}` : '/'
  return singleRootPath.value && !parent.startsWith(`${singleRootPath.value}/`) && parent !== singleRootPath.value ? '' : parent
})
const visibleFolders = computed(() => {
  const offset = Math.max(0, scrollTop.value - (parentPath.value ? rowHeight : 0))
  const start = Math.max(0, Math.floor(offset / rowHeight) - 5)
  const end = Math.min(folders.value.length, Math.ceil((offset + viewportHeight.value) / rowHeight) + 5)
  return folders.value.slice(start, end).map((folder, index) => ({ folder, index: start + index }))
})
watch(viewport, element => {
  viewportObserver?.disconnect()
  if (!element) return
  viewportObserver = new ResizeObserver(entries => { viewportHeight.value = entries[0]?.contentRect.height ?? 352 })
  viewportObserver.observe(element)
})
onBeforeUnmount(() => viewportObserver?.disconnect())
const crumbs = computed(() => {
  const displayRoot = singleRootPath.value || '/'
  const relativePath = singleRootPath.value && current.value.startsWith(`${singleRootPath.value}/`)
    ? current.value.slice(singleRootPath.value.length)
    : current.value === displayRoot ? '' : current.value
  const parts = relativePath.split('/').filter(Boolean)
  const result: Array<{ label: string; path: string }> = [{ label: singleRootPath.value ? 'Files' : 'Roots', path: displayRoot }]
  let cursor = singleRootPath.value
  for (const part of parts) {
    cursor += `/${part}`
    result.push({ label: part, path: cursor })
  }
  return result
})
const destinationPath = computed(() => focusedPath.value || current.value)

watch(() => props.visible, visible => {
  loadRequest++
  if (!visible) return
  current.value = props.initialPath || '/'
  createError.value = ''
  void load()
})

async function load(): Promise<boolean> {
  const request = ++loadRequest
  const path = current.value
  loading.value = true
  error.value = ''
  focusedPath.value = ''
  scrollTop.value = 0
  if (viewport.value) viewport.value.scrollTop = 0
  try {
    const response = await api.list(path)
    if (!props.visible || request !== loadRequest) return false
    singleRootPath.value = response.single_root_path ?? ''
    if (path === '/' && singleRootPath.value) {
      current.value = singleRootPath.value
      return await load()
    }
    entries.value = response.entries
    return true
  } catch (err) {
    if (!props.visible || request !== loadRequest) return false
    error.value = err instanceof Error ? err.message : String(err)
    entries.value = []
    return false
  } finally {
    if (request === loadRequest) loading.value = false
  }
}

function open(path: string) {
  current.value = path
  void load()
}

function openFolder(folder: FileEntry) {
  if (folder.can_navigate === false) return
  open(folder.path)
}

function selectFolder(folder: FileEntry) {
  if (folder.can_navigate === false) return
  focusedPath.value = folder.path
}

function joinPath(base: string, name: string) {
  return `${base === '/' ? '' : base}/${name}`.replace(/\/+/g, '/') || '/'
}

async function createFolder(name: string) {
  const request = loadRequest
  const path = joinPath(current.value, name)
  createError.value = ''
  try {
    await api.mkdir(path)
    if (!props.visible || request !== loadRequest) return
    if (await load()) {
      focusedPath.value = path
      await nextTick()
      const index = folders.value.findIndex(folder => folder.path === path)
      if (index >= 0 && viewport.value) viewport.value.scrollTop = (index + (parentPath.value ? 1 : 0)) * rowHeight
    }
  } catch (err) {
    if (props.visible && request === loadRequest) createError.value = err instanceof Error ? err.message : String(err)
  }
}

function submit() {
  emit('submit', destinationPath.value)
  emit('update:visible', false)
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :header="mode === 'copy' ? 'Copy to…' : 'Move to…'"
    :style="{ width: 'min(46rem, 94vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="space-y-4">
      <div class="flex items-center justify-between gap-3">
        <p class="text-sm text-zinc-300">{{ count }} item{{ count === 1 ? '' : 's' }}</p>
        <Button label="New folder" icon="pi pi-folder-plus" severity="secondary" text :disabled="loading" @click="newFolderVisible = true" />
      </div>
      <p v-if="createError" class="webfile-text-error text-sm">{{ createError }}</p>

      <nav class="flex min-h-9 flex-wrap items-center gap-1 rounded-lg border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm">
        <template v-for="(crumb, index) in crumbs" :key="crumb.path">
          <button class="rounded px-2 py-1 text-zinc-300 hover:bg-zinc-800" @click="open(crumb.path)">{{ crumb.label }}</button>
          <i v-if="index < crumbs.length - 1" class="pi pi-angle-right text-xs text-zinc-600" />
        </template>
      </nav>

      <div ref="viewport" class="h-[22rem] overflow-auto rounded-xl border border-zinc-800 bg-zinc-950" data-destination-viewport @scroll="scrollTop = viewport?.scrollTop ?? 0">
        <div v-if="loading" class="grid h-full place-items-center"><ProgressSpinner class="h-8 w-8" /></div>
        <div v-else-if="error" class="webfile-text-error p-4 text-sm">{{ error }}</div>
        <template v-else>
          <button
            v-if="parentPath"
            class="flex h-12 w-full items-center gap-3 border-b border-zinc-900 px-4 text-left hover:bg-zinc-900 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-emerald-400"
            :class="focusedPath === parentPath ? 'bg-zinc-900' : ''"
            aria-label="Parent directory"
            @click="focusedPath = parentPath"
            @dblclick="open(parentPath)"
            @keydown.enter.prevent="open(parentPath)"
          >
            <LevelUpIcon class="h-5 w-5 text-zinc-300" />
            <span class="min-w-0 flex-1" />
          </button>
          <div class="relative" :style="{ height: `${folders.length * rowHeight}px` }">
          <button
            v-for="{ folder, index } in visibleFolders"
            :key="folder.path"
            class="absolute left-0 flex h-12 w-full items-center gap-3 border-b border-zinc-900 px-4 text-left hover:bg-zinc-900 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-emerald-400"
            :style="{ top: `${index * rowHeight}px` }"
            :title="folder.can_navigate === false ? 'You do not have permission to open this folder' : undefined"
            :class="focusedPath === folder.path ? 'bg-zinc-900' : ''"
            :aria-disabled="folder.can_navigate === false"
            @click="selectFolder(folder)"
            @dblclick="openFolder(folder)"
            @keydown.enter.prevent="openFolder(folder)"
          >
            <span class="relative grid place-items-center">
              <i class="pi pi-folder text-lg" :class="folder.can_navigate === false ? 'text-zinc-500' : 'webfile-accent-warning'" aria-hidden="true" />
              <i v-if="folder.can_navigate === false" class="webfile-accent-danger pi pi-lock absolute -bottom-1 -right-2 text-[0.65rem]" :aria-label="`No permission to open ${folder.name}`" data-directory-lock />
            </span>
            <span class="min-w-0 flex-1 truncate">{{ folder.name }}</span>
          </button>
          </div>
          <div v-if="folders.length === 0" class="p-6 text-center text-sm text-zinc-500">No folders here.</div>
        </template>
      </div>

      <div class="flex items-center justify-between gap-4">
        <div class="min-w-0 text-sm text-zinc-400">
          Destination: <span class="font-mono text-zinc-200">{{ destinationPath }}</span>
        </div>
        <div class="flex shrink-0 gap-2">
          <Button label="Cancel" severity="secondary" text @click="emit('update:visible', false)" />
          <Button :label="mode === 'copy' ? 'Copy here' : 'Move here'" @click="submit" />
        </div>
      </div>
    </div>
  </Dialog>
  <NameDialog
    v-model:visible="newFolderVisible"
    title="New folder"
    label="Folder name"
    submit-label="Create"
    @submit="createFolder"
  />
</template>
