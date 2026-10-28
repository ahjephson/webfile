// Keep supported suffixes and compound-extension ordering aligned with the API.
export const archiveExtensions = ['.tar.bz2', '.tar.gz', '.tar.xz', '.zip', '.7z', '.rar', '.tar', '.tgz']

export function archiveFolderName(name: string): string {
  const extension = archiveExtensions.find(extension => name.toLowerCase().endsWith(extension))
  return extension ? name.slice(0, -extension.length) : name
}
