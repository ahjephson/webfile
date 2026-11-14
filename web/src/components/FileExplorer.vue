<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import Button from 'primevue/button'
import ContextMenu from 'primevue/contextmenu'
import InputText from 'primevue/inputtext'
import Menu from 'primevue/menu'
import ProgressSpinner from 'primevue/progressspinner'
import SplitButton from 'primevue/splitbutton'
import { useAppConfirm } from '@/composables/useAppConfirm'
import { useToast } from 'primevue/usetoast'
import { api, type PreparedUpload } from '@/api/client'
import { appPath } from '@/base'
import type { ConflictDecision, FileEntry, FileOperation, SearchRequest, VideoThumbnailJob } from '@/types/files'
import { resolvedTheme, setTheme, themeMode, type ThemeMode } from '@/theme'
import { defaultPreferences, normalizePreferences, type UserPreferences } from '@/preferences'
import { isEntryVisible } from '@/utils/entryVisibility'
import { fileVisualForPath } from '@/utils/fileVisual'
import { sortEntries, type SortField, type SortDirection } from '@/utils/sorting'
import { archiveExtensions } from '@/utils/archives'
import type { ListingScrollPosition } from '@/utils/listingScroll'
import SettingsDialog from '@/components/SettingsDialog.vue'
import SearchDialog from '@/components/SearchDialog.vue'
import ConflictDialog from '@/components/ConflictDialog.vue'
import { useRealtime } from '@/composables/useRealtime'
import { useFilePreview } from '@/composables/useFilePreview'
import { useExplorerSelection } from '@/composables/useExplorerSelection'
import { useExplorerNavigation } from '@/composables/useExplorerNavigation'
import { useFileOperations } from '@/composables/useFileOperations'
import { useFilenameSearch } from '@/composables/useFilenameSearch'
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
import { useExplorerWorkspace, fileDragType, dropMode, type PaneId } from '@/composables/useExplorerWorkspace'

const EditorDialog = defineAsyncComponent(() => import('@/components/EditorDialog.vue'))
const editorDialog = ref<{ confirmDiscard: () => boolean; currentPath: string } | null>(null)

type ViewMode = 'list' | 'grid'
type GridSize = 'small' | 'medium' | 'large'
type TransferMode = 'copy' | 'move'
const props = defineProps<{
  username: string
  showLogout: boolean
  elevationAvailable: boolean
  elevated: boolean
  elevationExpiresAt?: string
  paneId: PaneId
  panePosition: PaneId
  paneVisible: boolean
}>()

const workspace = useExplorerWorkspace()
const isPrimary = props.paneId === 'left'
const isActive = computed(() => workspace.activePane.value === props.paneId)
const currentFolderSelected = ref(false)
const otherPane = computed(() => workspace.panes.get(props.paneId === 'left' ? 'right' : 'left'))
const otherPath = computed(() => workspace.split.value ? otherPane.value?.path.value ?? '' : '')
function activatePane() { workspace.activePane.value = props.paneId }

const emit = defineEmits<{
  logout: []
  requestElevation: []
  dropElevation: []
}>()
const toast = useToast()
const confirm = useAppConfirm()

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
} = useExplorerNavigation(props.username, props.paneId)
const currentPath = ref(readHashPath() ?? readSessionPath(otherPane.value?.path.value ?? '/'))
const restoredExplorerState = readSessionState()
const singleRootPath = ref('')
const restoredStateMatchesPath = restoredExplorerState.directory === currentPath.value
// Listings are immutable snapshots. Per-entry reactive proxies are costly for
// very large folders; changes replace the snapshot instead.
const entries = shallowRef<FileEntry[]>([])
let loadedDirectoryPath = ''
let loadedDirectoryElevated = props.elevated
let listingShareable = false
const loading = ref(false)
const error = ref('')
const search = ref('')
const filterVisible = ref(false)
const recursiveSearchVisible = ref(false)
const { request: searchRequest, results: searchResults, busy: searchBusy, message: searchMessage, error: searchError, scanned: searchScanned, skipped: searchSkipped, start: runSearch, cancel: cancelSearch, close: closeSearch } = useFilenameSearch()
const viewingSearch = computed(() => searchRequest.value !== null)
const viewEntries = computed(() => viewingSearch.value ? [...searchResults.value].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }) || a.path.localeCompare(b.path)) : entries.value)
function startSearch(request: SearchRequest) {
  recursiveSearchVisible.value = false
  search.value = ''; clearSelection(); clearFocus(); loading.value = false
  void runSearch(request)
}
function exitSearch() {
  closeSearch(); search.value = ''; clearSelection(); clearFocus()
  void refresh(true)
}
function refreshView() {
  if (searchRequest.value) startSearch(searchRequest.value)
  else void refresh(true)
}
async function locateSearchResult(entry: FileEntry, open = false) {
  recursiveSearchVisible.value = false
  closeSearch(); search.value = ''
  if (entry.name.startsWith('.')) showHidden.value = true
  navigateTo(entry.path.slice(0, entry.path.lastIndexOf('/')) || '/')
  if (!await refresh(false)) return
  if (!entries.value.some(current => current.path === entry.path)) { showError(new Error('This search result is no longer available')); return }
  focusedPath.value = entry.path; replaceSelection([entry.path])
  if (detailsVisible.value) detailsPath.value = entry.path
  if (open) await openEntry(entry)
  else void nextTick(scrollFocusedIntoView)
}
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
const sortField = ref<SortField>('name')
const sortDirection = ref<SortDirection>('asc')
const sortMenu = ref<any>(null)
const paneOverflowMenu = ref<any>(null)
const sortItems = computed(() => [
  ...(['name', 'modified', 'size', 'type'] as SortField[]).map(field => ({
    label: `Sort by ${field}`, class: sortField.value === field ? 'webfile-sort-selected' : undefined,
    command: () => chooseSort(field, sortDirection.value),
  })),
  { separator: true },
  ...(['asc', 'desc'] as SortDirection[]).map(direction => ({
    label: direction === 'asc' ? 'Ascending' : 'Descending',
    class: sortDirection.value === direction ? 'webfile-sort-selected' : undefined,
    command: () => chooseSort(sortField.value, direction),
  })),
])
function chooseSort(field: SortField, direction?: SortDirection) {
  sortDirection.value = direction ?? (sortField.value === field && sortDirection.value === 'asc' ? 'desc' : 'asc')
  sortField.value = field
  void savePreferences()
  void nextTick(() => scrollFocusedIntoView())
}
const paneOverflowItems = computed(() => [
  { label: 'Folder actions', items: [
    { label: 'Search beneath this folder', icon: 'pi pi-search-plus', disabled: currentPath.value === '/', command: () => { recursiveSearchVisible.value = true } },
    { label: filterVisible.value ? 'Close filter' : 'Filter this folder', icon: 'pi pi-search', command: toggleFilter },
    ...(!viewingSearch.value ? [{ label: showHidden.value ? 'Hide dot files' : 'Show dot files', icon: 'pi pi-eye', command: () => { showHidden.value = !showHidden.value } }] : []),
    { label: 'Select all', icon: 'pi pi-list-check', command: selectAll },
    { label: 'Clear selection', icon: 'pi pi-times', command: clearSelection },
  ] },
  { label: 'Sort files', items: sortItems.value },
])
const gridColumns = ref(1)
const contextMenu = ref<any>(null)
const actionsMenu = ref<any>(null)
const contextEntry = ref<FileEntry | null>(null)
const themeMenu = ref<any>(null)
const listView = ref<InstanceType<typeof VirtualFileList> | null>(null)
const gridView = ref<InstanceType<typeof VirtualFileGrid> | null>(null)
const directoryScrollPositions = new Map<string, ListingScrollPosition>()
let pendingScrollRestorePath = ''
const fileViewHost = ref<HTMLElement | null>(null)
const breadcrumbScroll = ref<HTMLElement | null>(null)
const breadcrumbContent = ref<HTMLElement | null>(null)

const newDocumentVisible = ref(false)
const nameDialogVisible = ref(false)
const nameBusy = ref(false)
const transferBusy = ref(false)
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
const passwordBusy = ref(false)
const shownPasswordRequests = new Set<string>()
const extractDestination = ref('')
const extractPasswordDialogClosed = ref(true)
const pendingPasswordTerminal = ref<FileOperation | null>(null)
const extractBusy = ref(false)
const destinationVisible = ref(false)
const transferMode = ref<TransferMode>('copy')
const operationsVisible = workspace.operationsVisible
const taskAttention = workspace.taskAttention
const settingsVisible = workspace.settingsVisible
const settingsSaving = ref(false)
const conflictVisible = ref(false), conflictBusy = ref(false), conflictError = ref(''), conflictOperationId = ref('')
const shownConflicts = new Set<string>()
const userPreferences = ref<UserPreferences>({ ...defaultPreferences })
const thumbnailJobs = workspace.thumbnailJobs
const pendingVideoThumbnailPaths = ref<Set<string>>(new Set())
const detailsVisible = ref(restoredExplorerState.detailsVisible)
const detailsPath = ref(restoredStateMatchesPath ? restoredExplorerState.detailsPath : currentPath.value)
const transferEntries = ref<FileEntry[]>([])
const fileClipboard = workspace.clipboard
const renameTarget = ref<FileEntry | null>(null)
const extractTarget = ref<FileEntry | null>(null)
const extractTargets = ref<FileEntry[]>([])
const uploadInput = ref<HTMLInputElement | null>(null)
const draggingOver = ref(false)
const dragDestination = ref('')
const folderDropTarget = computed(() => draggingOver.value && dragDestination.value !== currentPath.value ? dragDestination.value : '')
const dragAction = ref<TransferMode>('move')
const dragLabel = computed(() => workspace.drag.value
  ? `${dragAction.value === 'copy' ? 'Copy' : 'Move'} to ${toVisiblePath(dragDestination.value || currentPath.value)}`
  : 'Drop files to upload')
