import { previewKindForContentType, previewKindForPath, type PreviewKind } from '@/utils/preview'

export type FileVisual = {
  icon: string
  color: string
  kind: PreviewKind
  known: boolean
}

const archives = new Set(['7z', 'bz2', 'gz', 'rar', 'tar', 'tgz', 'xz', 'zip'])
const audio = new Set(['aac', 'flac', 'm4a', 'mp3', 'oga', 'ogg', 'opus', 'wav'])
const databases = new Set(['db', 'mdb', 'sqlite', 'sqlite3'])
const documents = new Set(['doc', 'docx', 'odt', 'rtf'])
const pdfs = new Set(['pdf'])
const spreadsheets = new Set(['csv', 'ods', 'xls', 'xlsx'])
const code = new Set([
  'bat', 'c', 'conf', 'cpp', 'cs', 'css', 'env', 'go', 'h', 'hpp', 'html', 'ini', 'java', 'js', 'json',
  'jsx', 'php', 'properties', 'ps1', 'py', 'rb', 'rs', 'scss', 'sh', 'sql', 'toml', 'ts', 'tsx', 'vue',
  'xml', 'yaml', 'yml',
])
const text = new Set(['log', 'md', 'txt'])

function extension(path: string): string {
  const name = path.split('/').pop()?.toLowerCase() ?? ''
  return name.includes('.') ? name.split('.').pop() ?? '' : ''
}

export function fileVisualForPath(path: string): FileVisual {
  const kind = previewKindForPath(path)
  const ext = extension(path)
  if (kind === 'image') return { icon: 'pi pi-image', color: 'text-sky-400', kind, known: true }
  if (kind === 'video') return { icon: 'pi pi-video', color: 'text-violet-400', kind, known: true }
  if (audio.has(ext)) return { icon: 'pi pi-volume-up', color: 'text-fuchsia-400', kind: 'unsupported', known: true }
  if (archives.has(ext)) return { icon: 'pi pi-box', color: 'text-amber-400', kind: 'unsupported', known: true }
  if (pdfs.has(ext)) return { icon: 'pi pi-file-pdf', color: 'text-red-400', kind: 'unsupported', known: true }
  if (documents.has(ext)) return { icon: 'pi pi-file-word', color: 'text-blue-400', kind: 'unsupported', known: true }
  if (spreadsheets.has(ext)) return { icon: 'pi pi-file-excel', color: 'text-emerald-400', kind: 'text', known: true }
  if (databases.has(ext)) return { icon: 'pi pi-database', color: 'text-cyan-400', kind: 'unsupported', known: true }
  if (code.has(ext)) return { icon: 'pi pi-code', color: 'text-cyan-400', kind: 'text', known: true }
  if (text.has(ext)) return { icon: 'pi pi-file-edit', color: 'text-zinc-300', kind: 'text', known: true }
  return { icon: 'pi pi-file', color: 'text-zinc-400', kind, known: false }
}

export function fileVisualForContentType(contentType: string, path: string): FileVisual {
  const normalized = contentType.split(';', 1)[0].trim().toLowerCase()
  const kind = previewKindForContentType(contentType, path)
  if (kind === 'image') return { icon: 'pi pi-image', color: 'text-sky-400', kind, known: true }
  if (kind === 'video') return { icon: 'pi pi-video', color: 'text-violet-400', kind, known: true }
  if (normalized.startsWith('audio/')) return { icon: 'pi pi-volume-up', color: 'text-fuchsia-400', kind, known: true }
  if (normalized === 'application/pdf') return { icon: 'pi pi-file-pdf', color: 'text-red-400', kind, known: true }
  if (normalized.includes('zip') || normalized.includes('compressed') || normalized.includes('archive')) {
    return { icon: 'pi pi-box', color: 'text-amber-400', kind, known: true }
  }
  if (normalized.includes('spreadsheet') || normalized.includes('excel') || normalized === 'text/csv') {
    return { icon: 'pi pi-file-excel', color: 'text-emerald-400', kind, known: true }
  }
  if (normalized.includes('wordprocessingml') || normalized === 'application/msword' || normalized === 'application/rtf') {
    return { icon: 'pi pi-file-word', color: 'text-blue-400', kind, known: true }
  }
  if (normalized.includes('sqlite') || normalized.includes('database')) {
    return { icon: 'pi pi-database', color: 'text-cyan-400', kind, known: true }
  }
  if (kind === 'text') return { icon: 'pi pi-file-edit', color: 'text-zinc-300', kind, known: true }
  return { icon: 'pi pi-file', color: 'text-zinc-400', kind, known: true }
}
