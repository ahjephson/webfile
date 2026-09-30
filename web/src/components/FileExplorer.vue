<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Button from 'primevue/button'
import ContextMenu from 'primevue/contextmenu'
import InputText from 'primevue/inputtext'
import Menu from 'primevue/menu'
import ProgressSpinner from 'primevue/progressspinner'
import SplitButton from 'primevue/splitbutton'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import { api } from '@/api/client'
import type { Favourite, FileEntry, FileOperation, TrashItem } from '@/types/files'
import { resolvedTheme, setTheme, themeMode, type ThemeMode } from '@/theme'
import { useRealtime } from '@/composables/useRealtime'
import { useFilePreview } from '@/composables/useFilePreview'
import { useExplorerSelection } from '@/composables/useExplorerSelection'
import { useExplorerNavigation } from '@/composables/useExplorerNavigation'
import { useFileOperations } from '@/composables/useFileOperations'
import NewDocumentDialog from '@/components/NewDocumentDialog.vue'
import NameDialog from '@/components/NameDialog.vue'
import ExtractDialog from '@/components/ExtractDialog.vue'
import ArchivePasswordDialog from '@/components/ArchivePasswordDialog.vue'
import DestinationDialog from '@/components/DestinationDialog.vue'
import OperationsPanel from '@/components/OperationsPanel.vue'
import DetailsDialog from '@/components/DetailsDialog.vue'
import FilePreview from '@/components/FilePreview.vue'
import VirtualFileList from '@/components/VirtualFileList.vue'
import VirtualFileGrid from '@/components/VirtualFileGrid.vue'

const EditorDialog = defineAsyncComponent(() => import('@/components/EditorDialog.vue'))

type ViewMode = 'list' | 'grid'
type GridSize = 'small' | 'medium' | 'large'
type TransferMode = 'copy' | 'move'
const props = defineProps<{
  username: string
  showLogout: boolean
}>()

const emit = defineEmits<{ logout: [] }>()
const toast = useToast()
const confirm = useConfirm()

const {
  readSessionPath,
  readSessionState,
  readHashPath,
  readHashFile,
  writeHash: writePathHash,
  storePath: storeSessionPath,
  clearPath: clearSessionPath,
  storeState: storeExplorerState,
} = useExplorerNavigation(props.username)
const currentPath = ref(readHashPath() ?? readSessionPath())
const restoredExplorerState = readSessionState()
const restoredStateMatchesPath = restoredExplorerState.directory === currentPath.value
const entries = ref<FileEntry[]>([])
const loading = ref(false)
const error = ref('')
const search = ref('')
const filterVisible = ref(false)
const filterInput = ref<any>(null)
const selectionControlsVisible = ref(false)
const {
  selectedPaths,
  focusedPath,
  anchorPath,
  toggle: toggleSelection,
  clear: clearSelectionState,
  replace: replaceSelection,
} = useExplorerSelection(
  restoredStateMatchesPath ? restoredExplorerState.selectedPaths : [],
  restoredStateMatchesPath ? restoredExplorerState.focusedPath : '',
)
const viewMode = ref<ViewMode>('list')
const gridSize = ref<GridSize>('medium')
const showHidden = ref(false)
const favourites = ref<Favourite[]>([])
const gridColumns = ref(1)
const contextMenu = ref<any>(null)
const contextEntry = ref<FileEntry | null>(null)
const themeMenu = ref<any>(null)
const listView = ref<InstanceType<typeof VirtualFileList> | null>(null)
const gridView = ref<InstanceType<typeof VirtualFileGrid> | null>(null)
const fileViewHost = ref<HTMLElement | null>(null)
const breadcrumbScroll = ref<HTMLElement | null>(null)

const newDocumentVisible = ref(false)
const nameDialogVisible = ref(false)
const nameDialogMode = ref<'folder' | 'rename'>('folder')
const editorVisible = ref(false)
const editorPath = ref('')
const editorIsNew = ref(false)
const {
  visible: previewVisible,
  path: previewPath,
  kind: previewKind,
  contentType: previewContentType,
  detect: detectPreview,
  show: showDetectedPreview,
  navigate: navigateDetectedPreview,
  hide: hidePreview,
} = useFilePreview()
const extractVisible = ref(false)
const extractPasswordVisible = ref(false)
const extractPasswordOperationId = ref('')
const extractPasswordError = ref('')
const extractDestination = ref('')
const extractBusy = ref(false)
const destinationVisible = ref(false)
const transferMode = ref<TransferMode>('copy')
const operationsVisible = ref(false)
const detailsVisible = ref(restoredExplorerState.detailsVisible)
const detailsPath = ref(restoredStateMatchesPath ? restoredExplorerState.detailsPath : currentPath.value)
const favouritesVisible = ref(false)
const trashMode = ref(false)
const recycleReturnPath = ref('/')
const trashItems = ref<TrashItem[]>([])
const transferEntries = ref<FileEntry[]>([])
const renameTarget = ref<FileEntry | null>(null)
const extractTarget = ref<FileEntry | null>(null)
const uploadInput = ref<HTMLInputElement | null>(null)
const draggingOver = ref(false)
let invalidateTimer: number | null = null
let hashFileRequest = 0
let breadcrumbResizeObserver: ResizeObserver | null = null

const {
  operations,
  activeOperations,
  load: loadOperations,
  update: updateOperation,
  start: startFileOperation,
  clearHistory: clearFinishedOperations,
  removeHistory: removeFinishedOperation,
} = useFileOperations(onOperationTerminal)

const {
  start: startRealtime,
  setDirectory: setRealtimeDirectory,
  connected: watchConnected,
  cancelOperation: cancelRealtimeOperation,
} = useRealtime({
  onInvalidate: path => {
    if (trashMode.value) return
    if (path !== currentPath.value) return
    if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
    invalidateTimer = window.setTimeout(() => void refresh(false), 120)
  },
  onOperation: handleOperationUpdate,
  onConnected: () => { void loadOperations().then(restorePasswordPrompt) },
})

const trashEntries = computed<FileEntry[]>(() => trashItems.value.map(item => ({
  name: item.name,
  path: `recycle:${item.id}`,
  is_dir: item.is_dir,
  size: item.size,
  modified: item.deleted_at,
  mode: '',
})))

const filteredEntries = computed(() => {
  const q = search.value.trim().toLocaleLowerCase()
  const source = trashMode.value ? trashEntries.value : entries.value
  return source.filter(entry => {
    if (!trashMode.value && !showHidden.value && entry.name.startsWith('.')) return false
    return !q || entry.name.toLocaleLowerCase().includes(q)
  })
})

