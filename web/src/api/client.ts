import type {
  DirectoryResponse,
  FileContent,
  FileDetails,
  FileEntry,
  FileOperation,
  Preferences,
  UploadSession,
  VideoThumbnailJob,
  SearchRequest,
  SearchUpdate,
  ConflictDecision,
} from '@/types/files'
import { appPath } from '@/base'

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
  ) {
    super(message)
  }
}

let csrfToken = ''

function setCSRF(value?: string) {
  csrfToken = value ?? ''
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  const method = (init?.method ?? 'GET').toUpperCase()
  if (csrfToken && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    headers.set('X-WebFile-CSRF', csrfToken)
  }
  const response = await fetch(appPath(url), { ...init, headers })
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    let code: string | undefined
    try {
      const body = await response.json() as { error?: string; code?: string }
      message = body.error ?? message
      code = body.code
    } catch {
      // Keep the HTTP status message.
    }
    throw new ApiError(message, response.status, code)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json() as Promise<T>
}

function withPath(endpoint: string, path: string): string {
  const query = new URLSearchParams({ path })
  return `${endpoint}?${query}`
}

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

export interface SessionUser {
  username: string
  uid: number
  gid: number
  home: string
}

export interface SessionStatus {
  authenticated: boolean
  mode: 'none' | 'pam' | 'proxy'
  user?: SessionUser
  csrf_token?: string
  elevation_available?: boolean
  elevated?: boolean
  elevation_expires_at?: string
  logout_url?: string
}

export interface HealthStatus {
  ok: boolean
  version: string
  commit: string
  build_date: string
}

export interface ArchivePreviewEntry {
  path: string
  is_dir: boolean
  size: number
  modified?: string
  encrypted: boolean
}

interface UploadInitResponse {
  upload: UploadSession
  operation: FileOperation
}

export interface PreparedUpload extends UploadInitResponse {
  attempt: string
}

interface UploadChunkResponse extends UploadInitResponse {
  complete: boolean
}

const uploadFailureMessage = 'The transfer was interrupted. Please try again to resume it.'
const failureStorageKey = appPath('pending-upload-failures-v1')
const queuedStorageKey = appPath('prepared-uploads-v1')
type PendingUploadFailure = { id: string; attempt_id: string }
const pendingUploadFailures = new Map<string, PendingUploadFailure>()
const preparedUploads = new Map<string, PendingUploadFailure>()
const reportingUploadFailures = new Map<string, Promise<FileOperation | undefined>>()
const failureKey = (failure: PendingUploadFailure) => failure.attempt_id
try {
  for (const storageKey of [failureStorageKey, queuedStorageKey]) {
    const saved: unknown = JSON.parse(sessionStorage.getItem(storageKey) ?? '[]')
    if (Array.isArray(saved)) for (const item of saved) {
      if (item && typeof item.id === 'string' && typeof item.attempt_id === 'string') pendingUploadFailures.set(failureKey(item), item)
    }
  }
  saveUploadFailures()
  sessionStorage.removeItem(queuedStorageKey)
} catch { /* Storage may be unavailable; keep reports in memory. */ }

function savePreparedUploads() {
  try { sessionStorage.setItem(queuedStorageKey, JSON.stringify([...preparedUploads.values()])) } catch { /* Keep metadata in memory. */ }
}

function forgetPreparedUpload(attempt: string) {
  preparedUploads.delete(attempt)
  savePreparedUploads()
}

function abandonUploads() {
  for (const upload of preparedUploads.values()) pendingUploadFailures.set(failureKey(upload), upload)
  saveUploadFailures()
  for (const upload of [...preparedUploads.values()].slice(0, 8)) void reportUploadFailure(upload)
}
window.addEventListener('pagehide', abandonUploads)

function saveUploadFailures() {
  try { sessionStorage.setItem(failureStorageKey, JSON.stringify([...pendingUploadFailures.values()])) } catch { /* Keep reports in memory. */ }
}

function failedUpload(operation: FileOperation): FileOperation {
  return { ...operation, state: 'failed', phase: 'failed', message: 'Upload stopped', error: uploadFailureMessage,
    cancellable: false, finished_at: operation.updated_at, client_failure: true }
}

