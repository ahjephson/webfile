const placeholder = '__WEBFILE_BASE_PATH__'

function configuredBasePath(): string {
  const configured = document.querySelector<HTMLMetaElement>('meta[name="webfile-base-path"]')?.content
  if (!configured || configured === placeholder) return '/'
  return `/${configured.replace(/^\/+|\/+$/g, '')}/`.replace(/^\/\/$/, '/')
}

export const basePath = configuredBasePath()

export function appPath(path = ''): string {
  return `${basePath}${path.replace(/^\/+/, '')}`
}

export function appURL(path = ''): URL {
  return new URL(appPath(path), window.location.origin)
}

export function appWebSocketURL(path: string): string {
  const target = appURL(path)
  target.protocol = target.protocol === 'https:' ? 'wss:' : 'ws:'
  return target.toString()
}