const selectedEntries = computed(() => (trashMode.value ? trashEntries.value : entries.value).filter(entry => selectedPaths.value.has(entry.path)))
const selectedOne = computed(() => selectedEntries.value.length === 1 ? selectedEntries.value[0] : null)
const canMutate = computed(() => !trashMode.value && currentPath.value !== '/')
const selectionContainsConfiguredRoot = computed(() => !trashMode.value && currentPath.value === '/' && selectedEntries.value.length > 0)
const currentFavourite = computed(() => favourites.value.some(item => item.path === currentPath.value))
const displayedParentPath = computed(() => trashMode.value ? '' : parentPath.value)

const themeItems = computed(() => (['system', 'dark', 'light'] as ThemeMode[]).map(mode => ({
  mode,
  label: mode[0].toUpperCase() + mode.slice(1),
  icon: themeMode.value === mode ? 'pi pi-check' : mode === 'system' ? 'pi pi-desktop' : mode === 'dark' ? 'pi pi-moon' : 'pi pi-sun',
})))

const contextItems = computed(() => {
  const clicked = contextEntry.value
  const items = clicked && !selectedPaths.value.has(clicked.path) ? [clicked] : selectedEntries.value
  if (items.length === 0) return []
  if (trashMode.value) {
    return [
      { label: `Restore${items.length > 1 ? ` ${items.length} items` : ''}`, icon: 'pi pi-replay', command: () => restoreTrash(items) },
      { label: 'Delete permanently', icon: 'pi pi-times', command: () => purgeTrash(items) },
    ]
  }
  const one = items.length === 1 ? items[0] : null
  const containsConfiguredRoot = currentPath.value === '/' && items.length > 0
  const result: Array<{ label?: string; icon?: string; command?: () => void; separator?: boolean; disabled?: boolean }> = []

  if (one) {
    result.push({ label: 'Open', icon: one.is_dir ? 'pi pi-folder-open' : 'pi pi-file-edit', command: () => openEntry(one) })
    result.push({ label: 'Download', icon: 'pi pi-download', command: () => download(one) })
    result.push({ label: 'Details', icon: 'pi pi-info-circle', command: () => toggleDetails(one) })
    if (isArchive(one)) {
      result.push({ label: 'Extract…', icon: 'pi pi-box', command: () => openExtract(one) })
      result.push({ label: 'Extract here', icon: 'pi pi-folder-open', command: () => extractHere(one) })
    }
  }

  if (!containsConfiguredRoot) {
    result.push(
      { separator: true },
      { label: `Copy${items.length > 1 ? ` ${items.length} items` : ''} to…`, icon: 'pi pi-copy', command: () => openTransfer('copy', items) },
      { label: `Move${items.length > 1 ? ` ${items.length} items` : ''} to…`, icon: 'pi pi-arrow-right', command: () => openTransfer('move', items) },
    )
    if (one) result.push({ label: 'Rename…', icon: 'pi pi-pencil', command: () => openRename(one) })
    result.push({ label: 'Delete', icon: 'pi pi-trash', command: () => deleteEntries(items) })
  }
  return result
})

const breadcrumbs = computed(() => {
  if (trashMode.value) return [{ name: 'Files', path: '/' }, { name: 'Recycle Bin', path: 'recycle:' }]
  if (currentPath.value === '/') return [{ name: 'Files', path: '/' }]
  const parts = currentPath.value.split('/').filter(Boolean)
  const crumbs = [{ name: 'Files', path: '/' }]
  let p = ''
  for (const part of parts) {
    p += `/${part}`
    crumbs.push({ name: part, path: p })
  }
  return crumbs
})

const parentPath = computed(() => {
  if (currentPath.value === '/') return ''
  const parts = currentPath.value.split('/').filter(Boolean)
  parts.pop()
  return parts.length ? `/${parts.join('/')}` : '/'
})

onMounted(async () => {
  startRealtime(currentPath.value)
  breadcrumbResizeObserver = new ResizeObserver(scrollBreadcrumbToEnd)
  if (breadcrumbScroll.value) breadcrumbResizeObserver.observe(breadcrumbScroll.value)
  window.addEventListener('keydown', onGlobalKeyDown)
  window.addEventListener('popstate', applyHashPath)
  window.addEventListener('hashchange', applyHashPath)
  const initialFolderLoad = refresh(true)
  await Promise.all([loadPreferences(), loadFavourites(), loadOperations()])
  restorePasswordPrompt()
  if (!await initialFolderLoad && currentPath.value !== '/') {
    clearSessionPath()
    navigateTo('/', true)
  } else {
    const file = readHashFile()
    writePathHash(currentPath.value, true, file)
    if (file) await restoreHashFile(file)
  }
})

watch(currentPath, path => {
  clearSelection()
  clearFocus(path)
  search.value = ''
  setRealtimeDirectory(path)
  void refresh(true)
}, { flush: 'post' })

watch([currentPath, trashMode], () => { void scrollBreadcrumbToEnd() }, { flush: 'post' })

watch([viewMode, gridSize], () => {
  void savePreferences()
  void nextTick(scrollFocusedIntoView)
})

watch(showHidden, () => { void savePreferences() })

watch([selectedPaths, focusedPath, detailsVisible, detailsPath], storeSessionExplorerState, { flush: 'sync' })

onBeforeUnmount(() => {
  if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
  window.removeEventListener('keydown', onGlobalKeyDown)
  window.removeEventListener('popstate', applyHashPath)
  window.removeEventListener('hashchange', applyHashPath)
  breadcrumbResizeObserver?.disconnect()
})

async function scrollBreadcrumbToEnd() {
  await nextTick()
  if (breadcrumbScroll.value) breadcrumbScroll.value.scrollLeft = breadcrumbScroll.value.scrollWidth
}

async function loadPreferences() {
  try {
    const prefs = await api.preferences()
    if (prefs.view_mode === 'grid' || prefs.view_mode === 'list') viewMode.value = prefs.view_mode
    if (prefs.grid_size === 'small' || prefs.grid_size === 'medium' || prefs.grid_size === 'large') gridSize.value = prefs.grid_size
    showHidden.value = !!prefs.show_hidden
    if (prefs.theme === 'system' || prefs.theme === 'dark' || prefs.theme === 'light') setTheme(prefs.theme)
  } catch {
    // Preferences are non-critical; browsing should still work.
  }
}

async function savePreferences() {
  try {
    await api.savePreferences({ view_mode: viewMode.value, grid_size: gridSize.value, show_hidden: showHidden.value, theme: themeMode.value })
  } catch {
    // Do not interrupt file work for a preference persistence failure.
  }
}

function chooseGridSize(size: GridSize) {
  gridSize.value = size
  viewMode.value = 'grid'
}

