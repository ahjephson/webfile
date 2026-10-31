import type { FileEntry } from '@/types/files'

export type ListingScrollPosition = {
  top: number
  left: number
  width: number
  height: number
  columns: number
  rowHeight: number
  anchorPath: string
  anchorIndex: number
  offset: number
}

export function captureListingScroll(element: HTMLElement | null, entries: FileEntry[], columns: number, rowHeight: number): ListingScrollPosition | null {
  if (!element || !element.clientWidth || !element.clientHeight) return null
  const row = Math.floor(element.scrollTop / rowHeight)
  const anchorIndex = Math.min(row * columns, Math.max(0, entries.length - 1))
  return {
    top: element.scrollTop, left: element.scrollLeft,
    width: element.clientWidth, height: element.clientHeight, columns, rowHeight,
    anchorPath: entries[anchorIndex]?.path ?? '', anchorIndex,
    offset: element.scrollTop - row * rowHeight,
  }
}

export function restoreListingScroll(element: HTMLElement | null, entries: FileEntry[], columns: number, rowHeight: number, position: ListingScrollPosition | null) {
  if (!element) return
  let top = 0
  if (position) {
    const sameLayout = position.width === element.clientWidth && position.height === element.clientHeight && position.columns === columns && position.rowHeight === rowHeight
    const sameAnchor = entries[position.anchorIndex]?.path === position.anchorPath
    if (sameLayout && sameAnchor) top = position.top
    else {
      const located = entries.findIndex(entry => entry.path === position.anchorPath)
      const index = located >= 0 ? located : Math.min(position.anchorIndex, Math.max(0, entries.length - 1))
      top = Math.floor(index / columns) * rowHeight + Math.min(position.offset, rowHeight - 1)
    }
  }
  element.scrollTop = top
  element.scrollLeft = position?.left ?? 0
}