watch(workspace.drag, drag => { if (!drag) draggingOver.value = false })
let invalidateTimer: number | null = null
let sizeFreshnessTimer: number | null = null
let sizeRefreshPoll: number | null = null
let refreshRequest = 0
let latestDirectoryRefresh: { path: string; promise: Promise<boolean> } | null = null
let invalidationRefreshRunning = false
let invalidationRefreshPending = false
let largeDirectoryRefreshTimer: number | null = null
let nextLargeDirectoryRefresh = 0
const largeDirectoryThreshold = 10_000
const largeDirectoryRefreshInterval = 5000
let explorerDisposed = false
let terminalRefreshTimer: number | null = null
const uploadBatchFolders = new Map<string, number>()
let acceptedHashLocation: { url: string; state: { webfilePosition?: number } | null } | null = null
let historyRestorePending = false
let hashFileRequest = 0
let breadcrumbResizeObserver: ResizeObserver | null = null
let preferencesLoaded = false
let navigationReady = false
let queuedVideoThumbnailVersions = new Map<string, string>()
let desiredVideoThumbnailEntries: Array<{ path: string; version: string }> = []
let thumbnailBatchRunning = false
let parentHistoryNavigationPending = false
let queuedParentNavigations = 0
let ignoreTouchNavigationUntil = 0

const {
  operations,
  activeOperations,
  load: loadOperations,
  update: updateOperation,
  start: startFileOperation,
  startExtractions,
  reorder: reorderFileOperation,
  undo: undoFileOperation,
  clearHistory: clearFinishedOperations,
  removeHistory: removeFinishedOperation,
} = useFileOperations(onOperationTerminal)

const conflictOperation = computed(() => operations.value.find(operation => operation.id === conflictOperationId.value && operation.state === 'awaiting_input' && operation.input)
  ?? operations.value.find(operation => operation.state === 'awaiting_input' && operation.input) ?? null)
watch(conflictOperation, operation => {
  if (!isPrimary) return
  if (!operation?.input) { conflictVisible.value = false; return }
  if (!shownConflicts.has(`${operation.id}:${operation.input.revision}`)) openConflict(operation)
})
function openConflict(operation: FileOperation) {
  if (!operation.input) return
  shownConflicts.add(`${operation.id}:${operation.input.revision}`)
  conflictOperationId.value = operation.id; conflictError.value = ''; conflictVisible.value = true
}
async function resolveConflict(decision: ConflictDecision) {
  const operation = conflictOperation.value
  if (!operation || conflictBusy.value) return
  conflictBusy.value = true; conflictError.value = ''
  try {
    await api.resolveConflict(operation.id, decision)
    await loadOperations()
  } catch (err) {
    conflictError.value = err instanceof Error ? err.message : String(err)
    await loadOperations()
  } finally { conflictBusy.value = false }
}
async function cancelConflictTask() {
  const operation = conflictOperation.value
  if (!operation || conflictBusy.value) return
  conflictBusy.value = true; conflictError.value = ''
  try { await api.cancelOperation(operation.id); await loadOperations() }
  catch (err) { conflictError.value = err instanceof Error ? err.message : String(err) }
  finally { conflictBusy.value = false }
}

