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
}

export interface DirectoryResponse {
  path: string
  entries: FileEntry[]
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
  view_mode?: 'list' | 'grid'
  grid_size?: 'small' | 'medium' | 'large'
  show_hidden?: boolean
  theme?: 'system' | 'dark' | 'light'
}

export interface Favourite {
  path: string
  label?: string
  created_at: string
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
  id: string
  kind: 'copy' | 'move' | 'trash' | 'extract' | 'upload' | string
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
  cancellable: boolean
  cleanup_policy?: string
  cleanup_error?: string
  started_at: string
  updated_at: string
  finished_at?: string
}

export type VideoThumbnailState = 'queued' | 'probing' | 'generating' | 'publishing' | 'ready' | 'failed' | 'cancelled'

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

export interface TrashItem {
  id: string
  name: string
  original_path: string
  deleted_at: string
  is_dir: boolean
  size: number
}