const gridSizeItems = computed(() => ([
  { label: 'Small tiles', icon: gridSize.value === 'small' ? 'pi pi-check' : undefined, command: () => chooseGridSize('small') },
  { label: 'Medium tiles', icon: gridSize.value === 'medium' ? 'pi pi-check' : undefined, command: () => chooseGridSize('medium') },
  { label: 'Large tiles', icon: gridSize.value === 'large' ? 'pi pi-check' : undefined, command: () => chooseGridSize('large') },
]))

async function loadFavourites() {
  try {
    favourites.value = (await api.favourites()).favourites
  } catch {
    favourites.value = []
  }
}

async function toggleFavourite() {
  if (currentPath.value === '/') return
  try {
    if (currentFavourite.value) await api.removeFavourite(currentPath.value)
    else await api.addFavourite(currentPath.value)
    await loadFavourites()
  } catch (err) {
    showError(err, 'Could not update favourite')
  }
}

function applyHashPath() {
  void applyHashLocation()
}

async function applyHashLocation() {
  const request = ++hashFileRequest
  const path = readHashPath()
  if (path && path !== currentPath.value) {
    trashMode.value = false
    currentPath.value = path
  }
  const file = readHashFile()
  if (!file) {
    previewVisible.value = false
    editorVisible.value = false
    return
  }
  await restoreHashFile(file, request)
}

function storeSessionExplorerState() {
  storeExplorerState({
    directory: currentPath.value,
    selectedPaths: [...selectedPaths.value],
    focusedPath: focusedPath.value,
    detailsVisible: detailsVisible.value,
    detailsPath: detailsPath.value,
  })
}

async function refresh(showSpinner: boolean): Promise<boolean> {
  const requestedPath = currentPath.value
  if (showSpinner) loading.value = true
  error.value = ''
  try {
    const result = await api.list(requestedPath)
    if (requestedPath !== currentPath.value || trashMode.value) return false
    entries.value = result.entries
    const existing = new Set(result.entries.map(entry => entry.path))
    selectedPaths.value = new Set([...selectedPaths.value].filter(path => existing.has(path)))
    if (focusedPath.value && !existing.has(focusedPath.value)) clearFocus(requestedPath)
    if (detailsVisible.value && detailsPath.value !== requestedPath && !existing.has(detailsPath.value)) detailsPath.value = requestedPath
    storeSessionPath(requestedPath)
    return true
  } catch (err) {
    if (requestedPath === currentPath.value && !trashMode.value) error.value = messageOf(err)
    return false
  } finally {
    if (showSpinner) loading.value = false
  }
}

async function loadTrash(showSpinner: boolean) {
  if (showSpinner) loading.value = true
  error.value = ''
  try {
    trashItems.value = (await api.trash()).items
    const existing = new Set(trashEntries.value.map(entry => entry.path))
    selectedPaths.value = new Set([...selectedPaths.value].filter(path => existing.has(path)))
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    if (showSpinner) loading.value = false
  }
}

function refreshCurrent(showSpinner: boolean) {
  return trashMode.value ? loadTrash(showSpinner) : refresh(showSpinner)
}

async function toggleRecycleBin() {
  if (trashMode.value) {
    trashMode.value = false
    search.value = ''
    filterVisible.value = false
    clearSelection()
    clearFocus(recycleReturnPath.value)
    if (currentPath.value === recycleReturnPath.value) await refresh(true)
    else navigateTo(recycleReturnPath.value)
    return
  }
  recycleReturnPath.value = currentPath.value
  trashMode.value = true
  search.value = ''
  filterVisible.value = false
  detailsVisible.value = false
  clearSelection()
  clearFocus(currentPath.value)
  await loadTrash(true)
}

function navigateTo(path: string, replaceHistory = false) {
  trashMode.value = false
  if (path === currentPath.value) return
  currentPath.value = path
  writePathHash(path, replaceHistory)
}

function onOperationTerminal(operation: FileOperation) {
  if (operation.state === 'completed') {
    if (operation.kind === 'trash' && trashMode.value) void loadTrash(false)
    toast.add({ severity: 'success', summary: `${operationLabel(operation.kind)} complete`, detail: operation.message || undefined, life: 2500 })
  } else if (operation.state === 'failed') {
    toast.add({ severity: 'error', summary: `${operationLabel(operation.kind)} failed`, detail: operation.error, life: 6000 })
  }
  if (!trashMode.value) void refresh(false)
}

async function openEntry(entry: FileEntry) {
  if (trashMode.value) return
  if (entry.is_dir) {
    navigateTo(entry.path)
    return
  }
  await openFile(entry.path, true)
}

async function openFile(path: string, addHistory: boolean, request?: number) {
	const detected = await detectPreview(path)
	if (request !== undefined && request !== hashFileRequest) return
	if (addHistory) writePathHash(currentPath.value, false, path)
	if (detected.kind !== 'text') {
		editorVisible.value = false
		showDetectedPreview(path, detected)
		return
	}
  previewVisible.value = false
  editorPath.value = path
  editorIsNew.value = false
  editorVisible.value = true
}

async function restoreHashFile(path: string, request = ++hashFileRequest) {
  await openFile(path, false, request)
}

async function navigatePreview(path: string) {
	await navigateDetectedPreview(path)
	writePathHash(currentPath.value, true, path)
}

function setPreviewVisible(visible: boolean) {
  if (visible) {
    previewVisible.value = true
    return
  }
  if (previewVisible.value && readHashFile()) {
    window.history.back()
    return
  }
  hidePreview()
}

function returnPreviewToFolder() {
  hidePreview()
  writePathHash(currentPath.value, true)
}

function handleSelect(entry: FileEntry, event: MouseEvent) {
  const index = filteredEntries.value.findIndex(item => item.path === entry.path)
  const anchorIndex = filteredEntries.value.findIndex(item => item.path === anchorPath.value)

  if (event.shiftKey && anchorIndex >= 0 && index >= 0) {
    const [start, end] = anchorIndex <= index ? [anchorIndex, index] : [index, anchorIndex]
    const range = filteredEntries.value.slice(start, end + 1).map(item => item.path)
    const next = event.ctrlKey || event.metaKey ? new Set(selectedPaths.value) : new Set<string>()
    for (const path of range) next.add(path)
    selectedPaths.value = next
  } else {
    toggleSelection(entry.path)
    anchorPath.value = entry.path
  }
  fileViewHost.value?.focus({ preventScroll: true })
}