const {
  start: startRealtime,
  stop: stopRealtime,
  setDirectory: setRealtimeDirectory,
  connected: watchConnected,
  cancelOperation: cancelRealtimeOperation,
} = useRealtime({
  onInvalidate: path => {
    if (path !== currentPath.value) return
    if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
    invalidateTimer = window.setTimeout(() => {
      invalidateTimer = null
      if (path === currentPath.value) void refreshInvalidatedDirectory()
    }, 120)
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
  void loadOperations()
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
    await workspace.thumbnails.load()
  } catch {
    // Realtime updates or the next drawer open will retry.
  }
}

function updateThumbnailJob(job: VideoThumbnailJob) {
  workspace.thumbnails.update(job)
}

async function cancelThumbnailJob(id: string) {
  const failed = thumbnailJobs.value.some(job => job.id === id && job.state === 'failed')
  try {
    await workspace.thumbnails.cancel(id)
  } catch (err) {
    showError(err, failed ? 'Could not dismiss thumbnail task' : 'Could not cancel thumbnail')
  }
}

const filteredEntries = computed(() => {
  const q = search.value.trim().toLocaleLowerCase()
  const visibility = { showHidden: viewingSearch.value ? searchRequest.value!.show_hidden : showHidden.value, hideInaccessible: userPreferences.value.hide_inaccessible }
  const visible = viewEntries.value.filter(entry => {
    if (!isEntryVisible(entry, visibility)) return false
    return !q || entry.name.toLocaleLowerCase().includes(q)
  })
  return sortEntries(visible, sortField.value, sortDirection.value)
})
const orderedVideoThumbnailEntries = computed(() => viewMode.value !== 'grid' ? [] : filteredEntries.value
  .filter(entry => !entry.is_dir && entry.can_read !== false && fileVisualForPath(entry.path).kind === 'video')
  .map(entry => ({ path: entry.path, version: `${entry.size}:${entry.modified}` })))

watch([viewMode, orderedVideoThumbnailEntries], ([mode, videoEntries]) => {
  desiredVideoThumbnailEntries = mode === 'grid' ? videoEntries : []
  const desired = new Set(desiredVideoThumbnailEntries.map(entry => entry.path))
  queuedVideoThumbnailVersions = new Map([...queuedVideoThumbnailVersions].filter(([path]) => desired.has(path)))

  const pending = new Set([...pendingVideoThumbnailPaths.value].filter(path => desired.has(path)))
  for (const entry of desiredVideoThumbnailEntries) {
    if (queuedVideoThumbnailVersions.get(entry.path) !== entry.version) pending.add(entry.path)
  }
  pendingVideoThumbnailPaths.value = pending
  void queuePendingVideoThumbnails()
}, { flush: 'pre' })

async function queuePendingVideoThumbnails() {
  if (thumbnailBatchRunning || desiredVideoThumbnailEntries.length === 0) return
  thumbnailBatchRunning = true
  try {
    while (desiredVideoThumbnailEntries.length > 0) {
      const entriesToQueue = [...desiredVideoThumbnailEntries]
      const missing = entriesToQueue.filter(entry => queuedVideoThumbnailVersions.get(entry.path) !== entry.version)
      if (missing.length === 0) return
      try {
        await api.queueVideoThumbnails(missing.map(entry => entry.path))
      } catch {
        // Individual tile requests remain the fallback if batch scheduling fails.
      }

      const current = new Map(desiredVideoThumbnailEntries.map(entry => [entry.path, entry.version]))
      const pending = new Set(pendingVideoThumbnailPaths.value)
      for (const entry of missing) {
        if (current.get(entry.path) !== entry.version) continue
        pending.delete(entry.path)
        queuedVideoThumbnailVersions.set(entry.path, entry.version)
      }
      pendingVideoThumbnailPaths.value = pending
    }
  } finally {
    thumbnailBatchRunning = false
    if (desiredVideoThumbnailEntries.some(entry => queuedVideoThumbnailVersions.get(entry.path) !== entry.version)) {
      void queuePendingVideoThumbnails()
    }
  }
}

const entryPositions = computed(() => new Map(viewEntries.value.map((entry, index) => [entry.path, index])))
const displayedPositions = computed(() => new Map(filteredEntries.value.map((entry, index) => [entry.path, index])))
const selectedEntries = computed(() => [...selectedPaths.value]
  .map(path => entryPositions.value.get(path))
  .filter((index): index is number => index !== undefined)
  .sort((a, b) => a - b)
  .map(index => viewEntries.value[index]))
const selectedOne = computed(() => selectedEntries.value.length === 1 ? selectedEntries.value[0] : null)
const canMutate = computed(() => currentPath.value !== '/' && !viewingSearch.value)
const canPaste = computed(() => canMutate.value && Boolean(fileClipboard.value?.paths.length))
const cutPaths = computed(() => fileClipboard.value?.mode === 'move' ? fileClipboard.value.paths : [])
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
  if (items.length === 0) {
    return fileClipboard.value ? [{ label: 'Paste', icon: 'pi pi-clipboard', disabled: !canPaste.value, command: pasteClipboard }] : []
  }
  const one = items.length === 1 ? items[0] : null
  const containsConfiguredRoot = currentPath.value === '/' && items.length > 0
  const result: Array<{ label?: string; icon?: string; command?: () => void; separator?: boolean; disabled?: boolean }> = []

  if (one) {
    if (!canOpenEntry(one) && props.elevationAvailable && !props.elevated) {
      result.push({ label: 'Elevate access…', icon: 'pi pi-shield', command: () => emit('requestElevation') })
      result.push({ separator: true })
    }
    result.push({ label: 'Open', icon: one.is_dir ? 'pi pi-folder-open' : 'pi pi-file-edit', disabled: !canOpenEntry(one), command: () => openEntry(one) })
    if (viewingSearch.value) result.push({ label: 'Show in folder', icon: 'pi pi-folder-open', command: () => locateSearchResult(one) })
    result.push({ label: 'Download', icon: 'pi pi-download', disabled: !one.is_dir && one.can_read === false, command: () => download(one) })
    result.push({ label: 'Details', icon: 'pi pi-info-circle', command: () => toggleDetails(one) })
  }

  if (items.every(isArchive)) {
    const unreadable = items.some(item => item.can_read === false)
    result.push({ label: items.length > 1 ? `Extract ${items.length} archives…` : 'Extract…', icon: 'pi pi-box', disabled: unreadable, command: () => openExtract(items) })
    result.push({ label: items.length > 1 ? `Extract ${items.length} archives here` : 'Extract here', icon: 'pi pi-folder-open', disabled: unreadable, command: () => extractHere(items) })
  }

  if (!containsConfiguredRoot) {
    if (result.length > 0) result.push({ separator: true })
    result.push(
      { label: `Copy${items.length > 1 ? ` ${items.length} items` : ''}`, icon: 'pi pi-copy', command: () => setFileClipboard('copy', items) },
      { label: `Cut${items.length > 1 ? ` ${items.length} items` : ''}`, icon: 'pi pi-file-export', command: () => setFileClipboard('move', items) },
      { label: `Copy${items.length > 1 ? ` ${items.length} items` : ''} to…`, icon: 'pi pi-copy', command: () => openTransfer('copy', items) },
      { label: `Move${items.length > 1 ? ` ${items.length} items` : ''} to…`, icon: 'pi pi-arrow-right', command: () => openTransfer('move', items) },
    )
    if (workspace.split.value) result.push(
      { label: 'Copy to other pane', icon: 'pi pi-copy', disabled: !otherPath.value || otherPath.value === '/', command: () => { void transferToOtherPane('copy', items) } },
      { label: 'Move to other pane', icon: 'pi pi-arrow-right', disabled: !otherPath.value || otherPath.value === '/', command: () => { void transferToOtherPane('move', items) } },
    )
    if (one) result.push({ label: 'Rename…', icon: 'pi pi-pencil', command: () => openRename(one) })
    result.push({ label: 'Delete', icon: 'pi pi-trash', command: () => deleteEntries(items) })
  }
  if (fileClipboard.value) {
    result.push({ separator: true }, { label: 'Paste', icon: 'pi pi-clipboard', disabled: !canPaste.value, command: pasteClipboard })
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
  workspace.panes.set(props.paneId, { path: currentPath, canHide: canHidePane, focus: () => fileViewHost.value?.focus(), snapshot: directorySnapshot })
  sizeFreshnessTimer = window.setInterval(() => {
    if (props.paneVisible && !viewingSearch.value && entries.value.some(entry => entry.is_dir && entry.size_calculated_at && Date.now() - Date.parse(entry.size_calculated_at) >= 5 * 60_000)) void refreshInvalidatedDirectory()
  }, 30_000)
  const shared = otherPane.value?.snapshot()
  if (shared) singleRootPath.value = shared.singleRootPath
  else await loadRootLayout()
  currentPath.value = toInternalPath(readHashPath() ?? currentPath.value)
  if (!restoredStateMatchesPath && restoredExplorerState.directory === currentPath.value) {
    replaceSelection(restoredExplorerState.selectedPaths)
    focusedPath.value = restoredExplorerState.focusedPath
    detailsPath.value = restoredExplorerState.detailsPath || currentPath.value
  }
  await nextTick()
  navigationReady = true
  if (props.paneVisible) startRealtime(currentPath.value)
  breadcrumbResizeObserver = new ResizeObserver(scrollBreadcrumbToEnd)
  if (breadcrumbScroll.value) breadcrumbResizeObserver.observe(breadcrumbScroll.value)
  // Font and icon metrics can change the trail width without resizing its viewport.
  if (breadcrumbContent.value) breadcrumbResizeObserver.observe(breadcrumbContent.value)
  window.addEventListener('keydown', onGlobalKeyDown)
  window.addEventListener('popstate', applyHashPath)
  window.addEventListener('hashchange', applyHashPath)
  const initialFolderLoad = refresh(true, true)
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

watch(currentPath, (_path, previousPath) => rememberDirectoryScroll(previousPath), { flush: 'sync' })

watch(currentPath, path => {
  currentFolderSelected.value = false
  if (!navigationReady) return
  if (largeDirectoryRefreshTimer !== null) window.clearTimeout(largeDirectoryRefreshTimer)
  largeDirectoryRefreshTimer = null
  nextLargeDirectoryRefresh = 0
  closeSearch()
  clearSelection()
  clearFocus(path)
  search.value = ''
  if (props.paneVisible) setRealtimeDirectory(path)
  void refresh(true)
}, { flush: 'post' })

watch(currentPath, () => { void scrollBreadcrumbToEnd() }, { flush: 'post' })

watch(() => props.paneVisible, visible => {
  if (!navigationReady) return
  if (visible) { startRealtime(currentPath.value); void refresh(true, true) }
  else {
    listingShareable = false
    stopRealtime(); closeSearch()
    invalidationRefreshPending = false
    if (largeDirectoryRefreshTimer !== null) window.clearTimeout(largeDirectoryRefreshTimer)
    largeDirectoryRefreshTimer = null
  }
})

watch(workspace.preferences, preferences => {
  if (preferences && preferencesLoaded) userPreferences.value = { ...preferences }
})

watch(() => props.elevated, () => {
  if (!navigationReady) return
  hashFileRequest++
  closeSearch()
  stopRealtime()
  if (props.paneVisible) startRealtime(currentPath.value)
  queuedVideoThumbnailVersions.clear()
  thumbnailJobs.value = []
  void refresh(true)
  // The new connection reloads task and thumbnail state for its worker.
})

watch([viewMode, gridSize], () => {
  if (preferencesLoaded) void savePreferences()
  void nextTick(scrollFocusedIntoView)
})

watch(showHidden, () => { if (preferencesLoaded) void savePreferences() })

watch([selectedPaths, focusedPath, detailsVisible, detailsPath], storeSessionExplorerState, { flush: 'sync' })
watch([selectedPaths, focusedPath], () => {
  if (selectedPaths.value.size || focusedPath.value) currentFolderSelected.value = false
}, { flush: 'sync' })
watch([workspace.split, viewingSearch], () => { currentFolderSelected.value = false })

onBeforeUnmount(() => {
  workspace.panes.delete(props.paneId)
  explorerDisposed = true
  if (invalidateTimer !== null) window.clearTimeout(invalidateTimer)
  if (sizeFreshnessTimer !== null) window.clearInterval(sizeFreshnessTimer)
  if (sizeRefreshPoll !== null) window.clearTimeout(sizeRefreshPoll)
  if (terminalRefreshTimer !== null) window.clearTimeout(terminalRefreshTimer)
  if (largeDirectoryRefreshTimer !== null) window.clearTimeout(largeDirectoryRefreshTimer)
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
  rememberHashLocation()
}

function rememberHashLocation() {
  acceptedHashLocation = { url: window.location.href, state: window.history.state }
}

function restoreHashLocation() {
  if (!acceptedHashLocation) return
  const targetPosition = acceptedHashLocation.state?.webfilePosition
  const currentPosition = window.history.state?.webfilePosition
  if (typeof targetPosition === 'number' && typeof currentPosition === 'number' && targetPosition !== currentPosition) {
    historyRestorePending = true
    window.history.go(targetPosition - currentPosition)
  } else {
    window.history.replaceState(acceptedHashLocation.state, '', acceptedHashLocation.url)
  }
}

async function scrollBreadcrumbToEnd() {
  await nextTick()
  if (breadcrumbScroll.value) breadcrumbScroll.value.scrollLeft = breadcrumbScroll.value.scrollWidth
}

async function loadPreferences() {
  try {
    const preferences = workspace.preferences.value ?? normalizePreferences(await api.preferences())
    if (!workspace.preferences.value) workspace.preferences.value = preferences
    applyPreferences(workspace.preferences.value ?? preferences)
  } catch {
    // Preferences are non-critical; browsing should still work.
  } finally {
    preferencesLoaded = true
  }
}

function persistPreferences(preferences: UserPreferences) {
  return workspace.savePreferences({ ...preferences })
}

async function savePreferences() {
  try {
    userPreferences.value = currentPreferences()
    await persistPreferences(userPreferences.value)
  } catch {
    // Do not interrupt file work for a preference persistence failure.
  }
}

function currentPreferences(): UserPreferences {
  return {
    ...userPreferences.value,
    sort_field: sortField.value,
    sort_direction: sortDirection.value,
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
  sortField.value = preferences.sort_field
  sortDirection.value = preferences.sort_direction
  viewMode.value = preferences.view_mode
  gridSize.value = preferences.grid_size
  showHidden.value = preferences.show_hidden
  setTheme(preferences.theme)
  preferencesLoaded = wasLoaded
}

async function saveSettings(preferences: UserPreferences) {
  if (settingsSaving.value) return
  settingsSaving.value = true
  try {
    const saved = normalizePreferences(await persistPreferences(preferences))
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
  if (historyRestorePending) {
    if (window.location.href !== acceptedHashLocation?.url) return
    historyRestorePending = false
  }
  const request = ++hashFileRequest
  const path = readHashPath()
  const internalPath = path ? toInternalPath(path) : null
  const pathChanged = Boolean(internalPath && internalPath !== currentPath.value)
  const file = readInternalHashFile()
  if (editorVisible.value && editorDialog.value && (pathChanged || file !== editorDialog.value.currentPath)) {
    if (!editorDialog.value.confirmDiscard()) {
      restoreHashLocation()
      return
    }
    editorVisible.value = false
  }
  rememberHashLocation()
  if (pathChanged && internalPath) {
    currentPath.value = internalPath
  }
  if (path && internalPath && path !== toVisiblePath(internalPath)) {
    writePathHash(internalPath, true, file)
  }
  if (pathChanged && parentHistoryNavigationPending) queueMicrotask(finishParentHistoryNavigation)
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

async function refreshInvalidatedDirectory() {
  invalidationRefreshPending = true
  if (uploadBatchFolders.has(currentPath.value)) return
  if (invalidationRefreshRunning) return
  invalidationRefreshRunning = true
  try {
    while (invalidationRefreshPending && !explorerDisposed) {
      // Leave time for interaction between expensive full-directory rereads.
      // Explicit Refresh still bypasses this background-only coalescing.
      const delay = nextLargeDirectoryRefresh - performance.now()
      if (entries.value.length >= largeDirectoryThreshold && delay > 0) {
        if (largeDirectoryRefreshTimer === null) largeDirectoryRefreshTimer = window.setTimeout(() => {
          largeDirectoryRefreshTimer = null
          void refreshInvalidatedDirectory()
        }, delay)
        break
      }
      invalidationRefreshPending = false
      await refresh(false)
    }
  } finally {
    invalidationRefreshRunning = false
  }
}

function directorySnapshot() {
  if (!listingShareable || !props.paneVisible || !watchConnected.value || loading.value || error.value || loadedDirectoryPath !== currentPath.value || loadedDirectoryElevated !== props.elevated || viewingSearch.value || invalidationRefreshPending || invalidationRefreshRunning || invalidateTimer !== null || terminalRefreshTimer !== null || sizeRefreshPoll !== null) return null
  return { path: currentPath.value, entries: entries.value, singleRootPath: singleRootPath.value }
}

function refresh(showSpinner: boolean, reuseListing = false): Promise<boolean> {
  if (viewingSearch.value) return Promise.resolve(true)
  if (showSpinner) {
    rememberDirectoryScroll(currentPath.value)
    pendingScrollRestorePath = currentPath.value
    invalidationRefreshPending = false
    if (largeDirectoryRefreshTimer !== null) window.clearTimeout(largeDirectoryRefreshTimer)
    largeDirectoryRefreshTimer = null
  }
  const request = ++refreshRequest
  const requestedPath = currentPath.value
  const promise = loadDirectory(request, requestedPath, showSpinner, reuseListing)
  latestDirectoryRefresh = { path: requestedPath, promise }
  return promise
}

function waitForLatestDirectoryRefresh(path: string): Promise<boolean> {
  if (explorerDisposed || path !== currentPath.value || viewingSearch.value || latestDirectoryRefresh?.path !== path) return Promise.resolve(false)
  return latestDirectoryRefresh.promise
}

async function loadDirectory(request: number, requestedPath: string, showSpinner: boolean, reuseListing = false): Promise<boolean> {
  if (sizeRefreshPoll !== null) window.clearTimeout(sizeRefreshPoll)
  sizeRefreshPoll = null
  if (showSpinner) loading.value = true
  error.value = ''
  try {
    const shared = reuseListing ? otherPane.value?.snapshot() : null
    const result = shared?.path === requestedPath ? { entries: shared.entries, single_root_path: shared.singleRootPath } : await api.list(requestedPath)
    // Callers that select or open an entry must wait until the owning read has
    // applied its listing, even when their own request was superseded.
    if (request !== refreshRequest) return waitForLatestDirectoryRefresh(requestedPath)
    if (explorerDisposed || requestedPath !== currentPath.value || viewingSearch.value) return false
    singleRootPath.value = result.single_root_path ?? ''
    if (requestedPath === '/' && singleRootPath.value && navigationReady) {
      navigateTo(singleRootPath.value, true)
      return true
    }
    entries.value = result.entries
    loadedDirectoryPath = requestedPath
    loadedDirectoryElevated = props.elevated
    listingShareable = props.paneVisible
    nextLargeDirectoryRefresh = result.entries.length >= largeDirectoryThreshold ? performance.now() + largeDirectoryRefreshInterval : 0
    if (result.entries.some(entry => entry.size_refreshing)) {
      sizeRefreshPoll = window.setTimeout(() => {
        sizeRefreshPoll = null
        if (!explorerDisposed && currentPath.value === requestedPath && !viewingSearch.value) void refreshInvalidatedDirectory()
      }, 500)
    }
    const existing = entryPositions.value
    selectedPaths.value = new Set([...selectedPaths.value].filter(path => existing.has(path)))
    if (focusedPath.value && !existing.has(focusedPath.value)) clearFocus(requestedPath)
    if (detailsVisible.value && detailsPath.value !== requestedPath && !existing.has(detailsPath.value)) detailsPath.value = requestedPath
    storeSessionPath(requestedPath)
    return true
  } catch (err) {
    if (request !== refreshRequest) return waitForLatestDirectoryRefresh(requestedPath)
    if (!explorerDisposed && requestedPath === currentPath.value) error.value = messageOf(err)
    return false
  } finally {
    if (request === refreshRequest) {
      loading.value = false
      await nextTick()
      if (request === refreshRequest && requestedPath === currentPath.value && pendingScrollRestorePath === requestedPath && !viewingSearch.value && !error.value) {
        pendingScrollRestorePath = ''
        const position = directoryScrollPositions.get(requestedPath) ?? null
        const view = viewMode.value === 'list' ? listView.value : gridView.value
        await view?.restoreScrollPosition(position)
      }
    }
  }
}

function navigateTo(path: string, replaceHistory = false) {
  const wasSearch = viewingSearch.value
  closeSearch()
  if (wasSearch) { clearSelection(); clearFocus(); search.value = '' }
  if (wasSearch && path === currentPath.value) { void refresh(true); return }
  if (path === currentPath.value) return
  hashFileRequest++
  clearParentNavigationQueue()
  currentPath.value = path
  writePathHash(path, replaceHistory)
}

function navigateHistoryAware(path: string): boolean {
  if (viewingSearch.value) { navigateTo(path); return false }
  if (path === currentPath.value) return false
  if (!workspace.split.value && goToPathInHistory(path)) return true
  navigateTo(path)
  return false
}

function navigateBreadcrumb(path: string) {
  clearParentNavigationQueue()
  if (workspace.split.value && path === currentPath.value && path !== '/' && !viewingSearch.value) {
    selectCurrentFolder()
    return
  }
  navigateHistoryAware(path)
}

function showOperationTerminalToast(operation: FileOperation) {
  if (!isPrimary) return
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
  if (!props.paneVisible) return
  const folder = (path: string) => path.slice(0, path.lastIndexOf('/')) || '/'
  const paths = [...(operation.sources ?? []), operation.destination].filter((path): path is string => Boolean(path))
  if (!paths.some(path => path === currentPath.value || folder(path) === currentPath.value)) return
  if (operation.kind === 'upload' && uploadBatchFolders.has(currentPath.value)) return
  scheduleTerminalRefresh()
}

function scheduleTerminalRefresh() {
  if (terminalRefreshTimer !== null) window.clearTimeout(terminalRefreshTimer)
  terminalRefreshTimer = window.setTimeout(() => {
    terminalRefreshTimer = null
    if (!explorerDisposed) void refreshInvalidatedDirectory()
  }, 120)
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

async function openEntry(entry: FileEntry, input: 'mouse' | 'touch' = 'mouse') {
  if (input === 'touch') {
    if (Date.now() < ignoreTouchNavigationUntil) return
    ignoreTouchNavigationUntil = Date.now() + 600
  }
  if (!canOpenEntry(entry)) return
  if (entry.is_dir) {
    navigateTo(entry.path)
    return
  }
  await openFile(entry.path, true)
}

async function openFile(path: string, addHistory: boolean, request = ++hashFileRequest) {
	const detected = await detectPreview(path)
	if (explorerDisposed || request !== hashFileRequest) return
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
	if (await navigateDetectedPreview(path)) writePathHash(currentPath.value, true, path)
}

function setPreviewVisible(visible: boolean) {
  if (visible) {
    previewVisible.value = true
    return
  }
  if (previewVisible.value && readHashFile()) {
    if (workspace.split.value) { hidePreview(); writePathHash(currentPath.value, true); return }
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
  const index = event.shiftKey ? displayedPositions.value.get(entry.path) ?? -1 : -1
  const anchorIndex = event.shiftKey ? displayedPositions.value.get(anchorPath.value) ?? -1 : -1

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
  const index = event.shiftKey ? displayedPositions.value.get(entry.path) ?? -1 : -1
  const anchor = anchorPath.value || previousFocus || entry.path
  const anchorIndex = event.shiftKey ? displayedPositions.value.get(anchor) ?? -1 : -1

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

function openBackgroundContext(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Element) || target.closest('[data-file-entry], button, input, textarea, select, a')) return
  if (!fileClipboard.value) return
  event.preventDefault()
  activatePane()
  clearSelection()
  clearFocus()
  contextEntry.value = null
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
  currentFolderSelected.value = false
  clearSelectionState()
}

function onFileViewClick(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Element)) return
  if (target.closest('[data-file-entry], button, input, textarea, select, a')) return
  if (!target.closest('[data-file-viewport], [data-empty-list]')) return
  selectCurrentFolder()
}

function selectCurrentFolder() {
  clearSelection()
  clearFocus()
  currentFolderSelected.value = workspace.split.value && !viewingSearch.value && currentPath.value !== '/'
  fileViewHost.value?.focus({ preventScroll: true })
}

function onFileViewPointerDown(event: PointerEvent) {
  if (event.target instanceof Element && event.target.closest('[data-file-entry]')) currentFolderSelected.value = false
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

function applyCachedFolderSize(value: { path: string; size: number; calculatedAt?: string }) {
  entries.value = entries.value.map(entry => entry.path === value.path ? { ...entry, size: value.size, size_status: 'ready', size_calculated_at: value.calculatedAt } : entry)
}

function openNewFolder(event: MouseEvent) {
  // Safari can activate a button without focusing it. Let the dialog retain its opener.
  if (event.currentTarget instanceof HTMLElement) event.currentTarget.focus({ preventScroll: true })
  nameDialogMode.value = 'folder'
  nameDialogVisible.value = true
}

async function onNameDialogSubmit(value: string) {
  if (nameBusy.value) return
  nameBusy.value = true
  try {
    let createdPath = ''
    if (nameDialogMode.value === 'folder') {
      createdPath = joinPath(currentPath.value, value)
      await api.mkdir(createdPath)
    } else if (renameTarget.value) {
      const source = renameTarget.value.path
      const containingFolder = source.slice(0, source.lastIndexOf('/')) || '/'
      await api.rename(source, joinPath(containingFolder, value))
      nameDialogVisible.value = false
      if (searchRequest.value) {
        clearSelection(); clearFocus()
        await runSearch({ ...searchRequest.value })
        return
      }
    }
    nameDialogVisible.value = false
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
  } finally {
    nameBusy.value = false
  }
}

function createDocument(name: string) {
  hashFileRequest++
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
    if (workspace.split.value) { editorVisible.value = false; writePathHash(currentPath.value, true); return }
    // The editor already confirmed this close. Unmount before history events
    // arrive so browser navigation does not prompt for the same discard again.
    editorVisible.value = false
    window.history.back()
    return
  }
  editorVisible.value = false
}

function canHidePane() {
  if (editorVisible.value && editorDialog.value && !editorDialog.value.confirmDiscard()) return false
  if (editorVisible.value) {
    editorVisible.value = false
    writePathHash(currentPath.value, true)
  }
  return true
}

function onEditorSaved(path: string) {
  editorIsNew.value = false
  const folder = path.slice(0, path.lastIndexOf('/')) || '/'
  if (currentPath.value !== folder) {
    clearParentNavigationQueue()
    currentPath.value = folder
  }
  if (readInternalHashFile() !== path) {
    // Close uses Back, so a Save As must leave its containing folder directly
    // behind the new document rather than the previous document's URL.
    if (readHashFile()) writePathHash(folder, true)
    writePathHash(folder, false, path)
  }
  void refresh(false)
}

function openTransfer(mode: TransferMode, items: FileEntry[] = selectedEntries.value) {
  if (items.length === 0 || currentPath.value === '/') return
  transferMode.value = mode
  transferEntries.value = [...items]
  destinationVisible.value = true
}

async function submitTransfer(destination: string) {
  if (transferBusy.value) return
  transferBusy.value = true
  try {
    if (await beginOperation(transferMode.value, transferEntries.value.map(entry => entry.path), destination)) destinationVisible.value = false
  } finally {
    transferBusy.value = false
  }
}

async function transferToOtherPane(mode: TransferMode, items: FileEntry[] = selectedEntries.value) {
  if (!workspace.split.value || !otherPath.value || otherPath.value === '/' || currentPath.value === '/' || !items.length) return
  await beginOperation(mode, items.map(entry => entry.path), otherPath.value)
}

function setFileClipboard(mode: TransferMode, items: FileEntry[] = selectedEntries.value) {
  if (items.length === 0 || currentPath.value === '/') return
  fileClipboard.value = { mode, paths: items.map(item => item.path) }
}

async function pasteClipboard() {
  const clipboard = fileClipboard.value
  if (!clipboard || !canPaste.value) return
  if (await beginOperation(clipboard.mode, clipboard.paths, currentPath.value) && clipboard.mode === 'move') {
    fileClipboard.value = null
  }
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
      if (!await beginOperation('delete', items.map(item => item.path))) return false
      clearSelection()
    },
  })
}

async function beginOperation(kind: 'copy' | 'move' | 'delete' | 'extract' | 'archive', sources: string[], destination?: string) {
  try {
    const copyChunkMiB = kind === 'copy' || kind === 'move' ? userPreferences.value.copy_chunk_mib : undefined
    await startFileOperation(kind, sources, destination, copyChunkMiB)
    signalTaskAdded()
    return true
  } catch (err) {
    showError(err, `Could not start ${kind}`)
    return false
  }
}

async function cancelOperation(id: string) {
  try {
    // Keep cancellation off the HTTP upload connection when realtime is available.
    if (!cancelRealtimeOperation(id)) await api.cancelOperation(id)
    return true
  } catch (err) {
    showError(err, 'Could not cancel operation')
    return false
  }
}

function requestOperationCancellation(operation: FileOperation) {
  const source = operation.sources?.length === 1
    ? operation.sources[0].split('/').pop()
    : `${operation.sources?.length ?? 0} items`
  const task = `${capitalize(operation.kind)} ${source ?? ''}`.trim()
  const cleanupPolicy = operation.cleanup_policy?.trim()
  confirm.require({
    header: `Cancel ${task}?`,
    message: cleanupPolicy || 'The task will stop as soon as possible.',
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Continue running',
    acceptLabel: 'Cancel task',
    acceptProps: { severity: 'danger' },
    accept: () => cancelOperation(operation.id),
  })
}

function requestOperationUndo(operation: FileOperation) {
  confirm.require({
    header: `Undo ${operation.kind}?`,
    message: operation.kind === 'copy'
      ? 'Remove the copied items? Undo checks for later changes and keeps the originals. This action can only be attempted once.'
      : 'Move the transferred items back to their original locations, including changes made inside moved folders? Existing files will be kept. This action can only be attempted once.',
    icon: 'pi pi-undo',
    rejectLabel: 'Cancel',
    acceptLabel: 'Undo',
    accept: async () => {
      try {
        await undoFileOperation(operation.id)
        signalTaskAdded()
      } catch (err) {
        showError(err, 'Could not start undo')
        return false
      }
    },
  })
}

function clearOperationHistory() {
  confirm.require({
    header: 'Clear task history',
    message: 'Clear all completed, failed, cancelled and interrupted tasks? This also removes their Undo actions. Active tasks will be kept.',
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
        return false
      }
    },
  })
}

