export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  size_status?: 'ready'
  modified: string
  mode: string
  uid?: number
  gid?: number
  can_navigate?: boolean
  can_read?: boolean
}

export interface DirectoryResponse {
  path: string
  entries: FileEntry[]
  single_root_path?: string
}

export interface SearchRequest {
  path: string
  query: string
  wildcard: boolean
  case_sensitive: boolean
  kind: 'all' | 'files' | 'folders'
  show_hidden: boolean
}
export interface SearchUpdate {
  entry?: FileEntry
  scanned: number
  skipped: number
  truncated: boolean
  done: boolean
  error?: string
}

export interface FileContent {
  path: string
  content: string
  modified: string
  etag: string
}

export interface FileDetails {
  path: string
  name: string
  is_dir: boolean
  is_symlink: boolean
  size: number
  modified: string
  mode: string
  mode_octal: string
  uid: number
  gid: number
  owner: string
  group: string
  link_target?: string
  size_status?: 'calculating' | 'ready' | 'error'
  size_error?: string
  size_refreshing?: boolean
}

export interface Preferences {
  sort_field?: 'name' | 'modified' | 'size' | 'type'
  sort_direction?: 'asc' | 'desc'
  view_mode?: 'list' | 'grid'
  grid_size?: 'small' | 'medium' | 'large'
  show_hidden?: boolean
  hide_inaccessible?: boolean
  theme?: 'system' | 'dark' | 'light'
  editor_word_wrap?: boolean
  editor_font_size?: number
  editor_line_numbers?: boolean
  editor_show_whitespace?: boolean
  editor_minimap?: boolean
  editor_toolbar?: 'compact' | 'rich'
  editor_markdown_preview?: boolean
  copy_chunk_mib?: 1 | 4 | 16 | 64
  extract_existing_files?: 'skip' | 'overwrite'
}

export interface UploadSession {
  id: string
  path: string
  name: string
  size: number
  offset: number
  updated_at: string
  operation_id: string
}

export type OperationState = 'queued' | 'preparing' | 'awaiting_input' | 'running' | 'cancelling' | 'cleaning_up' | 'completed' | 'failed' | 'cancelled' | 'interrupted'

export interface FileOperation {
  input?: TransferConflict
  id: string
  kind: 'copy' | 'move' | 'delete' | 'extract' | 'archive' | 'upload' | string
  sources?: string[]
  destination?: string
  state: OperationState
  phase?: string
  message?: string
  error?: string
  progress_done: number
  progress_total: number
  progress_unit?: 'bytes' | 'items' | 'percent'
  bytes_done: number
  bytes_total: number
  bytes_per_second?: number | null
  items_done: number
  items_total: number
  queue_position?: number
  cancellable: boolean
  cleanup_policy?: string
  cleanup_error?: string
  started_at: string
  updated_at: string
  finished_at?: string
}

export interface TransferConflict {
  revision: string
  source: FileEntry
  destination: FileEntry
  category: 'files' | 'folders' | 'type_mismatch'
  choices: ConflictChoice[]
  changed: boolean
}
export type ConflictChoice = 'skip' | 'keep_both' | 'replace' | 'merge'
export interface ConflictDecision {
  revision: string
  choice: ConflictChoice
  apply_remaining: boolean
}

export type VideoThumbnailState = 'settling' | 'queued' | 'probing' | 'generating' | 'publishing' | 'ready' | 'failed' | 'cancelled'

export interface VideoThumbnailJob {
  id: string
  path: string
  name: string
  state: VideoThumbnailState
  queue_position?: number
  error?: string
  cancellable: boolean
  started_at?: string
  updated_at: string
  finished_at?: string
}