function handleFocus(entry: FileEntry, event: MouseEvent) {
  const previousFocus = focusedPath.value
  focusedPath.value = entry.path
  if (detailsVisible.value) detailsPath.value = entry.path
  const index = filteredEntries.value.findIndex(item => item.path === entry.path)
  const anchor = anchorPath.value || previousFocus || entry.path
  const anchorIndex = filteredEntries.value.findIndex(item => item.path === anchor)

  if (event.shiftKey && anchorIndex >= 0 && index >= 0) {
    const [start, end] = anchorIndex <= index ? [anchorIndex, index] : [index, anchorIndex]
    const range = filteredEntries.value.slice(start, end + 1).map(item => item.path)
    const next = event.ctrlKey || event.metaKey ? new Set(selectedPaths.value) : new Set<string>()
    for (const path of range) next.add(path)
    selectedPaths.value = next
    if (!anchorPath.value) anchorPath.value = anchor
  } else if (event.ctrlKey || event.metaKey) {
    toggleSelection(entry.path)
    anchorPath.value = entry.path
  } else {
    selectedPaths.value = new Set([entry.path])
    anchorPath.value = entry.path
  }
  fileViewHost.value?.focus({ preventScroll: true })
}

function openContext(entry: FileEntry, event: MouseEvent | PointerEvent) {
  const isTouch = 'pointerType' in event && (event.pointerType === 'touch' || event.pointerType === 'pen')
  if (!isTouch) {
    focusedPath.value = entry.path
    anchorPath.value = entry.path
  }
  if (!isTouch && detailsVisible.value) detailsPath.value = entry.path
  contextEntry.value = entry
  contextMenu.value?.show(event)
}

function onContextMenuHide() {
  contextEntry.value = null
}

function clearSelection() {
  clearSelectionState()
}

function clearFocus(fallbackPath = currentPath.value) {
  focusedPath.value = ''
  if (detailsVisible.value) detailsPath.value = fallbackPath
}

function selectAll() {
  replaceSelection(filteredEntries.value.map(entry => entry.path))
  if (filteredEntries.value.length) {
    anchorPath.value = filteredEntries.value[0].path
  }
}

function openRename(entry: FileEntry | null = selectedOne.value) {
  if (!entry || currentPath.value === '/') return
  renameTarget.value = entry
  nameDialogMode.value = 'rename'
  nameDialogVisible.value = true
}

function toggleDetails(entry: FileEntry | null = selectedOne.value) {
  if (detailsVisible.value && (!entry || detailsPath.value === entry.path)) {
    detailsVisible.value = false
    return
  }
  if (!entry) return
  detailsPath.value = entry.path
  detailsVisible.value = true
}

function applyCachedFolderSize(value: { path: string; size: number }) {
  entries.value = entries.value.map(entry => entry.path === value.path ? { ...entry, size: value.size, size_status: 'ready' } : entry)
}

function openNewFolder() {
  nameDialogMode.value = 'folder'
  nameDialogVisible.value = true
}

async function onNameDialogSubmit(value: string) {
  try {
    let createdPath = ''
    if (nameDialogMode.value === 'folder') {
      createdPath = joinPath(currentPath.value, value)
      await api.mkdir(createdPath)
    } else if (renameTarget.value) {
      await api.rename(renameTarget.value.path, joinPath(currentPath.value, value))
    }
    await refresh(false)
    if (createdPath) {
      focusedPath.value = createdPath
      if (detailsVisible.value) detailsPath.value = createdPath
      await nextTick()
      const index = filteredEntries.value.findIndex(entry => entry.path === createdPath)
      if (index >= 0) scrollIndexToTopUnlessAlreadyAtTop(index)
    }
  } catch (err) {
    showError(err)
  }
}

function createDocument(name: string) {
  editorPath.value = joinPath(currentPath.value, name)
  editorIsNew.value = true
  editorVisible.value = true
}

function setEditorVisible(visible: boolean) {
  if (visible) {
    editorVisible.value = true
    return
  }
  if (editorVisible.value && readHashFile()) {
    window.history.back()
    return
  }
  editorVisible.value = false
}

function onEditorSaved(path: string) {
  editorIsNew.value = false
  if (readHashFile() !== path) writePathHash(currentPath.value, false, path)
  void refresh(false)
}

function openTransfer(mode: TransferMode, items: FileEntry[] = selectedEntries.value) {
  if (items.length === 0 || currentPath.value === '/') return
  transferMode.value = mode
  transferEntries.value = [...items]
  destinationVisible.value = true
}

async function submitTransfer(destination: string) {
  await beginOperation(transferMode.value, transferEntries.value.map(entry => entry.path), destination)
}

function deleteEntries(items: FileEntry[] = selectedEntries.value) {
  if (items.length === 0 || currentPath.value === '/') return
  const description = items.length === 1 ? `“${items[0].name}”` : `${items.length} items`
  confirm.require({
    header: 'Delete',
    message: `Move ${description} to the Recycle Bin?`,
    icon: 'pi pi-trash',
    rejectLabel: 'Cancel',
    acceptLabel: 'Delete',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      await beginOperation('trash', items.map(item => item.path))
      clearSelection()
    },
  })
}

async function restoreSelectedTrash() {
  await restoreTrash(selectedEntries.value)
}

async function restoreTrash(items: FileEntry[]) {
  const ids = items.map(entry => entry.path.replace(/^recycle:/, ''))
  try {
    for (const id of ids) await api.restoreTrash(id)
    clearSelection()
    await Promise.all([loadTrash(false), refresh(false)])
    toast.add({ severity: 'success', summary: ids.length === 1 ? 'Item restored' : `${ids.length} items restored`, life: 2500 })
  } catch (err) {
    showError(err, 'Could not restore item')
  }
}

function purgeSelectedTrash() {
  purgeTrash(selectedEntries.value)
}

function purgeTrash(items: FileEntry[]) {
  if (!items.length) return
  const description = items.length === 1 ? `“${items[0].name}”` : `${items.length} items`
  confirm.require({
    header: 'Delete permanently',
    message: `Permanently delete ${description}? This cannot be undone.`,
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Cancel',
    acceptLabel: 'Delete permanently',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      try {
        for (const item of items) await api.purgeTrash(item.path.replace(/^recycle:/, ''))
        clearSelection()
        await loadTrash(false)
      } catch (err) {
        showError(err, 'Could not permanently delete item')
      }
    },
  })
}

async function beginOperation(kind: 'copy' | 'move' | 'trash' | 'extract', sources: string[], destination?: string) {
  try {
    await startFileOperation(kind, sources, destination)
    operationsVisible.value = true
  } catch (err) {
    showError(err, `Could not start ${kind}`)
  }
}

function cancelOperation(id: string) {
  if (!cancelRealtimeOperation(id)) {
    void api.cancelOperation(id).catch(err => showError(err, 'Could not cancel operation'))
  }
}