function removeOperationHistory(operation: FileOperation) {
  confirm.require({
    header: 'Remove task from history',
    message: 'Remove this task from history? This also removes its Undo action. Your files stay in place.',
    icon: 'pi pi-exclamation-triangle',
    rejectLabel: 'Cancel',
    acceptLabel: 'Remove',
    acceptProps: { severity: 'danger' },
    accept: async () => {
      try {
        await removeFinishedOperation(operation.id)
      } catch (err) {
        showError(err, 'Could not remove task from history')
        return false
      }
    },
  })
}

async function download(item: FileEntry) {
  if (item.is_dir) {
    await beginOperation('archive', [item.path])
    return
  }
  downloadURL(api.downloadUrl(item.path), item.name)
}

function downloadOperation(operation: FileOperation) {
  const sourceName = operation.sources?.[0]?.split('/').pop() || 'folder'
  downloadURL(api.operationDownloadUrl(operation.id), `${sourceName}.zip`)
}

function downloadURL(url: string, name: string) {
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}

async function prepareExtraction(destination: string) {
  if (extractBusy.value || extractTargets.value.length === 0) return
  extractDestination.value = destination
  extractBusy.value = true
  try {
    const started = await startExtractions(
      extractTargets.value.map(entry => entry.path),
      extractDestination.value,
      'separate_directories',
      userPreferences.value.extract_existing_files,
      userPreferences.value.extract_delete_archive,
    )
    for (const operation of started) handleOperationUpdate(operation)
    signalTaskAdded()
    clearSelection()
    extractVisible.value = false
  } catch (err) {
    showError(err, 'Could not start extraction')
  } finally {
    extractBusy.value = false
  }
}

