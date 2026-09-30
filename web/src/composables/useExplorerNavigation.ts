export type ExplorerSessionState = {
  directory: string
  selectedPaths: string[]
  focusedPath: string
  detailsVisible: boolean
  detailsPath: string
}

export function useExplorerNavigation(username: string) {
  const currentPathStorageKey = `webfile-current-path:${username}`
  const explorerStateStorageKey = `webfile-explorer-state:${username}`

  function readSessionPath(): string {
    try {
      const stored = window.sessionStorage.getItem(currentPathStorageKey)
      return stored?.startsWith('/') ? stored : '/'
    } catch {
      return '/'
    }
  }

  function readSessionState(): ExplorerSessionState {
    const fallback: ExplorerSessionState = { directory: '', selectedPaths: [], focusedPath: '', detailsVisible: false, detailsPath: '' }
    try {
      const stored = window.sessionStorage.getItem(explorerStateStorageKey)
      if (!stored) return fallback
      const parsed = JSON.parse(stored) as Partial<ExplorerSessionState>
      return {
        directory: typeof parsed.directory === 'string' ? parsed.directory : '',
        selectedPaths: Array.isArray(parsed.selectedPaths) ? parsed.selectedPaths.filter(path => typeof path === 'string') : [],
        focusedPath: typeof parsed.focusedPath === 'string' ? parsed.focusedPath : '',
        detailsVisible: parsed.detailsVisible === true,
        detailsPath: typeof parsed.detailsPath === 'string' ? parsed.detailsPath : '',
      }
    } catch {
      return fallback
    }
  }

  function readHashPath(): string | null {
    const value = new URLSearchParams(window.location.hash.replace(/^#/, '')).get('path')
    return value?.startsWith('/') ? value : null
  }

  function readHashFile(): string | null {
    const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
    const value = params.get('file')
    if (!value) return null
    if (value.startsWith('/')) return value
    if (value.includes('/')) return null
    const directory = params.get('path')
    if (!directory?.startsWith('/')) return null
    return `${directory.replace(/\/$/, '')}/${value}`
  }

  function writeHash(path: string, replace = false, file: string | null = null) {
    const fileName = file?.split('/').at(-1) ?? ''
    const filePart = fileName ? `&file=${encodeURIComponent(fileName)}` : ''
    const url = `${window.location.pathname}${window.location.search}#path=${encodeURIComponent(path)}${filePart}`
    window.history[replace ? 'replaceState' : 'pushState']({ webfilePath: path, webfileFile: fileName || null }, '', url)
  }

  function storePath(path: string) {
    try {
      window.sessionStorage.setItem(currentPathStorageKey, path)
    } catch {
      return
    }
  }

  function clearPath() {
    try {
      window.sessionStorage.removeItem(currentPathStorageKey)
    } catch {
      return
    }
  }

  function storeState(state: ExplorerSessionState) {
    try {
      window.sessionStorage.setItem(explorerStateStorageKey, JSON.stringify(state))
    } catch {
      return
    }
  }

  return { readSessionPath, readSessionState, readHashPath, readHashFile, writeHash, storePath, clearPath, storeState }
}
