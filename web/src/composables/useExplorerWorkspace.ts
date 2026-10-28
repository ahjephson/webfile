import { inject, onBeforeUnmount, ref, shallowReactive, shallowRef, type InjectionKey, type Ref } from 'vue'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import { createThumbnailJobs } from '@/composables/useThumbnailJobs'
import type { UserPreferences } from '@/preferences'

export type PaneId = 'left' | 'right'
export type TransferMode = 'copy' | 'move'
export const fileDragType = 'application/x-webfile-items'
export type PaneController = {
  path: Ref<string>
  canHide: () => boolean
  focus: () => void
  snapshot: () => { path: string; entries: FileEntry[]; singleRootPath: string } | null
}

export function createExplorerWorkspace() {
  const preferences = shallowRef<UserPreferences | null>(null)
  const thumbnails = createThumbnailJobs()
  let preferenceWrites: Promise<unknown> = Promise.resolve()
  let uploadTail: Promise<void> = Promise.resolve()
  let disposed = false
  onBeforeUnmount(() => { disposed = true; api.abandonUploads() })
  return {
    split: ref(false),
    activePane: ref<PaneId>('left'),
    panes: shallowReactive(new Map<PaneId, PaneController>()),
    clipboard: ref<{ mode: TransferMode; paths: string[] } | null>(null),
    drag: shallowRef<{ token: string; entries: FileEntry[] } | null>(null),
    operationsVisible: ref(false),
    settingsVisible: ref(false),
    taskAttention: ref(false),
    thumbnailJobs: thumbnails.jobs,
    thumbnails,
    preferences,
    enqueueUpload(run: () => Promise<void>) {
      const transfer = uploadTail.catch(() => undefined).then(() => {
        if (disposed) return
        return run()
      })
      uploadTail = transfer
      return transfer
    },
    savePreferences(snapshot: UserPreferences) {
      preferences.value = { ...snapshot }
      const write = preferenceWrites.catch(() => undefined).then(() => {
        if (disposed) throw new Error('Workspace is closed')
        return api.savePreferences(snapshot)
      })
      preferenceWrites = write
      return write
    },
    toggleSplit: () => {},
  }
}

export const explorerWorkspaceKey: InjectionKey<ReturnType<typeof createExplorerWorkspace>> = Symbol('explorer-workspace')
export function useExplorerWorkspace() { return inject(explorerWorkspaceKey)! }

export function dropMode(event: Pick<DragEvent, 'ctrlKey' | 'shiftKey'>): TransferMode {
  return event.ctrlKey && !event.shiftKey ? 'copy' : 'move'
}

export function acceptsFileDrop(event: DragEvent) {
  return [...(event.dataTransfer?.types ?? [])].some(type => type === 'Files' || type === fileDragType)
}