async function submitExtractionPassword(password: string) {
  const operationId = extractPasswordOperationId.value
  if (!operationId || passwordBusy.value) return
  extractPasswordError.value = ''
  passwordBusy.value = true
  try {
    await api.submitOperationPassword(operationId, password)
    extractPasswordVisible.value = false
    await loadOperations()
  } catch (err) {
    if (extractPasswordOperationId.value === operationId) {
      extractPasswordVisible.value = true
    }
    showError(err, 'Could not submit archive password')
  } finally {
    passwordBusy.value = false
    restorePasswordPrompt()
  }
}

function handleOperationUpdate(operation: FileOperation) {
  updateOperation(operation)
}

function awaitingPassword(operation: FileOperation) {
  return operation.kind === 'extract' && operation.state === 'awaiting_input' && operation.phase === 'awaiting_password'
}

function passwordRequestKey(operation: FileOperation) {
  return `${operation.id}:${operation.updated_at}:${operation.message}`
}

function restorePasswordPrompt() {
  if (!isPrimary || passwordBusy.value) return
  const waitingOperations = operations.value.filter(awaitingPassword)
  const current = waitingOperations.find(operation => operation.id === extractPasswordOperationId.value)
  // Keep the current archive in focus when another task requests a password.
  if (extractPasswordVisible.value && current) {
    showPasswordPrompt(current)
    return
  }
  const waiting = waitingOperations.find(operation => !shownPasswordRequests.has(passwordRequestKey(operation)))
  if (waiting) showPasswordPrompt(waiting)
  else if (extractPasswordOperationId.value) extractPasswordVisible.value = false
}