function clearOperationHistory() {
  confirm.require({
    header: 'Clear task history',
    message: 'Clear all completed, failed, cancelled and interrupted tasks? Active tasks will be kept.',
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Cancel',
    acceptLabel: 'Clear history',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      try {
        const removed = await clearFinishedOperations()
        toast.add({ severity: 'success', summary: removed === 1 ? '1 task removed' : `${removed} tasks removed`, life: 2500 })
      } catch (err) {
        showError(err, 'Could not clear task history')
      }
    },
  })
}

function removeOperationHistory(operation: FileOperation) {
  confirm.require({
    header: 'Remove task from history',
    message: 'Remove this task from history? This does not affect any files.',
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Cancel',
    acceptLabel: 'Remove',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      try {
        await removeFinishedOperation(operation.id)
      } catch (err) {
        showError(err, 'Could not remove task from history')
      }
    },
  })
}

function download(item: FileEntry) {
  const anchor = document.createElement('a')
  anchor.href = api.downloadUrl(item.path)
  anchor.download = item.is_dir ? `${item.name}.zip` : item.name
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}

async function prepareExtraction(destination: string) {
  if (!extractTarget.value) return
  extractVisible.value = false
  extractDestination.value = destination
  extractBusy.value = true
  try {
    const operation = await api.extract(extractTarget.value.path, extractDestination.value)
    handleOperationUpdate(operation)
    extractVisible.value = false
  } catch (err) {
    showError(err, 'Could not start extraction')
  } finally {
    extractBusy.value = false
  }
}

async function submitExtractionPassword(password: string) {
  if (!extractPasswordOperationId.value) return
  extractBusy.value = true
  try {
    await api.submitOperationPassword(extractPasswordOperationId.value, password)
  } catch (err) {
    showError(err, 'Could not submit archive password')
  } finally {
    extractBusy.value = false
  }
}

function handleOperationUpdate(operation: FileOperation) {
  updateOperation(operation)
  if (operation.kind === 'extract' && operation.phase === 'awaiting_password') {
    showPasswordPrompt(operation)
  } else if (operation.id === extractPasswordOperationId.value && operation.phase !== 'awaiting_password') {
    extractPasswordVisible.value = false
    extractPasswordError.value = ''
  }
}

function restorePasswordPrompt() {
  const waiting = operations.value.find(operation => operation.kind === 'extract' && operation.phase === 'awaiting_password')
  if (waiting) showPasswordPrompt(waiting)
}

function showPasswordPrompt(operation: FileOperation) {
  extractPasswordOperationId.value = operation.id
  extractPasswordError.value = operation.message?.startsWith('Incorrect') ? operation.message : ''
  extractTarget.value = {
    name: operation.sources?.[0]?.split('/').pop() ?? 'archive',
    path: operation.sources?.[0] ?? '',
    is_dir: false,
    size: 0,
    modified: '',
    mode: '',
  }
  extractPasswordVisible.value = true
}

async function cancelPasswordPrompt() {
  const id = extractPasswordOperationId.value
  extractPasswordVisible.value = false
  extractPasswordError.value = ''
  if (id) await cancelOperation(id)
}

function openExtract(entry: FileEntry) {
  extractTarget.value = entry
  extractDestination.value = joinPath(directoryOf(entry.path), archiveNameWithoutExtension(entry.name))
  extractVisible.value = true
}

function extractHere(entry: FileEntry) {
  extractTarget.value = entry
  void prepareExtraction(directoryOf(entry.path))
}

function chooseUpload() {
  uploadInput.value?.click()
}

async function uploadFiles(event: Event) {
  const input = event.target as HTMLInputElement
  const files = [...(input.files ?? [])]
  input.value = ''
  await uploadFileList(files, currentPath.value)
}

async function uploadFileList(files: File[], destination: string) {
  for (const file of files) {
    try {
      operationsVisible.value = true
      const operation = await api.upload(destination, file, updateOperation)
      updateOperation(operation)
    } catch (err) {
      showError(err, `Could not upload ${file.name}`)
    }
  }
  await refresh(false)
}

function dropFilesOnFolder(folder: FileEntry, event: DragEvent) {
  if (!canMutate.value) return
  draggingOver.value = false
  const files = [...(event.dataTransfer?.files ?? [])]
  if (files.length) void uploadFileList(files, folder.path)
}

function onViewDragOver(event: DragEvent) {
  if (!canMutate.value) return
  const hasFiles = [...(event.dataTransfer?.types ?? [])].includes('Files')
  draggingOver.value = hasFiles
  if (!hasFiles) return
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
}

function onViewDragLeave(event: DragEvent) {
  const host = fileViewHost.value
  if (!host) return
  const next = event.relatedTarget as Node | null
  if (!next || !host.contains(next)) draggingOver.value = false
}

function onBackgroundDrop(event: DragEvent) {
  draggingOver.value = false
  if (!canMutate.value) return
  const files = [...(event.dataTransfer?.files ?? [])]
  if (files.length) {
    event.preventDefault()
    void uploadFileList(files, currentPath.value)
  }
}

function goUp() {
  if (trashMode.value) {
    navigateTo('/')
    return
  }
  if (parentPath.value) navigateTo(parentPath.value)
}

function openFilter() {
  filterVisible.value = true
  void nextTick(() => filterInput.value?.$el?.focus())
}

function closeFilter() {
  search.value = ''
  filterVisible.value = false
  fileViewHost.value?.focus({ preventScroll: true })
}

function toggleFilter() {
  if (filterVisible.value) closeFilter()
  else openFilter()
}

function toggleDetailsPanel() {
  if (detailsVisible.value) {
    detailsVisible.value = false
    return
  }
  const focused = filteredEntries.value.find(entry => entry.path === focusedPath.value) ?? null
  detailsPath.value = focused?.path ?? currentPath.value
  detailsVisible.value = true
}

function handleDetailsShortcut(event: KeyboardEvent): boolean {
  if (!event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.key !== 'Enter') return false
  const target = event.target
  if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target instanceof HTMLElement && target.isContentEditable) return false
  if (editorVisible.value || previewVisible.value || newDocumentVisible.value || nameDialogVisible.value || extractVisible.value || extractPasswordVisible.value || destinationVisible.value) return false
  event.preventDefault()
  toggleDetailsPanel()
  return true
}

function onGlobalKeyDown(event: KeyboardEvent) {
  if (handleDetailsShortcut(event)) return
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') {
    event.preventDefault()
    openFilter()
  }
}

function selectTheme(mode: ThemeMode) {
  setTheme(mode)
  themeMenu.value?.hide()
  void savePreferences()
}

function showThemeMenu(event: Event) {
  themeMenu.value?.toggle(event)
}

function showSettingsPlaceholder() {
  toast.add({ severity: 'info', summary: 'Settings', detail: 'Settings will be available here later.', life: 2500 })
}

