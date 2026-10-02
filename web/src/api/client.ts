import type {
  DirectoryResponse,
  FileContent,
  FileDetails,
  FileEntry,
  FileOperation,
  Preferences,
  UploadSession,
  VideoThumbnailJob,
} from '@/types/files'

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
  const response = await fetch(url, { ...init, headers })
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
  mode: 'none' | 'pam'
  user?: SessionUser
  csrf_token?: string
  elevation_available?: boolean
  elevated?: boolean
  elevation_expires_at?: string
}

interface UploadInitResponse {
  upload: UploadSession
  operation: FileOperation
}

interface UploadChunkResponse extends UploadInitResponse {
  complete: boolean
}

export const api = {
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

  details(path: string, refresh = true) {
    const url = new URL(withPath('/api/fs/details', path), window.location.origin)
    url.searchParams.set('refresh', String(refresh))
    return request<FileDetails>(`${url.pathname}${url.search}`)
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

  contentType(path: string) {
    return request<{ path: string; content_type: string }>(withPath('/api/fs/content-type', path))
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

  async upload(dir: string, file: File, onProgress?: (operation: FileOperation) => void): Promise<FileOperation> {
    const initial = await request<UploadInitResponse>('/api/fs/uploads/init', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dir, name: file.name, size: file.size, last_modified: file.lastModified }),
    })
    let upload = initial.upload
    let operation = initial.operation
    onProgress?.(operation)
    const chunkSize = 8 * 1024 * 1024

    if (upload.offset === upload.size) {
      const query = new URLSearchParams({ id: upload.id, offset: String(upload.offset) })
      const finished = await request<UploadChunkResponse>(`/api/fs/uploads/chunk?${query}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: new Blob([]),
      })
      onProgress?.(finished.operation)
      return finished.operation
    }

    while (upload.offset < upload.size) {
      const end = Math.min(upload.offset + chunkSize, upload.size)
      const chunk = file.slice(upload.offset, end)
      const query = new URLSearchParams({ id: upload.id, offset: String(upload.offset) })
      const result = await request<UploadChunkResponse>(`/api/fs/uploads/chunk?${query}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: chunk,
      })
      upload = result.upload
      operation = result.operation
      onProgress?.(operation)
    }
    return operation
  },

  startOperation(kind: 'copy' | 'move' | 'delete' | 'extract', sources: string[], destination?: string, copyChunkMiB?: number) {
    return request<FileOperation>('/api/fs/operations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind, sources, destination, copy_chunk_mib: copyChunkMiB }),
    })
  },

  async operations(): Promise<{ operations: FileOperation[] }> {
    const response = await request<{ operations?: FileOperation[] | null }>('/api/fs/operations')
    return { operations: arrayOrEmpty(response.operations) }
  },

  cancelOperation(id: string) {
    return request<{ ok: boolean }>('/api/fs/operations/cancel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
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
    return withPath('/api/fs/download', path)
  },

  previewUrl(path: string) {
    return `${withPath('/api/fs/download', path)}&inline=true`
  },

  thumbnailUrl(path: string, width = 320, height = 320) {
    const query = new URLSearchParams({ path, width: String(width), height: String(height) })
    return `/api/fs/thumbnail?${query}`
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
