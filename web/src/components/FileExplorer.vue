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
import type { FileEntry, FileOperation, VideoThumbnailJob } from '@/types/files'
import { resolvedTheme, setTheme, themeMode, type ThemeMode } from '@/theme'
import { defaultPreferences, normalizePreferences, type UserPreferences } from '@/preferences'
import { isEntryVisible } from '@/utils/entryVisibility'
import { fileVisualForPath } from '@/utils/fileVisual'
import SettingsDialog from '@/components/SettingsDialog.vue'
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
  elevationAvailable: boolean
  elevated: boolean
  elevationExpiresAt?: string
}>()

const emit = defineEmits<{
  logout: []
  requestElevation: []
  dropElevation: []
}>()
const toast = useToast()
const confirm = useConfirm()

const {
  readSessionPath,
  readSessionState,
  readHashPath,
  readHashFile,
  writeHash: writeRawPathHash,
  goToPathInHistory,
  storePath: storeSessionPath,
  clearPath: clearSessionPath,
  storeState: storeExplorerState,
} = useExplorerNavigation(props.username)
const currentPath = ref(readHashPath() ?? readSessionPath())
const restoredExplorerState = readSessionState()
const singleRootPath = ref('')
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
const gridColumns = ref(1)
const contextMenu = ref<any>(null)
const actionsMenu = ref<any>(null)
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
const extractPasswordDialogClosed = ref(true)
const pendingPasswordTerminal = ref<FileOperation | null>(null)
const extractBusy = ref(false)
const destinationVisible = ref(false)
const transferMode = ref<TransferMode>('copy')
const operationsVisible = ref(false)
const taskAttention = ref(false)
const settingsVisible = ref(false)
const settingsSaving = ref(false)
const userPreferences = ref<UserPreferences>({ ...defaultPreferences })
const thumbnailJobs = ref<VideoThumbnailJob[]>([])
const thumbnailBatchReady = ref(true)
const detailsVisible = ref(restoredExplorerState.detailsVisible)
const detailsPath = ref(restoredStateMatchesPath ? restoredExplorerState.detailsPath : currentPath.value)
const transferEntries = ref<FileEntry[]>([])
const renameTarget = ref<FileEntry | null>(null)
const extractTarget = ref<FileEntry | null>(null)
const uploadInput = ref<HTMLInputElement | null>(null)
const draggingOver = ref(false)
let invalidateTimer: number | null = null
let hashFileRequest = 0
let breadcrumbResizeObserver: ResizeObserver | null = null
let preferencesLoaded = false
let navigationReady = false
let thumbnailBatchGeneration = 0
let parentHistoryNavigationPending = false
let queuedParentNavigations = 0
let parentHistoryNavigationTimer: number | null = null
let lastParentHistoryNavigationAt = Number.NEGATIVE_INFINITY
const parentHistoryNavigationIntervalMs = 500

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
    if (path !== currentPath.value) return
    if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
    invalidateTimer = window.setTimeout(() => void refresh(false), 120)
  },
  onOperation: handleOperationUpdate,
  onThumbnailJob: updateThumbnailJob,
  onConnected: () => {
    void Promise.all([loadOperations(), loadThumbnailJobs()]).then(() => restorePasswordPrompt())
  },
})

const activeThumbnailJobs = computed(() => thumbnailJobs.value.filter(job => ['queued', 'probing', 'generating', 'publishing'].includes(job.state)))
const taskBadgeCount = computed(() => activeOperations.value.length + activeThumbnailJobs.value.length)

watch(operationsVisible, visible => {
  if (!visible) return
  clearTaskAttention()
  void loadThumbnailJobs()
})

function clearTaskAttention() {
  taskAttention.value = false
}

function signalTaskAdded() {
  if (operationsVisible.value) return
  clearTaskAttention()
  void nextTick(() => {
    if (operationsVisible.value) return
    taskAttention.value = true
  })
}

async function loadThumbnailJobs() {
  try {
    thumbnailJobs.value = (await api.thumbnailJobs()).jobs
  } catch {
    // Realtime updates or the next drawer open will retry.
  }
}

