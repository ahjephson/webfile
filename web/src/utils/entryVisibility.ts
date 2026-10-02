import type { FileEntry } from '@/types/files'

export interface EntryVisibilityOptions {
  showHidden: boolean
  hideInaccessible: boolean
}

export function isEntryVisible(entry: FileEntry, options: EntryVisibilityOptions) {
  if (!options.showHidden && entry.name.startsWith('.')) return false
  if (options.hideInaccessible
    && (entry.is_dir ? entry.can_navigate === false : entry.can_read === false)) return false
  return true
}
