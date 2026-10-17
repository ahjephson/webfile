import { expect, test, type Page } from '@playwright/test'

async function largeDirectory(page: Page) {
  const entries = Array.from({ length: 20_000 }, (_, index) => ({ name: `file-${String(index).padStart(6, '0')}.txt`,
    path: `/Files/file-${String(index).padStart(6, '0')}.txt`, is_dir: false, size: index,
    modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }))
  let reads = 0
  let invalidate = () => {}
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'large-directory' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  await page.routeWebSocket('**/api/fs/ws', socket => { invalidate = () => socket.send(JSON.stringify({ type: 'directory-invalidated', path: '/Files' })) })
  await page.route('**/api/fs/dir**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    if (path === '/Files') reads++
    return route.fulfill({ json: { path, entries: path === '/Files' ? entries : [] } })
  })
  await page.goto('/#path=%2FFiles')
  await expect(page.getByText('file-000000.txt', { exact: true })).toBeVisible()
  return { reads: () => reads, invalidate: () => invalidate() }
}

test('selecting a file in a large folder reads only the visible rows', async ({ page }) => {
  await page.addInitScript(() => {
    const state = { pathReads: 0 }
    Object.assign(window, { __largeDirectory: state })
    const originalFetch = window.fetch.bind(window)
    window.fetch = async (...args) => {
      const response = await originalFetch(...args)
      if (!response.url.includes('/api/fs/dir')) return response
      const json = response.json.bind(response)
      response.json = async () => {
        const body = await json()
        for (const entry of body.entries ?? []) {
          const path = entry.path
          Object.defineProperty(entry, 'path', { enumerable: true, get: () => { state.pathReads++; return path } })
        }
        return body
      }
      return response
    }
  })
  await largeDirectory(page)
  await page.waitForLoadState('networkidle')
  await page.evaluate(() => { (window as unknown as { __largeDirectory: { pathReads: number } }).__largeDirectory.pathReads = 0 })
  const start = Date.now()
  await page.getByText('file-000000.txt', { exact: true }).click()
  await expect(page.getByText('1 selected', { exact: true })).toBeVisible()
  const pathReads = await page.evaluate(() => (window as unknown as { __largeDirectory: { pathReads: number } }).__largeDirectory.pathReads)
  console.log(JSON.stringify({ entries: 20_000, selectionMilliseconds: Date.now() - start, pathReads }))
  expect(pathReads).toBeLessThan(2000)
  expect(await page.locator('[data-file-entry]').count()).toBeLessThan(100)
})

test('large-folder invalidations coalesce without delaying explicit Refresh', async ({ page }) => {
  await page.clock.install()
  const folder = await largeDirectory(page)
  const initial = folder.reads()
  for (let index = 0; index < 10; index++) folder.invalidate()
  await page.clock.fastForward(120)
  expect(folder.reads()).toBe(initial)
  await page.clock.fastForward(5000)
  await expect.poll(folder.reads).toBe(initial + 1)
  await expect(page.getByText('file-000000.txt', { exact: true })).toBeVisible()
  folder.invalidate()
  await page.clock.fastForward(120)
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await expect.poll(folder.reads).toBe(initial + 2)
})