// Only accepted store updates may control the dialog. HTTP responses can arrive
// after newer WebSocket events, and updateOperation rejects those stale states.
watch(operations, restorePasswordPrompt)

function showPasswordPrompt(operation: FileOperation) {
  if (!isPrimary) return
  shownPasswordRequests.add(passwordRequestKey(operation))
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

function reopenPasswordPrompt(operation: FileOperation) {
  const current = operations.value.find(item => item.id === operation.id)
  if (current && awaitingPassword(current) && !passwordBusy.value) showPasswordPrompt(current)
}

async function cancelPasswordPrompt() {
  const id = extractPasswordOperationId.value
  extractPasswordVisible.value = false
  extractPasswordError.value = ''
  if (id) await cancelOperation(id)
}

function openExtract(entries: FileEntry[]) {
  extractTargets.value = entries
  extractDestination.value = currentPath.value
  extractVisible.value = true
}

async function extractHere(entries: FileEntry[]) {
  try {
    const started = await startExtractions(entries.map(entry => entry.path), currentPath.value, 'here', userPreferences.value.extract_existing_files, userPreferences.value.extract_delete_archive)
    for (const operation of started) handleOperationUpdate(operation)
    signalTaskAdded()
    clearSelection()
  } catch (err) {
    showError(err, 'Could not start extraction')
  }
}

function reorderOperation(id: string, direction: 'up' | 'down') {
	void reorderFileOperation(id, direction).catch(err => showError(err, 'Could not reorder operation'))
}

async function toggleOperationPause(operation: FileOperation) {
  try { updateOperation(await api.setOperationPaused(operation.id, !operation.paused)) }
  catch (err) { showError(err, operation.paused ? 'Could not resume task' : 'Could not pause task') }
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
  uploadBatchFolders.set(destination, (uploadBatchFolders.get(destination) ?? 0) + 1)
  try {
    const queued: Array<{ file: File; prepared: PreparedUpload }> = []
    for (const file of files) {
      try {
        const prepared = await api.queueUpload(destination, file)
        queued.push({ file, prepared })
        updateOperation(prepared.operation)
        signalTaskAdded()
      } catch (err) {
        await loadOperations()
        showError(err, `Could not upload ${file.name}`)
      }
    }
    await Promise.all(queued.map(({ file, prepared }) => workspace.enqueueUpload(async () => {
      const current = operations.value.find(operation => operation.id === prepared.operation.id)
      if (current && (['cancelled', 'cancelling', 'failed', 'interrupted'].includes(current.state) || current.attempt_id && current.attempt_id !== prepared.operation.attempt_id)) {
        api.forgetPreparedUpload(prepared.attempt)
        return
      }
      try {
        updateOperation(await api.upload(destination, file, updateOperation, prepared))
      } catch (err) {
        await loadOperations()
        const latest = operations.value.find(operation => operation.id === prepared.operation.id)
        if (!latest || !['cancelled', 'cancelling'].includes(latest.state)) showError(err, `Could not upload ${file.name}`)
      }
    })))
  } finally {
    const remaining = (uploadBatchFolders.get(destination) ?? 1) - 1
    if (remaining > 0) uploadBatchFolders.set(destination, remaining)
    else {
      uploadBatchFolders.delete(destination)
      if (destination === currentPath.value) scheduleTerminalRefresh()
    }
  }
}

function dropFilesOnFolder(folder: FileEntry, event: DragEvent) {
  if (!canMutate.value) return
  draggingOver.value = false
  if (workspace.drag.value) { void dropInternal(event, folder.path); return }
  const files = [...(event.dataTransfer?.files ?? [])]
  if (files.length) void uploadFileList(files, folder.path)
}

function onViewDragOver(event: DragEvent) {
  if (!canMutate.value) return
  const folder = (event.target as Element).closest<HTMLElement>('[data-drop-directory]')?.dataset.dropDirectory
  dragDestination.value = folder || currentPath.value
  if (workspace.drag.value && event.dataTransfer?.types.includes(fileDragType)) {
    dragAction.value = dropMode(event)
    const allowed = validDragDestination(dragDestination.value, dragAction.value)
    draggingOver.value = allowed
    event.preventDefault()
    event.dataTransfer.dropEffect = allowed ? dragAction.value : 'none'
    return
  }
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
  if (!next || !host.contains(next)) {
    draggingOver.value = false
    dragDestination.value = ''
    return
  }
  const element = next instanceof Element ? next : next.parentElement
  dragDestination.value = element?.closest<HTMLElement>('[data-drop-directory]')?.dataset.dropDirectory || currentPath.value
  if (workspace.drag.value) draggingOver.value = validDragDestination(dragDestination.value, dragAction.value)
}

function onBackgroundDrop(event: DragEvent) {
  draggingOver.value = false
  if (!canMutate.value) return
  if (workspace.drag.value) { void dropInternal(event, currentPath.value); return }
  const files = [...(event.dataTransfer?.files ?? [])]
  if (files.length) {
    event.preventDefault()
    void uploadFileList(files, currentPath.value)
  }
}

function startEntryDrag(entry: FileEntry, event: DragEvent) {
  if (currentPath.value === '/' || !event.dataTransfer) { event.preventDefault(); return }
  const items = selectedPaths.value.has(entry.path) ? selectedEntries.value : [entry]
  if (!selectedPaths.value.has(entry.path)) replaceSelection([entry.path])
  workspace.drag.value = { token: crypto.randomUUID(), entries: [...items] }
  event.dataTransfer.setData(fileDragType, workspace.drag.value.token)
  event.dataTransfer.effectAllowed = 'copyMove'
}

function endEntryDrag() { workspace.drag.value = null; draggingOver.value = false }

function validDragDestination(destination: string, mode: TransferMode) {
  return !!workspace.drag.value?.entries.length && workspace.drag.value.entries.every(entry =>
    destination !== entry.path && !(entry.is_dir && destination.startsWith(`${entry.path}/`)) &&
    !(mode === 'move' && destination === (entry.path.slice(0, entry.path.lastIndexOf('/')) || '/')))
}

async function dropInternal(event: DragEvent, destination: string) {
  event.preventDefault()
  event.stopPropagation()
  draggingOver.value = false
  const drag = workspace.drag.value
  const mode = dropMode(event)
  if (!drag || event.dataTransfer?.getData(fileDragType) !== drag.token || !validDragDestination(destination, mode)) return
  const paths = drag.entries.map(entry => entry.path)
  workspace.drag.value = null
  await beginOperation(mode, paths, destination)
}

function goUp(input: 'mouse' | 'touch' = 'mouse') {
  if (input === 'touch') {
    if (Date.now() < ignoreTouchNavigationUntil) return
    ignoreTouchNavigationUntil = Date.now() + 600
  }
  if (!parentPath.value) return
  queuedParentNavigations++
  runQueuedParentNavigation()
}

function finishParentHistoryNavigation() {
  if (!parentHistoryNavigationPending) return
  parentHistoryNavigationPending = false
  runQueuedParentNavigation()
}

function clearParentNavigationQueue() {
  parentHistoryNavigationPending = false
  queuedParentNavigations = 0
}

function runQueuedParentNavigation() {
  if (parentHistoryNavigationPending || queuedParentNavigations === 0) return
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
  if (!isActive.value || !props.paneVisible) return
  if (handleDetailsShortcut(event)) return
  if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'f') {
    if (event.shiftKey) {
      if (currentPath.value === '/' || document.querySelector('[role="dialog"], [role="alertdialog"]')) return
      event.preventDefault()
      recursiveSearchVisible.value = true
      return
    }
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
  if (!isActive.value) return
  if (handleDetailsShortcut(event)) {
    event.stopPropagation()
    return
  }
  if (event.target instanceof HTMLButtonElement || event.target instanceof HTMLInputElement) return
  if ((event.ctrlKey || event.metaKey) && !event.altKey) {
    const key = event.key.toLowerCase()
    if ((key === 'c' || key === 'x') && selectedEntries.value.length) {
      event.preventDefault()
      setFileClipboard(key === 'c' ? 'copy' : 'move')
      return
    }
    if (key === 'v' && canPaste.value) {
      event.preventDefault()
      void pasteClipboard()
      return
    }
  }
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
  const current = displayedPositions.value.get(focusedPath.value) ?? -1
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

function rememberDirectoryScroll(path: string) {
  if (!navigationReady || loading.value || viewingSearch.value || loadedDirectoryPath !== path || !props.paneVisible) return
  const view = viewMode.value === 'list' ? listView.value : gridView.value
  const position = view?.captureScrollPosition()
  if (!position) return
  directoryScrollPositions.delete(path)
  directoryScrollPositions.set(path, position)
  if (directoryScrollPositions.size > 128) directoryScrollPositions.delete(directoryScrollPositions.keys().next().value!)
}

function scrollFocusedIntoView() {
  const index = displayedPositions.value.get(focusedPath.value) ?? -1
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

function joinPath(base: string, name: string): string {
  return `${base === '/' ? '' : base}/${name}`.replace(/\/+/g, '/') || '/'
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
  return capitalize(value.replaceAll('_', ' '))
}
</script>

<template>
  <section class="webfile-explorer relative flex h-full min-h-0 flex-col bg-zinc-950" :data-pane="paneId" :data-pane-position="panePosition" :data-active="isActive" :data-current-folder-selected="currentFolderSelected && workspace.split.value ? 'true' : undefined" :aria-label="`${panePosition === 'left' ? 'Left' : 'Right'} pane`" @pointerdown.capture="activatePane" @focusin="activatePane" @dragend="endEntryDrag">
    <Teleport to="#webfile-workspace-toolbar" defer>
    <header v-if="isActive" class="border-b border-zinc-800 bg-zinc-900/90 px-3 py-2 backdrop-blur">
      <div class="flex flex-wrap items-center gap-1">
        <img :src="appPath('favicon.svg')" alt="WebFile" width="32" height="32" class="mr-1 h-6 w-6 shrink-0 sm:mr-2 sm:h-8 sm:w-8" draggable="false" />
        <div class="flex items-center gap-1">
          <Button v-tooltip.bottom="'New folder'" icon="pi pi-folder-plus" size="small" severity="secondary" text :disabled="!canMutate" aria-label="New folder" @click="openNewFolder" />
          <Button v-tooltip.bottom="'New file'" icon="pi pi-file-plus" size="small" severity="secondary" text :disabled="!canMutate" aria-label="New file" @click="newDocumentVisible = true" />
          <Button v-tooltip.bottom="'Upload'" icon="pi pi-upload" size="small" severity="secondary" text :disabled="!canMutate" aria-label="Upload" @click="chooseUpload" />
        </div>
        <span class="webfile-divider mx-1 hidden h-6 w-px sm:block" />
        <div class="flex items-center gap-1">
          <Button v-tooltip.bottom="detailsVisible ? 'Close details' : 'Open details'" icon="pi pi-info-circle" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': detailsVisible }" :aria-label="detailsVisible ? 'Close details' : 'Open details'" :aria-pressed="detailsVisible" @click="toggleDetailsPanel" />
          <Button v-if="selectedEntries.length || fileClipboard" v-tooltip.bottom="'Actions'" icon="pi pi-ellipsis-v" size="small" severity="secondary" text aria-label="Actions" @click="showActionsMenu" />
        </div>

        <span class="min-w-2 flex-1" />
        <Button v-tooltip.bottom="workspace.split.value ? 'Close split view' : 'Split view'" size="small" severity="secondary" text :aria-label="workspace.split.value ? 'Close split view' : 'Split view'" :aria-pressed="workspace.split.value" @click="workspace.toggleSplit()">
          <template #icon>
            <svg class="p-button-icon h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <path d="M12 4v16" />
            </svg>
          </template>
        </Button>
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
        <span class="webfile-divider mx-1 hidden h-6 w-px sm:block" />
        <Button v-tooltip.bottom="`Theme: ${themeMode}`" :icon="themeMode === 'system' ? 'pi pi-palette' : themeMode === 'dark' ? 'pi pi-moon' : 'pi pi-sun'" size="small" severity="secondary" text aria-label="Choose theme" @click="showThemeMenu" />
        <Menu ref="themeMenu" popup :model="themeItems">
          <template #item="{ item, props }">
            <a v-bind="props.action" @click="selectTheme(item.mode)">
              <span :class="item.icon" />
              <span class="ml-2">{{ item.label }}</span>
            </a>
          </template>
        </Menu>
        <span v-if="props.username || props.showLogout" class="webfile-divider mx-1 hidden h-6 w-px sm:block" />
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
          <i class="pi pi-circle-fill text-[0.45rem]" :class="watchConnected ? 'webfile-accent-success' : 'text-zinc-600'" /> live
        </span>
        <span v-if="props.username" class="hidden text-xs text-zinc-400 sm:inline">{{ props.username }}</span>
        <Button v-if="props.showLogout" v-tooltip.bottom="'Sign out'" icon="pi pi-sign-out" severity="secondary" text rounded aria-label="Sign out" @click="emit('logout')" />
        <input ref="uploadInput" class="hidden" type="file" multiple @change="uploadFiles" />
      </div>
    </header>

    </Teleport>

    <SearchDialog v-model:visible="recursiveSearchVisible" :path="currentPath" :show-hidden="showHidden" :initial="searchRequest" @search="startSearch" />
    <div v-if="error" class="webfile-alert-error border-b px-4 py-3 text-sm">{{ error }}</div>

    <div class="webfile-pane-navigation flex shrink-0 flex-wrap items-center gap-2 border-b border-zinc-800 bg-zinc-950 px-3 py-2">
      <nav ref="breadcrumbScroll" class="min-w-0 flex-1 basis-full overflow-x-auto" aria-label="Breadcrumb" data-breadcrumb-scroll :data-single-root="singleRootPath ? 'true' : 'false'">
        <div ref="breadcrumbContent" class="flex min-w-max items-center gap-1 text-sm">
          <template v-for="(crumb, index) in breadcrumbs" :key="crumb.path">
            <i v-if="index" class="pi pi-angle-right text-xs text-zinc-600" />
            <button class="rounded px-2 py-1 text-zinc-300 hover:bg-zinc-800" :aria-current="index === breadcrumbs.length - 1 ? 'page' : undefined" @click="navigateBreadcrumb(crumb.path)">
              {{ crumb.name }}
            </button>
          </template>
        </div>
      </nav>
      <div class="webfile-pane-toolbar flex w-full min-w-0 items-center gap-2">
      <div class="flex shrink-0 items-center gap-1 text-xs text-zinc-400" data-selection-toolbar>
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
        <template v-if="selectionControlsVisible || selectedEntries.length">
          <span class="ml-1 whitespace-nowrap tabular-nums" :aria-label="`${selectedEntries.length} selected`">{{ selectedEntries.length }}<span class="webfile-selection-count-label"> selected</span></span>
          <Button v-tooltip.bottom="'Select all'" class="webfile-pane-secondary" icon="pi pi-list-check" size="small" severity="secondary" text rounded aria-label="Select all" :disabled="filteredEntries.length === 0 || selectedEntries.length === filteredEntries.length" @click="selectAll" />
          <Button v-tooltip.bottom="'Clear selection'" class="webfile-pane-secondary" icon="pi pi-times" size="small" severity="secondary" text rounded aria-label="Clear selection" :disabled="selectedEntries.length === 0" @click="clearSelection" />
        </template>
      </div>
      <div class="ml-auto flex min-w-0 items-center gap-2 overflow-x-auto" data-pane-actions>
      <Button v-tooltip.bottom="'Refresh'" icon="pi pi-refresh" size="small" severity="secondary" text aria-label="Refresh" @click="refreshView" />
      <span class="webfile-pane-secondary h-6 w-px shrink-0 bg-zinc-800" />
      <div class="webfile-pane-secondary flex shrink-0 items-center gap-2">
      <Button v-tooltip.bottom="'Sort files'" icon="pi pi-sort-alt" size="small" severity="secondary" text aria-label="Sort files" @click="sortMenu.toggle($event)" />
      <Menu ref="sortMenu" :model="sortItems" popup />
      <Button v-tooltip.bottom="'Search beneath this folder'" icon="pi pi-search-plus" size="small" severity="secondary" text aria-label="Search beneath this folder" :disabled="currentPath === '/'" @click="recursiveSearchVisible = true" />
      <Button v-tooltip.bottom="filterVisible ? 'Close filter' : 'Filter (Ctrl+F)'" icon="pi pi-search" size="small" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': filterVisible }" :aria-label="filterVisible ? 'Close filter' : 'Open filter'" :aria-pressed="filterVisible" @click="toggleFilter" />
      </div>
      <div class="flex shrink-0 items-center gap-1" data-pane-view-controls>
        <Button v-if="!viewingSearch" v-tooltip.bottom="showHidden ? 'Hide dot files' : 'Show dot files'" icon="pi pi-eye" size="small" severity="secondary" text class="webfile-toggle webfile-pane-secondary" :class="{ 'webfile-toggle-active': showHidden }" :aria-label="showHidden ? 'Hide dot files' : 'Show dot files'" :aria-pressed="showHidden" @click="showHidden = !showHidden" />
        <Button v-tooltip.bottom="'More folder actions'" class="webfile-pane-overflow" icon="pi pi-ellipsis-v" size="small" severity="secondary" text aria-label="More folder actions" @click="paneOverflowMenu.toggle($event)" />
        <Menu ref="paneOverflowMenu" :model="paneOverflowItems" class="max-h-[70vh] overflow-y-auto" popup />
        <span class="inline-flex overflow-hidden rounded-md border border-zinc-700">
          <button v-tooltip.bottom="'List view'" class="px-2.5 py-1.5" :class="viewMode === 'list' ? 'webfile-view-active' : 'text-zinc-400'" aria-label="List view" @click="viewMode = 'list'"><i class="pi pi-list" /></button>
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
    </div>

      <div v-if="filterVisible" class="w-full">
        <InputText ref="filterInput" v-model="search" size="small" fluid :placeholder="viewingSearch ? 'Filter search results' : 'Filter this folder'" :aria-label="viewingSearch ? 'Filter search results' : 'Filter this folder'" @keydown.escape="closeFilter" />
      </div>
    </div>
    <section v-if="searchRequest" class="shrink-0 border-b border-zinc-800 px-4 py-3" aria-label="Search results">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-0"><h2 class="break-words text-sm font-semibold"><i class="pi pi-search mr-2" />Search results for “{{ searchRequest.query }}”</h2><p class="mt-1 break-all text-xs text-zinc-500">Beneath {{ searchRequest.path }} · {{ searchRequest.kind === 'all' ? 'Files and folders' : searchRequest.kind }}<span v-if="searchRequest.wildcard"> · Wildcards</span><span v-if="searchRequest.case_sensitive"> · Match case</span><span v-if="searchRequest.show_hidden"> · Including dotfiles</span></p></div>
        <div class="flex flex-wrap gap-2"><Button v-if="searchBusy" label="Cancel search" size="small" severity="secondary" @click="cancelSearch" /><Button label="Refine search" size="small" severity="secondary" @click="recursiveSearchVisible = true" /><Button label="Back to folder" size="small" severity="secondary" @click="exitSearch" /></div>
      </div>
      <p class="mt-2 text-xs text-zinc-400" role="status"><i v-if="searchBusy" class="pi pi-spinner pi-spin mr-2" />{{ searchMessage }} · {{ searchResults.length }} results · {{ searchScanned }} entries checked<span v-if="searchSkipped"> · {{ searchSkipped }} inaccessible or unavailable entries skipped</span></p>
      <p v-if="searchError" class="webfile-text-error mt-2 text-sm" role="alert">{{ searchError }}</p>
    </section>
    <div class="flex min-h-0 flex-1">
      <div
        ref="fileViewHost"
        class="relative min-h-0 min-w-0 flex-1 outline-none"
        tabindex="0"
        @keydown="onKeyDown"
        @click="onFileViewClick"
        @contextmenu="openBackgroundContext"
        @pointerdown.capture="onFileViewPointerDown"
        @dragover="onViewDragOver"
        @dragleave="onViewDragLeave"
        @drop="onBackgroundDrop"
      >
        <div v-if="draggingOver && !folderDropTarget" data-pane-drop-target class="webfile-drop-overlay pointer-events-none absolute inset-3 z-30 grid place-items-center rounded-xl border-2 border-dashed text-sm font-medium">
          {{ dragLabel }}
        </div>

        <div v-if="loading" class="grid h-full place-items-center"><ProgressSpinner class="h-10 w-10" /></div>
        <div v-else-if="(search || viewingSearch) && filteredEntries.length === 0" data-empty-list class="grid h-full place-items-center p-8 text-center text-zinc-500">
          <div><i class="pi pi-search mb-3 text-4xl" /><p>{{ searchBusy ? 'Searching for files…' : 'No matching files' }}</p></div>
        </div>
        <div v-else class="flex h-full min-h-0 flex-col">
          <VirtualFileList
            :sort-field="sortField"
            :sort-direction="sortDirection"
            @sort="chooseSort($event)"
            v-if="viewMode === 'list'"
            ref="listView"
            class="min-h-0 flex-1"
            :entries="filteredEntries"
            :selected-paths="[...selectedPaths]"
            :cut-paths="cutPaths"
            :focused-path="focusedPath"
            :selection-controls-visible="selectionControlsVisible"
            :parent-path="viewingSearch ? '' : parentPath"
            :show-location="viewingSearch"
            :compact="workspace.split.value"
            :can-drag="currentPath !== '/'"
            :drop-target-path="folderDropTarget"
            @dragentry="startEntryDrag"
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
            :cut-paths="cutPaths"
            :focused-path="focusedPath"
            :selection-controls-visible="selectionControlsVisible"
            :size="gridSize"
            :thumbnail-pending-paths="[...pendingVideoThumbnailPaths]"
            :parent-path="viewingSearch ? '' : parentPath"
            :show-location="viewingSearch"
            :can-drag="currentPath !== '/'"
            :drop-target-path="folderDropTarget"
            @dragentry="startEntryDrag"
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
      <DetailsDialog :visible="detailsVisible && paneVisible" @update:visible="detailsVisible = $event" :path="detailsPath" @error="showError($event, 'Could not load details')" @size-ready="applyCachedFolderSize" />
    </div>

    <ContextMenu ref="contextMenu" :model="contextItems" :base-z-index="1100" :pt="{ transition: { css: false } }" @hide="onContextMenuHide" />
    <Menu ref="actionsMenu" popup :model="contextItems" />

    <NewDocumentDialog v-model:visible="newDocumentVisible" @create="createDocument" />
    <NameDialog
      v-model:visible="nameDialogVisible"
      :busy="nameBusy"
      :title="nameDialogMode === 'folder' ? 'New folder' : 'Rename'"
      :label="nameDialogMode === 'folder' ? 'Folder name' : 'New name'"
      :initial-value="nameDialogMode === 'rename' ? renameTarget?.name : ''"
      :submit-label="nameDialogMode === 'folder' ? 'Create' : 'Rename'"
      @submit="onNameDialogSubmit"
    />
    <ConflictDialog v-if="isPrimary" v-model:visible="conflictVisible" :operation="conflictOperation" :busy="conflictBusy" :error="conflictError" @submit="resolveConflict" @cancel="cancelConflictTask" />
    <SettingsDialog v-if="isPrimary" v-model:visible="settingsVisible" :preferences="currentPreferences()" :saving="settingsSaving" @save="saveSettings" />
    <EditorDialog v-if="editorVisible && paneVisible" ref="editorDialog" :visible="editorVisible" :active="isActive && paneVisible" :path="editorPath" :is-new="editorIsNew" :theme="resolvedTheme" :preferences="userPreferences" @update:visible="setEditorVisible" @saved="onEditorSaved" />
    <FilePreview :visible="previewVisible && paneVisible" :active="isActive" :path="previewPath" :kind="previewKind" :content-type="previewContentType" :folder-path="currentPath" :entries="filteredEntries" @update:visible="setPreviewVisible" @return-to-folder="returnPreviewToFolder" @navigate="navigatePreview" />
    <ExtractDialog v-model:visible="extractVisible" :archive-name="extractTargets.length === 1 ? extractTargets[0].name : `${extractTargets.length} archives`" :archive-names="extractTargets.map(entry => entry.name)" :destination="extractDestination" :busy="extractBusy" :existing-files="userPreferences.extract_existing_files" :show-hidden="showHidden" :hide-inaccessible="userPreferences.hide_inaccessible" :delete-archive="userPreferences.extract_delete_archive" @extract="prepareExtraction" />
    <ArchivePasswordDialog v-if="isPrimary" v-model:visible="extractPasswordVisible" :archive-name="extractTarget?.name ?? ''" :busy="passwordBusy" :error="extractPasswordError" @submit="submitExtractionPassword" @dismiss="extractPasswordVisible = false" @cancel="cancelPasswordPrompt" @after-hide="onArchivePasswordAfterHide" />
    <DestinationDialog
      v-model:visible="destinationVisible"
      :busy="transferBusy"
      :mode="transferMode"
      :count="transferEntries.length"
      :initial-path="currentPath"
      :show-hidden="showHidden"
      :hide-inaccessible="userPreferences.hide_inaccessible"
      @submit="submitTransfer"
    />
    <OperationsPanel v-if="isPrimary" v-model:visible="operationsVisible" :operations="operations" :thumbnail-jobs="thumbnailJobs" @undo="requestOperationUndo" @pause="toggleOperationPause" @cancel="requestOperationCancellation" @password="reopenPasswordPrompt" @resolve="openConflict" @reorder="reorderOperation" @cancel-thumbnail="cancelThumbnailJob" @clear-history="clearOperationHistory" @remove-history="removeOperationHistory" @download="downloadOperation" />
  </section>
</template>
