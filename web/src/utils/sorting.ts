import type { FileEntry } from '@/types/files'

export type SortField = 'name' | 'modified' | 'size' | 'type'
export type SortDirection = 'asc' | 'desc'

export function sortEntries(entries: readonly FileEntry[], field: SortField, direction: SortDirection): FileEntry[] {
  const sign = direction === 'desc' ? -1 : 1
  const type = (entry: FileEntry) => entry.name.includes('.') ? entry.name.split('.').pop()!.toLocaleLowerCase() : ''
  return entries.map((entry, index) => ({ entry, index })).sort((a, b) => {
    if (a.entry.is_dir !== b.entry.is_dir) return a.entry.is_dir ? -1 : 1
    if (field === 'size') {
      const unknownA = a.entry.is_dir && a.entry.size_status !== 'ready'
      const unknownB = b.entry.is_dir && b.entry.size_status !== 'ready'
      if (unknownA !== unknownB) return unknownA ? 1 : -1
      if (!unknownA && a.entry.size !== b.entry.size) return (a.entry.size - b.entry.size) * sign
    }
    if (field === 'modified') {
      const difference = Date.parse(a.entry.modified) - Date.parse(b.entry.modified)
      if (Number.isFinite(difference) && difference !== 0) return difference * sign
    }
    if (field === 'type') {
      const difference = type(a.entry).localeCompare(type(b.entry))
      if (difference !== 0) return difference * sign
    }
    return (a.index - b.index) * (field === 'name' ? sign : 1)
  }).map(value => value.entry)
}