function onKeyDown(event: KeyboardEvent) {
  if (handleDetailsShortcut(event)) {
    event.stopPropagation()
    return
  }
  if (event.target instanceof HTMLButtonElement || event.target instanceof HTMLInputElement) return
  if (event.key.toLowerCase() === 'a' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    selectAll()
    return
  }
  if (event.key === 'Escape') {
    clearSelection()
    return
  }
  if (event.key === 'Delete' || event.key === 'Backspace' && event.metaKey) {
    if (selectedEntries.value.length) {
      event.preventDefault()
      if (trashMode.value) purgeSelectedTrash()
      else deleteEntries()
    }
    return
  }
  if (event.key === 'F2') {
    if (trashMode.value) return
    event.preventDefault()
    openRename()
    return
  }
  if (event.key === 'Enter') {
    const focused = filteredEntries.value.find(entry => entry.path === focusedPath.value)
    if (focused) {
      event.preventDefault()
      openEntry(focused)
    }
    return
  }
  const arrows = ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight']
  if (!arrows.includes(event.key) || filteredEntries.value.length === 0) return
  event.preventDefault()
  const current = filteredEntries.value.findIndex(entry => entry.path === focusedPath.value)
  const columns = viewMode.value === 'grid' ? Math.max(1, gridColumns.value) : 1
  let next = current < 0 ? 0 : current
  if (current >= 0) {
    if (event.key === 'ArrowUp') next -= columns
    else if (event.key === 'ArrowDown') next += columns
    else if (event.key === 'ArrowLeft') next -= 1
    else if (event.key === 'ArrowRight') next += 1
  }
  next = Math.max(0, Math.min(filteredEntries.value.length - 1, next))
  const entry = filteredEntries.value[next]
  focusedPath.value = entry.path
  if (detailsVisible.value) detailsPath.value = entry.path

  void nextTick(() => scrollIndexIntoView(next))
}

function scrollFocusedIntoView() {
  const index = filteredEntries.value.findIndex(entry => entry.path === focusedPath.value)
  if (index >= 0) scrollIndexIntoView(index)
}

function scrollIndexIntoView(index: number) {
  if (viewMode.value === 'list') listView.value?.scrollToIndex(index)
  else gridView.value?.scrollToIndex(index)
}

function scrollIndexToTopUnlessAlreadyAtTop(index: number) {
  if (viewMode.value === 'list') listView.value?.scrollToIndexAtTop(index)
  else gridView.value?.scrollToIndexAtTop(index)
}

function isArchive(entry: FileEntry) {
  if (entry.is_dir) return false
  const name = entry.name.toLowerCase()
  return archiveExtensions.some(ext => name.endsWith(ext))
}

const archiveExtensions = ['.tar.bz2', '.tar.gz', '.tar.xz', '.zip', '.7z', '.rar', '.tar', '.tgz']

function archiveNameWithoutExtension(name: string): string {
  const lower = name.toLowerCase()
  const extension = archiveExtensions.find(candidate => lower.endsWith(candidate))
  return extension ? name.slice(0, -extension.length) : name
}

function joinPath(base: string, name: string): string {
  return `${base === '/' ? '' : base}/${name}`.replace(/\/+/g, '/') || '/'
}

