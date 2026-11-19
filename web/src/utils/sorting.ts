import type { FileEntry } from '@/types/files'

export type SortField = 'name' | 'modified' | 'size' | 'type'
export type SortDirection = 'asc' | 'desc'

export function fileExtension(entry: FileEntry): string {
  return entry.name.includes('.') ? entry.name.split('.').pop()!.toLocaleLowerCase() : ''
}

export function sortEntries(entries: readonly FileEntry[], field: SortField, direction: SortDirection): FileEntry[] {
  const sign = direction === 'desc' ? -1 : 1
  // The backend supplies host-collated name order. Preserve that ordering
  // without decorating and comparison-sorting every entry again.
  if (field === 'name') {
    const folders: FileEntry[] = [], files: FileEntry[] = []
    for (const entry of entries) (entry.is_dir ? folders : files).push(entry)
    if (direction === 'desc') { folders.reverse(); files.reverse() }
    return folders.concat(files)
  }
  return entries.map((entry, index) => ({ entry, index,
    modified: field === 'modified' ? Date.parse(entry.modified) : 0,
    type: field === 'type' ? fileExtension(entry) : '',
  })).sort((a, b) => {
    if (a.entry.is_dir !== b.entry.is_dir) return a.entry.is_dir ? -1 : 1
    if (field === 'size') {
      const unknownA = a.entry.is_dir && a.entry.size_status !== 'ready'
      const unknownB = b.entry.is_dir && b.entry.size_status !== 'ready'
      if (unknownA !== unknownB) return unknownA ? 1 : -1
      if (!unknownA && a.entry.size !== b.entry.size) return (a.entry.size - b.entry.size) * sign
    }
    if (field === 'modified') {
      const difference = a.modified - b.modified
      if (Number.isFinite(difference) && difference !== 0) return difference * sign
    }
    if (field === 'type') {
      const difference = a.type.localeCompare(b.type)
      if (difference !== 0) return difference * sign
    }
    return a.index - b.index
  }).map(value => value.entry)
}