function updateThumbnailJob(job: VideoThumbnailJob) {
  const index = thumbnailJobs.value.findIndex(current => current.id === job.id)
  if (index === -1) thumbnailJobs.value = [job, ...thumbnailJobs.value]
  else thumbnailJobs.value.splice(index, 1, job)
  if (job.state === 'ready' || job.state === 'cancelled') {
    window.setTimeout(() => {
      thumbnailJobs.value = thumbnailJobs.value.filter(current => current.id !== job.id || !['ready', 'cancelled'].includes(current.state))
    }, 3200)
  }
}

function cancelThumbnailJob(id: string) {
  void api.cancelThumbnailJob(id).catch(err => showError(err, 'Could not cancel thumbnail'))
}

const filteredEntries = computed(() => {
  const q = search.value.trim().toLocaleLowerCase()
  return entries.value.filter(entry => {
    if (!isEntryVisible(entry, { showHidden: showHidden.value, hideInaccessible: userPreferences.value.hide_inaccessible })) return false
    return !q || entry.name.toLocaleLowerCase().includes(q)
  })
})
const orderedVideoThumbnailPaths = computed(() => filteredEntries.value
  .filter(entry => !entry.is_dir && entry.can_read !== false && fileVisualForPath(entry.path).kind === 'video')
  .map(entry => entry.path))

watch([viewMode, orderedVideoThumbnailPaths], async ([mode, paths]) => {
  const generation = ++thumbnailBatchGeneration
  if (mode !== 'grid' || paths.length === 0) {
    thumbnailBatchReady.value = true
    return
  }
  thumbnailBatchReady.value = false
  try {
    await api.queueVideoThumbnails(paths)
  } catch {
    // Individual tile requests remain the fallback if batch scheduling fails.
  } finally {
    if (generation === thumbnailBatchGeneration) thumbnailBatchReady.value = true
  }
}, { flush: 'pre' })

