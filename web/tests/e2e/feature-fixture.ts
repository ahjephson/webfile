import { expect, type Page } from '@playwright/test'
import { randomUUID } from 'node:crypto'

export async function featureFixture(page: Page, prefix: string) {
  await page.goto('/')
  // Initial setup writes the normalized folder URL after loading preferences.
  // Finish that setup before navigating this page to a fixture folder.
  await expect(page).toHaveURL(/#path=/)
  const { csrf_token } = await (await page.request.get('/api/session')).json()
  const headers = { 'X-WebFile-CSRF': csrf_token }
  const listing = await (await page.request.get('/api/fs/dir?path=%2F')).json()
  const root = listing.single_root_path || listing.entries[0].path
  const parent = process.env.WEBFILE_E2E_FIXTURE_PARENT || (root === '/WSL Root' ? `${root}/tmp` : root)
  const path = `${parent}/${prefix}-${randomUUID().slice(0, 8)}`
  const mkdir = async (target: string) => {
    const response = await page.request.post('/api/fs/mkdir', { headers, data: { path: target } })
    expect(response.ok()).toBe(true)
  }
  const write = async (target: string, content: string) => {
    const url = `/api/fs/file?path=${encodeURIComponent(target)}`
    const existing = await page.request.get(url)
    const condition = existing.ok() ? { 'If-Match': (await existing.json()).etag } : { 'If-None-Match': '*' }
    const response = await page.request.put(url, { headers: { ...headers, ...condition, 'Content-Type': 'text/plain' }, data: content })
    expect(response.ok(), await response.text()).toBe(true)
  }
  const read = async (target: string) => {
    const response = await page.request.get(`/api/fs/file?path=${encodeURIComponent(target)}`)
    expect(response.ok()).toBe(true)
    return (await response.json()).content as string
  }
  const exists = async (target: string) => (await page.request.get(`/api/fs/file?path=${encodeURIComponent(target)}`)).ok()
  const start = async (kind: string, sources: string[], destination: string) => {
    const response = await page.request.post('/api/fs/operations', { headers, data: { kind, sources, destination } })
    expect(response.status()).toBe(202)
    return response.json()
  }
  await mkdir(path)
  return { path, mkdir, write, read, exists, start }
}