function directoryOf(path: string): string {
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.length ? `/${parts.join('/')}` : '/'
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function showError(err: unknown, summary = 'Operation failed') {
  toast.add({ severity: 'error', summary, detail: messageOf(err), life: 5000 })
}

function capitalize(value: string) {
  return value ? value[0].toUpperCase() + value.slice(1) : value
}

function operationLabel(value: string) {
  return value === 'trash' ? 'Delete' : capitalize(value)
}
</script>

<template>
  <section class="relative flex h-full min-h-0 flex-col bg-zinc-950">
    <header class="border-b border-zinc-800 bg-zinc-900/90 px-3 py-2 backdrop-blur">
      <div class="flex flex-wrap items-center gap-1">
        <div class="flex items-center gap-1">
          <Button v-tooltip.bottom="'New folder'" icon="pi pi-folder-plus" size="small" severity="secondary" text :disabled="!canMutate" aria-label="New folder" @click="openNewFolder" />
          <Button v-tooltip.bottom="'New file'" icon="pi pi-file-plus" size="small" severity="secondary" text :disabled="!canMutate" aria-label="New file" @click="newDocumentVisible = true" />
          <Button v-tooltip.bottom="'Upload'" icon="pi pi-upload" size="small" severity="secondary" text :disabled="!canMutate" aria-label="Upload" @click="chooseUpload" />
        </div>
        <span class="mx-1 h-6 w-px bg-zinc-700" />
        <div class="flex items-center gap-1">
          <Button v-tooltip.bottom="favouritesVisible ? 'Close favourites' : 'Open favourites'" icon="pi pi-star" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': favouritesVisible }" :aria-label="favouritesVisible ? 'Close favourites' : 'Open favourites'" :aria-pressed="favouritesVisible" @click="favouritesVisible = !favouritesVisible" />
          <Button v-tooltip.bottom="trashMode ? 'Close Recycle Bin' : 'Open Recycle Bin'" icon="pi pi-trash" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': trashMode }" :aria-label="trashMode ? 'Close Recycle Bin' : 'Open Recycle Bin'" :aria-pressed="trashMode" @click="toggleRecycleBin" />
        </div>

        <template v-if="selectedEntries.length || !trashMode || detailsVisible">
          <span class="mx-1 h-6 w-px bg-zinc-700" />
          <div class="flex items-center gap-1">
            <template v-if="trashMode && selectedEntries.length">
              <Button v-tooltip.bottom="'Restore'" icon="pi pi-replay" size="small" severity="secondary" text aria-label="Restore" @click="restoreSelectedTrash" />
              <Button v-tooltip.bottom="'Delete permanently'" icon="pi pi-times" size="small" severity="danger" text aria-label="Delete permanently" @click="purgeSelectedTrash" />
            </template>
            <template v-else>
              <Button v-if="selectedEntries.length && !selectionContainsConfiguredRoot" v-tooltip.bottom="'Copy'" icon="pi pi-copy" size="small" text aria-label="Copy" @click="openTransfer('copy')" />
              <Button v-if="selectedEntries.length && !selectionContainsConfiguredRoot" v-tooltip.bottom="'Move'" icon="pi pi-arrow-right" size="small" text aria-label="Move" @click="openTransfer('move')" />
              <Button v-if="selectedEntries.length && !selectionContainsConfiguredRoot" v-tooltip.bottom="'Delete'" icon="pi pi-trash" size="small" severity="danger" text aria-label="Delete" @click="deleteEntries()" />
              <Button v-if="!trashMode" v-tooltip.bottom="detailsVisible ? 'Close details' : 'Open details'" icon="pi pi-info-circle" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': detailsVisible }" :aria-label="detailsVisible ? 'Close details' : 'Open details'" :aria-pressed="detailsVisible" @click="toggleDetailsPanel" />
            </template>
          </div>
        </template>

        <span class="min-w-2 flex-1" />
        <div class="flex items-center gap-1">
          <Button v-tooltip.bottom="operationsVisible ? 'Close tasks' : 'Open tasks'" icon="pi pi-list-check" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': operationsVisible }" :badge="activeOperations.length ? String(activeOperations.length) : undefined" :aria-label="operationsVisible ? 'Close tasks' : 'Open tasks'" :aria-pressed="operationsVisible" @click="operationsVisible = !operationsVisible" />
          <Button v-tooltip.bottom="'Settings'" icon="pi pi-cog" size="small" severity="secondary" text aria-label="Settings" @click="showSettingsPlaceholder" />
        </div>
        <span class="mx-1 h-6 w-px bg-zinc-700" />
        <Button v-tooltip.bottom="`Theme: ${themeMode}`" :icon="themeMode === 'system' ? 'pi pi-desktop' : themeMode === 'dark' ? 'pi pi-moon' : 'pi pi-sun'" size="small" severity="secondary" text aria-label="Choose theme" @click="showThemeMenu" />
        <Menu ref="themeMenu" popup :model="themeItems">
          <template #item="{ item, props }">
            <a v-bind="props.action" @click="selectTheme(item.mode)">
              <span :class="item.icon" />
              <span class="ml-2">{{ item.label }}</span>
            </a>
          </template>
        </Menu>
        <span v-if="props.username || props.showLogout" class="mx-1 h-6 w-px bg-zinc-700" />
        <span class="hidden items-center gap-1 text-xs text-zinc-500 sm:flex">
          <i class="pi pi-circle-fill text-[0.45rem]" :class="watchConnected ? 'text-emerald-500' : 'text-zinc-600'" /> live
        </span>
        <span v-if="props.username" class="hidden text-xs text-zinc-400 sm:inline">{{ props.username }}</span>
        <Button v-if="props.showLogout" v-tooltip.bottom="'Sign out'" icon="pi pi-sign-out" severity="secondary" text rounded aria-label="Sign out" @click="emit('logout')" />
        <input ref="uploadInput" class="hidden" type="file" multiple @change="uploadFiles" />
      </div>
    </header>

    <div v-if="error" class="border-b border-red-950 bg-red-950/50 px-4 py-3 text-sm text-red-200">{{ error }}</div>

    <div class="flex shrink-0 items-center gap-2 border-b border-zinc-800 bg-zinc-950 px-3 py-2">
      <nav ref="breadcrumbScroll" class="min-w-0 flex-1 overflow-x-auto" aria-label="Breadcrumb" data-breadcrumb-scroll>
        <div class="flex min-w-max items-center gap-1 text-sm">
          <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
            <i v-if="index" class="pi pi-angle-right text-xs text-zinc-600" />
            <button class="rounded px-2 py-1 text-zinc-300 hover:bg-zinc-800 hover:text-white" :aria-current="index === breadcrumbs.length - 1 ? 'page' : undefined" @click="crumb.path !== 'recycle:' && navigateTo(crumb.path)">
              {{ crumb.name }}
            </button>
          </template>
        </div>
      </nav>
      <div class="flex shrink-0 items-center gap-1 text-xs text-zinc-400" data-selection-toolbar>
        <template v-if="selectionControlsVisible || selectedEntries.length">
          <span class="ml-1 min-w-[4.5rem] text-right">{{ selectedEntries.length }} selected</span>
          <Button v-tooltip.bottom="'Select all'" icon="pi pi-list-check" size="small" severity="secondary" text rounded aria-label="Select all" :disabled="filteredEntries.length === 0 || selectedEntries.length === filteredEntries.length" @click="selectAll" />
          <Button v-tooltip.bottom="'Clear selection'" icon="pi pi-times" size="small" severity="secondary" text rounded aria-label="Clear selection" :disabled="selectedEntries.length === 0" @click="clearSelection" />
        </template>
        <Button
          v-tooltip.bottom="selectionControlsVisible ? 'Hide selection controls' : 'Show selection controls'"
          icon="pi pi-check-square"
          size="small"
          severity="secondary"
          text
          class="webfile-toggle"
          :class="{ 'webfile-toggle-active': selectionControlsVisible }"
          :aria-label="selectionControlsVisible ? 'Hide selection controls' : 'Show selection controls'"
          :aria-pressed="selectionControlsVisible"
          @click="selectionControlsVisible = !selectionControlsVisible"
        />
      </div>
      <span class="h-6 w-px bg-zinc-800" />
      <Button v-tooltip.bottom="'Refresh'" icon="pi pi-refresh" size="small" severity="secondary" text aria-label="Refresh" @click="refreshCurrent(true)" />
      <span class="h-6 w-px bg-zinc-800" />
      <div v-if="filterVisible" class="w-64">
        <InputText ref="filterInput" v-model="search" size="small" fluid placeholder="Filter this folder" aria-label="Filter this folder" @keydown.escape="closeFilter" />
      </div>
      <Button v-tooltip.bottom="filterVisible ? 'Close filter' : 'Filter (Ctrl+F)'" icon="pi pi-search" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': filterVisible }" :aria-label="filterVisible ? 'Close filter' : 'Open filter'" :aria-pressed="filterVisible" @click="toggleFilter" />
      <span class="h-6 w-px bg-zinc-800" />
      <div class="flex items-center gap-1">
        <span class="inline-flex overflow-hidden rounded-md border border-zinc-700">
          <button v-tooltip.bottom="'List view'" class="px-2.5 py-1.5" :class="viewMode === 'list' ? 'bg-zinc-700 text-white' : 'text-zinc-400'" aria-label="List view" @click="viewMode = 'list'"><i class="pi pi-list" /></button>
          <SplitButton
            v-tooltip.bottom="'Grid view and tile size'"
            :model="gridSizeItems"
            icon="pi pi-th-large"
            size="small"
            text
            class="webfile-grid-split"
            :class="{ 'webfile-grid-split-active': viewMode === 'grid' }"
            :button-props="{ 'aria-label': 'Grid view', 'aria-pressed': viewMode === 'grid' }"
            :menu-button-props="{ 'aria-label': 'Grid size' }"
            @click="viewMode = 'grid'"
          />
        </span>
        <Button v-if="!trashMode" v-tooltip.bottom="showHidden ? 'Hide dot files' : 'Show dot files'" icon="pi pi-eye" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': showHidden }" :aria-label="showHidden ? 'Hide dot files' : 'Show dot files'" :aria-pressed="showHidden" @click="showHidden = !showHidden" />
      </div>
    </div>

    <div class="flex min-h-0 flex-1">
      <aside v-if="favouritesVisible" class="flex h-full w-72 shrink-0 flex-col border-r border-zinc-800 bg-zinc-900 text-zinc-100 shadow-2xl">
        <header class="flex h-12 shrink-0 items-center justify-between border-b border-zinc-800 px-4">
          <h2 class="font-semibold">Favourites</h2>
          <div class="flex items-center gap-1">
            <Button v-tooltip.bottom="currentFavourite ? 'Remove current folder' : 'Add current folder'" :icon="currentFavourite ? 'pi pi-star-fill' : 'pi pi-star'" severity="secondary" text rounded :disabled="currentPath === '/' || trashMode" :aria-label="currentFavourite ? 'Remove current folder' : 'Add current folder'" @click="toggleFavourite" />
            <Button v-tooltip.bottom="'Close favourites'" icon="pi pi-times" severity="secondary" text rounded aria-label="Close favourites" @click="favouritesVisible = false" />
          </div>
        </header>
        <div v-if="favourites.length" class="min-h-0 flex-1 space-y-1 overflow-auto p-2">
          <button
            v-for="favourite in favourites"
            :key="favourite.path"
            class="flex w-full items-center gap-3 rounded px-3 py-2 text-left text-sm text-zinc-300 hover:bg-zinc-800 hover:text-white"
            :title="favourite.path"
            @click="navigateTo(favourite.path)"
          >
            <i class="pi pi-folder text-amber-400" />
            <span class="min-w-0 flex-1 truncate">{{ favourite.label || favourite.path.split('/').filter(Boolean).at(-1) || favourite.path }}</span>
          </button>
        </div>
        <div v-else class="grid min-h-0 flex-1 place-items-center p-6 text-center text-sm text-zinc-500">No favourite folders yet.</div>
      </aside>
      <div
        ref="fileViewHost"
        class="relative min-h-0 min-w-0 flex-1 outline-none"
        tabindex="0"
        @keydown="onKeyDown"
        @dragover="onViewDragOver"
        @dragleave="onViewDragLeave"
        @drop="onBackgroundDrop"
      >
        <div v-if="draggingOver" class="pointer-events-none absolute inset-3 z-30 grid place-items-center rounded-xl border-2 border-dashed border-sky-500 bg-sky-950/40 text-sm font-medium text-sky-200">
          Drop files to upload
        </div>

        <div v-if="loading" class="grid h-full place-items-center"><ProgressSpinner class="h-10 w-10" /></div>
        <div v-else-if="search && filteredEntries.length === 0" class="grid h-full place-items-center p-8 text-center text-zinc-500">
          <div><i class="pi pi-search mb-3 text-4xl" /><p>No matching files</p></div>
        </div>
        <div v-else class="flex h-full min-h-0 flex-col">
          <div v-if="viewMode === 'list'" class="grid min-w-[46rem] shrink-0 grid-cols-[minmax(18rem,1fr)_8rem_13rem_8rem] border-b border-zinc-800 bg-zinc-950 px-3 py-2 text-xs font-medium uppercase tracking-wide text-zinc-500">
            <span>Name</span><span>Size</span><span>Modified</span><span>Mode</span>
          </div>
          <VirtualFileList
            v-if="viewMode === 'list'"
            ref="listView"
            class="min-h-0 flex-1"
            :entries="filteredEntries"
            :selected-paths="[...selectedPaths]"
            :focused-path="focusedPath"
            :selection-controls-visible="selectionControlsVisible"
            :parent-path="displayedParentPath"
            @select="handleSelect"
            @focus="handleFocus"
            @activate="openEntry"
            @parent="goUp"
            @context="openContext"
            @dropfiles="dropFilesOnFolder"
          />
          <VirtualFileGrid
            v-else
            ref="gridView"
            class="min-h-0 flex-1"
            :entries="filteredEntries"
            :selected-paths="[...selectedPaths]"
            :focused-path="focusedPath"
            :selection-controls-visible="selectionControlsVisible"
            :size="gridSize"
            :parent-path="displayedParentPath"
            @select="handleSelect"
            @focus="handleFocus"
            @activate="openEntry"
            @parent="goUp"
            @context="openContext"
            @dropfiles="dropFilesOnFolder"
            @columns-change="gridColumns = $event"
          />
        </div>
      </div>
      <DetailsDialog v-if="!trashMode" v-model:visible="detailsVisible" :path="detailsPath" @error="showError($event, 'Could not load details')" @size-ready="applyCachedFolderSize" />
    </div>

    <ContextMenu ref="contextMenu" :model="contextItems" :base-z-index="1100" @hide="onContextMenuHide" />

    <NewDocumentDialog v-model:visible="newDocumentVisible" @create="createDocument" />
    <NameDialog
      v-model:visible="nameDialogVisible"
      :title="nameDialogMode === 'folder' ? 'New folder' : 'Rename'"
      :label="nameDialogMode === 'folder' ? 'Folder name' : 'New name'"
      :initial-value="nameDialogMode === 'rename' ? selectedOne?.name : ''"
      :submit-label="nameDialogMode === 'folder' ? 'Create' : 'Rename'"
      @submit="onNameDialogSubmit"
    />
    <EditorDialog v-if="editorVisible" :visible="editorVisible" :path="editorPath" :is-new="editorIsNew" :theme="resolvedTheme" @update:visible="setEditorVisible" @saved="onEditorSaved" />
    <FilePreview :visible="previewVisible" :path="previewPath" :kind="previewKind" :content-type="previewContentType" :folder-path="currentPath" :entries="entries" @update:visible="setPreviewVisible" @return-to-folder="returnPreviewToFolder" @navigate="navigatePreview" />
    <ExtractDialog v-model:visible="extractVisible" :archive-name="extractTarget?.name ?? ''" :destination="extractDestination" :busy="extractBusy" @extract="prepareExtraction" />
    <ArchivePasswordDialog v-model:visible="extractPasswordVisible" :archive-name="extractTarget?.name ?? ''" :busy="extractBusy" :error="extractPasswordError" @submit="submitExtractionPassword" @cancel="cancelPasswordPrompt" />
    <DestinationDialog v-model:visible="destinationVisible" :mode="transferMode" :count="transferEntries.length" :initial-path="currentPath" @submit="submitTransfer" />
    <OperationsPanel v-model:visible="operationsVisible" :operations="operations" @cancel="cancelOperation" @clear-history="clearOperationHistory" @remove-history="removeOperationHistory" />
  </section>
</template>