const selectedEntries = computed(() => entries.value.filter(entry => selectedPaths.value.has(entry.path)))
const selectedOne = computed(() => selectedEntries.value.length === 1 ? selectedEntries.value[0] : null)
const canMutate = computed(() => currentPath.value !== '/')
const elevationLabel = computed(() => {
  if (!props.elevationExpiresAt) return 'Elevated access active'
  const expires = new Date(props.elevationExpiresAt)
  if (Number.isNaN(expires.getTime())) return 'Elevated access active'
  return `Elevated until ${expires.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
})

const themeItems = computed(() => (['system', 'dark', 'light'] as ThemeMode[]).map(mode => ({
  mode,
  label: mode[0].toUpperCase() + mode.slice(1),
  icon: themeMode.value === mode ? 'pi pi-check' : mode === 'system' ? 'pi pi-palette' : mode === 'dark' ? 'pi pi-moon' : 'pi pi-sun',
})))

const contextItems = computed(() => {
  const clicked = contextEntry.value
  const items = clicked && !selectedPaths.value.has(clicked.path) ? [clicked] : selectedEntries.value
  if (items.length === 0) return []
  const one = items.length === 1 ? items[0] : null
  const containsConfiguredRoot = currentPath.value === '/' && items.length > 0
  const result: Array<{ label?: string; icon?: string; command?: () => void; separator?: boolean; disabled?: boolean }> = []

  if (one) {
    if (!canOpenEntry(one) && props.elevationAvailable && !props.elevated) {
      result.push({ label: 'Elevate access…', icon: 'pi pi-shield', command: () => emit('requestElevation') })
      result.push({ separator: true })
    }
    result.push({ label: 'Open', icon: one.is_dir ? 'pi pi-folder-open' : 'pi pi-file-edit', disabled: !canOpenEntry(one), command: () => openEntry(one) })
    result.push({ label: 'Download', icon: 'pi pi-download', disabled: !one.is_dir && one.can_read === false, command: () => download(one) })
    result.push({ label: 'Details', icon: 'pi pi-info-circle', command: () => toggleDetails(one) })
    if (isArchive(one)) {
      result.push({ label: 'Extract…', icon: 'pi pi-box', disabled: one.can_read === false, command: () => openExtract(one) })
      result.push({ label: 'Extract here', icon: 'pi pi-folder-open', disabled: one.can_read === false, command: () => extractHere(one) })
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
  const displayRoot = singleRootPath.value || '/'
  if (currentPath.value === '/' || currentPath.value === displayRoot) return [{ name: 'Files', path: displayRoot }]
  const relativePath = singleRootPath.value && currentPath.value.startsWith(`${singleRootPath.value}/`)
    ? currentPath.value.slice(singleRootPath.value.length)
    : currentPath.value
  const parts = relativePath.split('/').filter(Boolean)
  const crumbs = [{ name: 'Files', path: displayRoot }]
  let p = singleRootPath.value
  for (const part of parts) {
    p += `/${part}`
    crumbs.push({ name: part, path: p })
  }
  return crumbs
})

function parentPathFor(path: string): string {
  if (path === '/' || path === singleRootPath.value) return ''
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  const parent = parts.length ? `/${parts.join('/')}` : '/'
  return singleRootPath.value && !parent.startsWith(`${singleRootPath.value}/`) && parent !== singleRootPath.value ? '' : parent
}

const parentPath = computed(() => parentPathFor(currentPath.value))

onMounted(async () => {
  await loadRootLayout()
  currentPath.value = toInternalPath(readHashPath() ?? currentPath.value)
  await nextTick()
  navigationReady = true
  startRealtime(currentPath.value)
  breadcrumbResizeObserver = new ResizeObserver(scrollBreadcrumbToEnd)
  if (breadcrumbScroll.value) breadcrumbResizeObserver.observe(breadcrumbScroll.value)
  window.addEventListener('keydown', onGlobalKeyDown)
  window.addEventListener('popstate', applyHashPath)
  window.addEventListener('hashchange', applyHashPath)
  const initialFolderLoad = refresh(true)
  await Promise.all([loadPreferences(), loadOperations()])
  restorePasswordPrompt()
  const fallbackPath = singleRootPath.value || '/'
  if (!await initialFolderLoad && currentPath.value !== fallbackPath) {
    clearSessionPath()
    navigateTo(fallbackPath, true)
  } else {
    const file = readInternalHashFile()
    writePathHash(currentPath.value, true, file)
    if (file) await restoreHashFile(file)
  }
})

watch(currentPath, path => {
  if (!navigationReady) return
  clearSelection()
  clearFocus(path)
  search.value = ''
  setRealtimeDirectory(path)
  void refresh(true)
}, { flush: 'post' })

watch(currentPath, () => { void scrollBreadcrumbToEnd() }, { flush: 'post' })

watch([viewMode, gridSize], () => {
  if (preferencesLoaded) void savePreferences()
  void nextTick(scrollFocusedIntoView)
})

watch(showHidden, () => { if (preferencesLoaded) void savePreferences() })

watch([selectedPaths, focusedPath, detailsVisible, detailsPath], storeSessionExplorerState, { flush: 'sync' })

onBeforeUnmount(() => {
  if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
  if (parentHistoryNavigationTimer !== null) window.clearTimeout(parentHistoryNavigationTimer)
  window.removeEventListener('keydown', onGlobalKeyDown)
  window.removeEventListener('popstate', applyHashPath)
  window.removeEventListener('hashchange', applyHashPath)
  breadcrumbResizeObserver?.disconnect()
})

async function loadRootLayout() {
  try {
    const result = await api.list('/')
    singleRootPath.value = result.single_root_path ?? ''
  } catch {
    singleRootPath.value = ''
  }
}

function toInternalPath(path: string): string {
  const root = singleRootPath.value
  if (!root || path === root || path.startsWith(`${root}/`)) return path
  return path === '/' ? root : `${root}${path}`
}

function toVisiblePath(path: string): string {
  const root = singleRootPath.value
  if (!root) return path
  if (path === root) return '/'
  return path.startsWith(`${root}/`) ? path.slice(root.length) : path
}

function readInternalHashFile(): string | null {
  const path = readHashFile()
  return path ? toInternalPath(path) : null
}

function writePathHash(path: string, replace = false, file: string | null = null) {
  writeRawPathHash(path, replace, file, toVisiblePath(path))
}

async function scrollBreadcrumbToEnd() {
  await nextTick()
  if (breadcrumbScroll.value) breadcrumbScroll.value.scrollLeft = breadcrumbScroll.value.scrollWidth
}

async function loadPreferences() {
  try {
    applyPreferences(normalizePreferences(await api.preferences()))
  } catch {
    // Preferences are non-critical; browsing should still work.
  } finally {
    preferencesLoaded = true
  }
}

async function savePreferences() {
  try {
    userPreferences.value = currentPreferences()
    await api.savePreferences(userPreferences.value)
  } catch {
    // Do not interrupt file work for a preference persistence failure.
  }
}

function currentPreferences(): UserPreferences {
  return {
    ...userPreferences.value,
    view_mode: viewMode.value,
    grid_size: gridSize.value,
    show_hidden: showHidden.value,
    theme: themeMode.value,
  }
}

function applyPreferences(preferences: UserPreferences) {
  const wasLoaded = preferencesLoaded
  preferencesLoaded = false
  userPreferences.value = { ...preferences }
  viewMode.value = preferences.view_mode
  gridSize.value = preferences.grid_size
  showHidden.value = preferences.show_hidden
  setTheme(preferences.theme)
  preferencesLoaded = wasLoaded
}

async function saveSettings(preferences: UserPreferences) {
  settingsSaving.value = true
  try {
    const saved = normalizePreferences(await api.savePreferences(preferences))
    applyPreferences(saved)
    settingsVisible.value = false
    toast.add({ severity: 'success', summary: 'Settings saved', life: 2000 })
  } catch (err) {
    showError(err, 'Could not save settings')
  } finally {
    settingsSaving.value = false
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

function applyHashPath() {
  void applyHashLocation()
}

async function applyHashLocation() {
  const request = ++hashFileRequest
  const path = readHashPath()
  const internalPath = path ? toInternalPath(path) : null
  const pathChanged = Boolean(internalPath && internalPath !== currentPath.value)
  if (pathChanged && internalPath) {
    currentPath.value = internalPath
  }
  const file = readInternalHashFile()
  if (path && internalPath && path !== toVisiblePath(internalPath)) {
    writePathHash(internalPath, true, file)
  }
  if (pathChanged) {
    if (parentHistoryNavigationPending) queueMicrotask(finishParentHistoryNavigation)
    else clearQueuedParentNavigation()
  }
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
    if (requestedPath !== currentPath.value) return false
    singleRootPath.value = result.single_root_path ?? ''
    if (requestedPath === '/' && singleRootPath.value && navigationReady) {
      navigateTo(singleRootPath.value, true)
      return true
    }
    entries.value = result.entries
    const existing = new Set(result.entries.map(entry => entry.path))
    selectedPaths.value = new Set([...selectedPaths.value].filter(path => existing.has(path)))
    if (focusedPath.value && !existing.has(focusedPath.value)) clearFocus(requestedPath)
    if (detailsVisible.value && detailsPath.value !== requestedPath && !existing.has(detailsPath.value)) detailsPath.value = requestedPath
    storeSessionPath(requestedPath)
    return true
  } catch (err) {
    if (requestedPath === currentPath.value) error.value = messageOf(err)
    return false
  } finally {
    if (showSpinner) loading.value = false
  }
}

function navigateTo(path: string, replaceHistory = false) {
  if (path === currentPath.value) return
  clearQueuedParentNavigation()
  currentPath.value = path
  writePathHash(path, replaceHistory)
}

function navigateHistoryAware(path: string): boolean {
  if (path === currentPath.value) return false
  if (goToPathInHistory(path)) return true
  navigateTo(path)
  return false
}

function navigateBreadcrumb(path: string) {
  clearQueuedParentNavigation()
  navigateHistoryAware(path)
}

function showOperationTerminalToast(operation: FileOperation) {
  if (operation.state === 'completed') {
    toast.add({ severity: 'success', summary: `${operationLabel(operation.kind)} complete`, detail: operation.message || undefined, life: 2500 })
  } else if (operation.state === 'failed') {
    toast.add({ severity: 'error', summary: `${operationLabel(operation.kind)} failed`, detail: operation.error, life: 6000 })
  }
}

function onOperationTerminal(operation: FileOperation) {
  if (operation.id === extractPasswordOperationId.value && !extractPasswordDialogClosed.value) {
    pendingPasswordTerminal.value = operation
  } else {
    showOperationTerminalToast(operation)
  }
  void refresh(false)
}

async function onArchivePasswordAfterHide() {
  await nextTick()
  extractPasswordDialogClosed.value = true
  if (!pendingPasswordTerminal.value) return
  showOperationTerminalToast(pendingPasswordTerminal.value)
  pendingPasswordTerminal.value = null
}

function canOpenEntry(entry: FileEntry) {
  return entry.is_dir ? entry.can_navigate !== false : entry.can_read !== false
}

async function openEntry(entry: FileEntry) {
  if (!canOpenEntry(entry)) return
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

function showActionsMenu(event: Event) {
  contextEntry.value = null
  actionsMenu.value?.toggle(event)
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
  if (readInternalHashFile() !== path) writePathHash(currentPath.value, false, path)
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
    message: `Permanently delete ${description}? This cannot be undone.`,
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Cancel',
    acceptLabel: 'Delete permanently',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      await beginOperation('delete', items.map(item => item.path))
      clearSelection()
    },
  })
}

async function beginOperation(kind: 'copy' | 'move' | 'delete' | 'extract', sources: string[], destination?: string) {
  try {
    const copyChunkMiB = kind === 'copy' || kind === 'move' ? userPreferences.value.copy_chunk_mib : undefined
    await startFileOperation(kind, sources, destination, copyChunkMiB)
    signalTaskAdded()
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
    signalTaskAdded()
    extractVisible.value = false
  } catch (err) {
    showError(err, 'Could not start extraction')
  } finally {
    extractBusy.value = false
  }
}

async function submitExtractionPassword(password: string) {
  const operationId = extractPasswordOperationId.value
  if (!operationId) return
  extractPasswordVisible.value = false
  extractPasswordError.value = ''
  extractBusy.value = true
  try {
    await api.submitOperationPassword(operationId, password)
  } catch (err) {
    if (extractPasswordOperationId.value === operationId) {
      extractPasswordVisible.value = true
    }
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
  extractPasswordDialogClosed.value = false
  pendingPasswordTerminal.value = null
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
      let announced = false
      const operation = await api.upload(destination, file, current => {
        updateOperation(current)
        if (announced) return
        announced = true
        signalTaskAdded()
      })
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
  if (!parentPath.value) return
  queuedParentNavigations++
  scheduleQueuedParentNavigation()
}

function finishParentHistoryNavigation() {
  if (!parentHistoryNavigationPending) return
  parentHistoryNavigationPending = false
  scheduleQueuedParentNavigation()
}

function clearQueuedParentNavigation() {
  queuedParentNavigations = 0
  if (parentHistoryNavigationTimer === null) return
  window.clearTimeout(parentHistoryNavigationTimer)
  parentHistoryNavigationTimer = null
}

function scheduleQueuedParentNavigation() {
  if (parentHistoryNavigationPending || !queuedParentNavigations) return
  const wait = Math.max(0, lastParentHistoryNavigationAt + parentHistoryNavigationIntervalMs - performance.now())
  if (wait > 0) {
    if (parentHistoryNavigationTimer === null) {
      parentHistoryNavigationTimer = window.setTimeout(() => {
        parentHistoryNavigationTimer = null
        runQueuedParentNavigation()
      }, wait)
    }
    return
  }
  runQueuedParentNavigation()
}

function runQueuedParentNavigation() {
  if (parentHistoryNavigationPending || !queuedParentNavigations) return

  let target = currentPath.value
  const requestedLevels = queuedParentNavigations
  queuedParentNavigations = 0
  for (let level = 0; level < requestedLevels; level++) {
    const parent = parentPathFor(target)
    if (!parent) break
    target = parent
  }
  if (target === currentPath.value) return
  parentHistoryNavigationPending = navigateHistoryAware(target)
  if (parentHistoryNavigationPending) lastParentHistoryNavigationAt = performance.now()
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
  userPreferences.value.theme = mode
  themeMenu.value?.hide()
  void savePreferences()
}

function showThemeMenu(event: Event) {
  themeMenu.value?.toggle(event)
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
      deleteEntries()
    }
    return
  }
  if (event.key === 'F2') {
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
  return capitalize(value)
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
          <Button v-tooltip.bottom="detailsVisible ? 'Close details' : 'Open details'" icon="pi pi-info-circle" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': detailsVisible }" :aria-label="detailsVisible ? 'Close details' : 'Open details'" :aria-pressed="detailsVisible" @click="toggleDetailsPanel" />
          <Button v-if="selectedEntries.length" v-tooltip.bottom="'Actions'" icon="pi pi-ellipsis-v" size="small" severity="secondary" text aria-label="Actions" @click="showActionsMenu" />
        </div>

        <span class="min-w-2 flex-1" />
        <div class="flex items-center gap-1">
          <Button
            v-tooltip.bottom="operationsVisible ? 'Close tasks' : 'Open tasks'"
            icon="pi pi-list-check"
            size="small"
            severity="secondary"
            text
            class="webfile-toggle"
            :class="{ 'webfile-toggle-active': operationsVisible, 'webfile-task-attention': taskAttention }"
            :badge="taskBadgeCount ? String(taskBadgeCount) : undefined"
            :aria-label="operationsVisible ? 'Close tasks' : 'Open tasks'"
            :aria-pressed="operationsVisible"
            @click="operationsVisible = !operationsVisible"
          />
          <Button v-tooltip.bottom="'Settings'" icon="pi pi-cog" size="small" severity="secondary" text aria-label="Settings" @click="settingsVisible = true" />
        </div>
        <span class="mx-1 h-6 w-px bg-zinc-700" />
        <Button v-tooltip.bottom="`Theme: ${themeMode}`" :icon="themeMode === 'system' ? 'pi pi-palette' : themeMode === 'dark' ? 'pi pi-moon' : 'pi pi-sun'" size="small" severity="secondary" text aria-label="Choose theme" @click="showThemeMenu" />
        <Menu ref="themeMenu" popup :model="themeItems">
          <template #item="{ item, props }">
            <a v-bind="props.action" @click="selectTheme(item.mode)">
              <span :class="item.icon" />
              <span class="ml-2">{{ item.label }}</span>
            </a>
          </template>
        </Menu>
        <span v-if="props.username || props.showLogout" class="mx-1 h-6 w-px bg-zinc-700" />
        <Button
          v-if="props.elevationAvailable && !props.elevated"
          v-tooltip.bottom="'Elevate access'"
          icon="pi pi-shield"
          severity="secondary"
          text
          rounded
          aria-label="Elevate access"
          @click="emit('requestElevation')"
        />
        <Button
          v-if="props.elevated"
          v-tooltip.bottom="'Drop elevated access'"
          :label="elevationLabel"
          icon="pi pi-shield"
          severity="warn"
          size="small"
          outlined
          aria-label="Drop elevated access"
          @click="emit('dropElevation')"
        />
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
      <nav ref="breadcrumbScroll" class="min-w-0 flex-1 overflow-x-auto" aria-label="Breadcrumb" data-breadcrumb-scroll :data-single-root="singleRootPath ? 'true' : 'false'">
        <div class="flex min-w-max items-center gap-1 text-sm">
          <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
            <i v-if="index" class="pi pi-angle-right text-xs text-zinc-600" />
            <button class="rounded px-2 py-1 text-zinc-300 hover:bg-zinc-800 hover:text-white" :aria-current="index === breadcrumbs.length - 1 ? 'page' : undefined" @click="navigateBreadcrumb(crumb.path)">
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
      <Button v-tooltip.bottom="'Refresh'" icon="pi pi-refresh" size="small" severity="secondary" text aria-label="Refresh" @click="refresh(true)" />
      <span class="h-6 w-px bg-zinc-800" />
      <div v-if="filterVisible" class="w-64">
        <InputText ref="filterInput" v-model="search" size="small" fluid placeholder="Filter this folder" aria-label="Filter this folder" @keydown.escape="closeFilter" />
      </div>
      <Button v-tooltip.bottom="filterVisible ? 'Close filter' : 'Filter (Ctrl+F)'" icon="pi pi-search" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': filterVisible }" :aria-label="filterVisible ? 'Close filter' : 'Open filter'" :aria-pressed="filterVisible" @click="toggleFilter" />
      <span class="h-6 w-px bg-zinc-800" />
      <div class="flex items-center gap-1">
        <Button v-tooltip.bottom="showHidden ? 'Hide dot files' : 'Show dot files'" icon="pi pi-eye" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': showHidden }" :aria-label="showHidden ? 'Hide dot files' : 'Show dot files'" :aria-pressed="showHidden" @click="showHidden = !showHidden" />
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
      </div>
    </div>

    <div class="flex min-h-0 flex-1">
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
            :parent-path="parentPath"
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
            :thumbnails-ready="thumbnailBatchReady"
            :parent-path="parentPath"
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
      <DetailsDialog v-model:visible="detailsVisible" :path="detailsPath" @error="showError($event, 'Could not load details')" @size-ready="applyCachedFolderSize" />
    </div>

    <ContextMenu ref="contextMenu" :model="contextItems" :base-z-index="1100" @hide="onContextMenuHide" />
    <Menu ref="actionsMenu" popup :model="contextItems" />

    <NewDocumentDialog v-model:visible="newDocumentVisible" @create="createDocument" />
    <NameDialog
      v-model:visible="nameDialogVisible"
      :title="nameDialogMode === 'folder' ? 'New folder' : 'Rename'"
      :label="nameDialogMode === 'folder' ? 'Folder name' : 'New name'"
      :initial-value="nameDialogMode === 'rename' ? selectedOne?.name : ''"
      :submit-label="nameDialogMode === 'folder' ? 'Create' : 'Rename'"
      @submit="onNameDialogSubmit"
    />
    <SettingsDialog v-model:visible="settingsVisible" :preferences="currentPreferences()" :saving="settingsSaving" @save="saveSettings" />
    <EditorDialog v-if="editorVisible" :visible="editorVisible" :path="editorPath" :is-new="editorIsNew" :theme="resolvedTheme" :preferences="userPreferences" @update:visible="setEditorVisible" @saved="onEditorSaved" />
    <FilePreview :visible="previewVisible" :path="previewPath" :kind="previewKind" :content-type="previewContentType" :folder-path="currentPath" :entries="entries" @update:visible="setPreviewVisible" @return-to-folder="returnPreviewToFolder" @navigate="navigatePreview" />
    <ExtractDialog v-model:visible="extractVisible" :archive-name="extractTarget?.name ?? ''" :destination="extractDestination" :busy="extractBusy" @extract="prepareExtraction" />
    <ArchivePasswordDialog v-model:visible="extractPasswordVisible" :archive-name="extractTarget?.name ?? ''" :busy="extractBusy" :error="extractPasswordError" @submit="submitExtractionPassword" @cancel="cancelPasswordPrompt" @after-hide="onArchivePasswordAfterHide" />
    <DestinationDialog
      v-model:visible="destinationVisible"
      :mode="transferMode"
      :count="transferEntries.length"
      :initial-path="currentPath"
      :show-hidden="showHidden"
      :hide-inaccessible="userPreferences.hide_inaccessible"
      @submit="submitTransfer"
    />
    <OperationsPanel v-model:visible="operationsVisible" :operations="operations" :thumbnail-jobs="thumbnailJobs" @cancel="cancelOperation" @cancel-thumbnail="cancelThumbnailJob" @clear-history="clearOperationHistory" @remove-history="removeOperationHistory" />
  </section>
</template>
