import { archiveExtensions } from '@/utils/archives'

export type PreviewKind = 'text' | 'image' | 'video' | 'audio' | 'archive' | 'unsupported'

const textExtensions = new Set([
  'bat', 'c', 'conf', 'cpp', 'cs', 'css', 'csv', 'env', 'go', 'h', 'hpp', 'html', 'ini', 'java', 'js', 'json',
  'jsx', 'log', 'md', 'php', 'properties', 'ps1', 'py', 'rb', 'rs', 'scss', 'sh', 'sql', 'toml', 'ts',
  'tsx', 'txt', 'vue', 'xml', 'yaml', 'yml',
])
const imageExtensions = new Set(['avif', 'bmp', 'gif', 'ico', 'jpeg', 'jpg', 'png', 'webp'])
const videoExtensions = new Set(['m4v', 'mov', 'mp4', 'ogv', 'webm'])
const audioExtensions = new Set(['aac', 'flac', 'm4a', 'mp3', 'oga', 'ogg', 'opus', 'wav'])

export function previewKindForPath(path: string): PreviewKind {
  const name = path.split('/').pop()?.toLowerCase() ?? ''
  if (archiveExtensions.some(extension => name.endsWith(extension))) return 'archive'
  const extension = name.includes('.') ? name.split('.').pop() ?? '' : ''
  if (textExtensions.has(extension)) return 'text'
  if (imageExtensions.has(extension)) return 'image'
  if (videoExtensions.has(extension)) return 'video'
  if (audioExtensions.has(extension)) return 'audio'
  return 'unsupported'
}

export function previewKindForContentType(contentType: string, path: string): PreviewKind {
  if (previewKindForPath(path) === 'archive') return 'archive'
  const normalized = contentType.split(';', 1)[0].trim().toLowerCase()
  if (normalized === 'image/svg+xml') return 'unsupported'
  if (normalized.startsWith('image/')) return 'image'
  if (normalized.startsWith('video/')) return 'video'
  if (normalized.startsWith('audio/')) return 'audio'
  if (normalized.startsWith('text/')) return 'text'
  if (['application/json', 'application/ld+json', 'application/javascript', 'application/xml', 'application/x-httpd-php', 'application/yaml'].includes(normalized)) return 'text'
  if (!normalized || normalized === 'application/octet-stream') return previewKindForPath(path)
  return 'unsupported'
}
