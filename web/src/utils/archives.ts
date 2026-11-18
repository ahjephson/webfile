// Keep supported suffixes and compound-extension ordering aligned with the API.
export const archiveExtensions = ['.tar.bz2', '.tar.gz', '.tar.xz', '.zip', '.7z', '.rar', '.tar', '.tgz']

const multipartSuffix = /\.(7z|zip)\.(\d{3}|[1-9]\d{3,})$/i

export function isArchiveName(name: string): boolean {
  return multipartSuffix.test(name) || archiveExtensions.some(extension => name.toLowerCase().endsWith(extension))
}

export function firstArchiveVolume(path: string): string {
  return multipartSuffix.test(path) ? path.replace(/\d+$/, '001') : path
}

export function archiveFolderName(name: string): string {
  if (multipartSuffix.test(name)) name = name.slice(0, name.lastIndexOf('.'))
  const extension = archiveExtensions.find(extension => name.toLowerCase().endsWith(extension))
  return extension ? name.slice(0, -extension.length) : name
}