function reconcileUploadFailure(operation: FileOperation): FileOperation {
  return operation.kind === 'upload' && ['queued', 'running'].includes(operation.state) && operation.attempt_id
    && pendingUploadFailures.has(failureKey({ id: operation.id, attempt_id: operation.attempt_id })) ? failedUpload(operation) : operation
}

function reportUploadFailure(failure: PendingUploadFailure): Promise<FileOperation | undefined> {
  const key = failureKey(failure)
  const existing = reportingUploadFailures.get(key)
  if (existing) return existing
  const reporting = (async () => {
    try {
      const operation = await request<FileOperation>('/api/fs/uploads/failure', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(failure), signal: AbortSignal.timeout(2000), keepalive: true,
      })
      pendingUploadFailures.delete(key)
      saveUploadFailures()
      return operation
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        pendingUploadFailures.delete(key)
        saveUploadFailures()
      }
      // The proxy may reject this request too. Retry during task reconciliation.
      return undefined
    } finally { reportingUploadFailures.delete(key) }
  })()
  reportingUploadFailures.set(key, reporting)
  return reporting
}

export const api = {
  previewArchive(path: string, password: string, signal?: AbortSignal) {
    return request<{ entries: ArchivePreviewEntry[] }>('/api/fs/archive/preview', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, password }), signal,
    })
  },
  abandonUploads,
  forgetPreparedUpload,
  reconcileUploadFailure,
  hasPendingUploadFailures: () => pendingUploadFailures.size > 0,
  resolveConflict(id: string, decision: ConflictDecision) {
    return request<FileOperation>(`/api/fs/operations/${encodeURIComponent(id)}/input`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(decision),
    })
  },
  async search(input: SearchRequest, update: (value: SearchUpdate) => void, signal: AbortSignal) {
    const response = await fetch(appPath('/api/fs/search'), {
      method: 'POST', signal,
      headers: { 'Content-Type': 'application/json', 'X-WebFile-CSRF': csrfToken },
      body: JSON.stringify(input),
    })
    if (!response.ok) {
      const body = await response.json().catch(() => ({}))
      throw new ApiError(body.error ?? 'Search failed', response.status)
    }
    if (!response.body) throw new Error('Streaming search is unavailable')
    const reader = response.body.getReader(), decoder = new TextDecoder()
    let pending = '', finished = false
    try {
      while (true) {
        const chunk = await reader.read()
        pending += decoder.decode(chunk.value, { stream: !chunk.done })
        let end: number
        while ((end = pending.indexOf('\n')) >= 0) {
          const line = pending.slice(0, end); pending = pending.slice(end + 1)
          if (!line.trim()) continue
          const value = JSON.parse(line) as SearchUpdate
          if (value.error) throw new Error(value.error)
          update(value); finished ||= value.done
        }
        if (chunk.done) break
      }
      if (!finished) throw new Error('Search connection ended before completion; results are partial')
    } finally { await reader.cancel().catch(() => undefined); reader.releaseLock() }
  },
  health() {
    return request<HealthStatus>('/api/health')
  },

  async session() {
    const status = await request<SessionStatus>('/api/session')
    setCSRF(status.csrf_token)
    return status
  },

  async login(username: string, password: string) {
    const status = await request<SessionStatus>('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
    setCSRF(status.csrf_token)
    return status
  },

  async logout() {
    await request<void>('/api/logout', { method: 'POST' })
    setCSRF('')
  },

  async elevate(password: string) {
    const status = await request<SessionStatus>('/api/elevation', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
    setCSRF(status.csrf_token)
    return status
  },

  dropElevation() {
    return request<void>('/api/elevation', { method: 'DELETE' })
  },

  async list(path: string): Promise<DirectoryResponse> {
    const response = await request<Omit<DirectoryResponse, 'entries'> & { entries?: FileEntry[] | null }>(withPath('/api/fs/dir', path))
    return { ...response, entries: arrayOrEmpty(response.entries) }
  },

  details(path: string, refresh = true, signal?: AbortSignal, scan?: string) {
    const url = new URL(withPath('/api/fs/details', path), window.location.origin)
    url.searchParams.set('refresh', String(refresh))
    if (scan) url.searchParams.set('scan', scan)
    return request<FileDetails>(`${url.pathname}${url.search}`, { signal })
  },

  cancelDetails(scan: string) {
    return request<void>(`/api/fs/details?${new URLSearchParams({ scan })}`, { method: 'DELETE' })
  },

  chmod(path: string, mode: string) {
    return request<{ ok: boolean }>('/api/fs/chmod', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, mode }),
    })
  },

  chown(path: string, owner: string, group: string) {
    return request<{ ok: boolean }>('/api/fs/chown', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, owner, group }),
    })
  },

  readFile(path: string) {
    return request<FileContent>(withPath('/api/fs/file', path))
  },

  contentType(path: string, signal?: AbortSignal) {
    return request<{ path: string; content_type: string }>(withPath('/api/fs/content-type', path), { signal })
  },

  saveFile(path: string, content: string, options?: { etag?: string; createOnly?: boolean; force?: boolean }) {
    const headers = new Headers({ 'Content-Type': 'text/plain; charset=utf-8' })
    if (!options?.force) {
      if (options?.createOnly) headers.set('If-None-Match', '*')
      else if (options?.etag) headers.set('If-Match', options.etag)
    }
    return request<FileContent>(withPath('/api/fs/file', path), {
      method: 'PUT',
      headers,
      body: content,
    })
  },

  mkdir(path: string) {
    return request<{ ok: boolean }>('/api/fs/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    })
  },

  rename(from: string, to: string) {
    return request<{ ok: boolean }>('/api/fs/rename', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ from, to }),
    })
  },

  async queueUpload(dir: string, file: File): Promise<PreparedUpload> {
    const attempt = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
    const failure = { id: '', attempt_id: attempt }
    preparedUploads.set(attempt, failure)
    savePreparedUploads()
    try {
      const initial = await request<UploadInitResponse>('/api/fs/uploads/init', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, name: file.name, size: file.size, last_modified: file.lastModified, attempt_id: attempt, queued: true }),
      })
      failure.id = initial.operation.id
      savePreparedUploads()
      return { ...initial, attempt }
    } catch (error) {
      pendingUploadFailures.set(attempt, failure)
      saveUploadFailures()
      forgetPreparedUpload(attempt)
      await reportUploadFailure(failure)
      throw error
    }
  },

  async upload(dir: string, file: File, onProgress?: (operation: FileOperation) => void, prepared?: PreparedUpload): Promise<FileOperation> {
    const attempt = prepared?.attempt ?? Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
    let operation: FileOperation | undefined
    try {
      const initial = prepared ?? await request<UploadInitResponse>('/api/fs/uploads/init', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, name: file.name, size: file.size, last_modified: file.lastModified, attempt_id: attempt }),
      })
      let upload = initial.upload
      operation = initial.operation
      let currentOperation = initial.operation
      onProgress?.(operation)
      const chunkSize = 8 * 1024 * 1024
      // Even empty files and fully staged resumes need one publication request.
      for (;;) {
        if (currentOperation.paused) {
          await new Promise(resolve => setTimeout(resolve, 1000))
          const status = await request<{ operations: FileOperation[] }>('/api/fs/operations')
          const latest = status.operations.find(item => item.id === currentOperation.id)
          if (!latest || !['queued', 'running'].includes(latest.state) || latest.attempt_id !== currentOperation.attempt_id) throw new Error('Upload is no longer active')
          currentOperation = latest
          onProgress?.(latest)
          continue
        }
        const end = Math.min(upload.offset + chunkSize, upload.size)
        const query = new URLSearchParams({ id: upload.id, offset: String(upload.offset), attempt_id: currentOperation.attempt_id ?? attempt })
        let result: UploadChunkResponse
        try {
          result = await request<UploadChunkResponse>(`/api/fs/uploads/chunk?${query}`, {
            method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: file.slice(upload.offset, end),
          })
        } catch (error) {
          if (error instanceof ApiError && error.code === 'operation_paused') {
            currentOperation = { ...currentOperation, paused: true }
            onProgress?.(currentOperation)
            continue
          }
          throw error
        }
        upload = result.upload
        operation = result.operation
        currentOperation = result.operation
        onProgress?.(currentOperation)
        if (upload.offset >= upload.size) break
      }
      return currentOperation
    } catch (error) {
      const failure = { id: operation?.id ?? '', attempt_id: operation?.attempt_id ?? attempt }
      pendingUploadFailures.set(failureKey(failure), failure)
      saveUploadFailures()
      if (operation) onProgress?.(failedUpload({ ...operation, attempt_id: operation.attempt_id ?? attempt }))
      const reported = await reportUploadFailure(failure)
      if (reported) onProgress?.(reported)
      throw error
    } finally {
      if (prepared) forgetPreparedUpload(attempt)
    }
  },

  startOperation(kind: 'copy' | 'move' | 'delete' | 'extract' | 'archive', sources: string[], destination?: string, copyChunkMiB?: number) {
    return request<FileOperation>('/api/fs/operations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind, sources, destination, copy_chunk_mib: copyChunkMiB }),
    })
  },

  startExtractions(sources: string[], destination: string, layout: 'separate_directories' | 'here', existingFiles: 'skip' | 'overwrite', deleteArchive: boolean) {
    return request<{ operations: FileOperation[] }>('/api/fs/operations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        kind: 'extract',
        sources,
        destination,
        extract_layout: layout,
        existing_files: existingFiles,
        delete_archive: deleteArchive,
      }),
    })
  },

  async operations(): Promise<{ operations: FileOperation[] }> {
    await Promise.all([...pendingUploadFailures.values()].slice(0, 8).map(reportUploadFailure))
    const response = await request<{ operations?: FileOperation[] | null }>('/api/fs/operations')
    return { operations: arrayOrEmpty(response.operations).map(reconcileUploadFailure) }
  },

  undoOperation(id: string) {
    return request<FileOperation>(`/api/fs/operations/${encodeURIComponent(id)}/undo`, { method: 'POST' })
  },

  cancelOperation(id: string) {
    return request<{ ok: boolean }>('/api/fs/operations/cancel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    })
  },

  setOperationPaused(id: string, paused: boolean) {
    return request<FileOperation>(`/api/fs/operations/${encodeURIComponent(id)}/${paused ? 'pause' : 'resume'}`, { method: 'POST' })
  },

  reorderOperation(id: string, direction: 'up' | 'down') {
    return request<FileOperation>(`/api/fs/operations/${encodeURIComponent(id)}/reorder`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ direction }),
    })
  },

  clearOperationHistory() {
    return request<{ removed: number }>('/api/fs/operations/history', {
      method: 'DELETE',
    })
  },

  removeOperationHistory(id: string) {
    return request<{ removed: boolean }>(`/api/fs/operations/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
  },

  extract(path: string, destination: string) {
    return this.startOperation('extract', [path], destination)
  },

  submitOperationPassword(id: string, password: string) {
    return request<{ ok: boolean }>(`/api/fs/operations/${encodeURIComponent(id)}/password`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
  },

  preferences() {
    return request<Preferences>('/api/preferences')
  },

  savePreferences(preferences: Preferences) {
    return request<Preferences>('/api/preferences', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(preferences),
    })
  },

  downloadUrl(path: string) {
    return appPath(withPath('/api/fs/download', path))
  },

  operationDownloadUrl(id: string) {
    return appPath(`/api/fs/operations/${encodeURIComponent(id)}/download`)
  },

  previewUrl(path: string) {
    return appPath(`${withPath('/api/fs/download', path)}&inline=true`)
  },

  thumbnailUrl(path: string, width = 320, height = 320, version?: string) {
    const query = new URLSearchParams({ path, width: String(width), height: String(height) })
    if (version !== undefined) query.set('version', version)
    return appPath(`/api/fs/thumbnail?${query}`)
  },

  async requestThumbnail(path: string, width = 320, height = 320): Promise<{ url?: string; job?: VideoThumbnailJob }> {
    const response = await fetch(this.thumbnailUrl(path, width, height))
    if (response.status === 202) {
      return response.json() as Promise<{ job: VideoThumbnailJob }>
    }
    if (!response.ok) throw new ApiError(`${response.status} ${response.statusText}`, response.status)
    return { url: URL.createObjectURL(await response.blob()) }
  },

  queueVideoThumbnails(paths: string[], width = 320, height = 320) {
    return request<{ jobs: VideoThumbnailJob[] }>('/api/fs/thumbnails/queue', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths, width, height }),
    })
  },

  async thumbnailJobs(): Promise<{ jobs: VideoThumbnailJob[] }> {
    const response = await request<{ jobs?: VideoThumbnailJob[] | null }>('/api/fs/thumbnails/jobs')
    return { jobs: arrayOrEmpty(response.jobs) }
  },

  cancelThumbnailJob(id: string) {
    return request<{ ok: boolean }>(`/api/fs/thumbnails/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
}
