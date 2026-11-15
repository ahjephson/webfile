import { expect, test, type Page } from '@playwright/test'

const image = '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="blue"/></svg>'
const entry = (name: string, version = 1) => ({ name, path: `/docs/${name}`, is_dir: false, size: 100,
  modified: `2000-01-0${version}T00:00:00Z`, mode: '-rw-r--r--' })

async function mediaFolder(page: Page) {
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'media-test' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'grid', theme: 'dark' } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  await page.route('**/api/fs/thumbnails/queue', route => route.fulfill({ json: { jobs: [] } }))
  await page.route('**/api/fs/thumbnail?**', route => route.fulfill({ contentType: 'image/svg+xml', body: image }))
  await page.route('**/api/fs/download?**', route => route.fulfill({ contentType: 'image/svg+xml', body: image }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'image/jpeg' } }))
  let socket: { send: (message: string) => void } | undefined
  await page.routeWebSocket('**/api/fs/ws', connection => { socket = connection })
  return { invalidate: async () => {
    await expect.poll(() => Boolean(socket)).toBe(true)
    socket!.send(JSON.stringify({ type: 'directory-invalidated', path: '/docs' }))
  } }
}

test('image tiles keep their icon until the thumbnail loads and on generation failure', async ({ page }) => {
  await mediaFolder(page)
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/docs', entries: [entry('photo.jpg'), entry('broken.jpg')] } }))
  let releaseThumbnails!: () => void
  const ready = new Promise<void>(resolve => { releaseThumbnails = resolve })
  await page.route('**/api/fs/thumbnail?**', async route => {
    await ready
    if (new URL(route.request().url()).searchParams.get('path') === '/docs/broken.jpg') {
      await route.fulfill({ status: 500 })
    } else {
      await route.fulfill({ contentType: 'image/svg+xml', body: image })
    }
  })
  await page.goto('/#path=%2Fdocs')
  const photo = page.locator('[data-file-entry]').filter({ hasText: 'photo.jpg' })
  const broken = page.locator('[data-file-entry]').filter({ hasText: 'broken.jpg' })
  try {
    await expect(photo.locator('[data-file-icon]')).toBeVisible()
    await expect(broken.locator('[data-file-icon]')).toBeVisible()
    await expect(photo.locator('[data-file-thumbnail]')).toHaveCSS('opacity', '0')
  } finally {
    releaseThumbnails()
  }
  await expect(photo.locator('[data-file-thumbnail]')).toHaveCSS('opacity', '1')
  await expect(photo.locator('[data-file-icon]')).toHaveCount(0)
  await expect(broken.locator('[data-file-thumbnail]')).toHaveCount(0)
  await expect(broken.locator('[data-file-icon]')).toBeVisible()
})

test('grid refreshes changed image thumbnails and retains unchanged ones', async ({ page }) => {
  const realtime = await mediaFolder(page)
  let version = 1, reads = 0
  const requests: string[] = []
  await page.route('**/api/fs/dir**', route => { reads++; return route.fulfill({ json: { path: '/docs', entries: [entry('photo.jpg', version)] } }) })
  await page.route('**/api/fs/thumbnail?**', route => {
    requests.push(route.request().url())
    return route.fulfill({ contentType: 'image/svg+xml', body: image })
  })
  await page.goto('/#path=%2Fdocs')
  await expect.poll(() => requests.length).toBe(1)
  for (version = 2; version <= 3; version++) {
    const before = reads
    await realtime.invalidate()
    await expect.poll(() => reads).toBeGreaterThan(before)
    await expect.poll(() => requests.length).toBe(version)
    await expect(page.locator('[data-file-thumbnail]')).toBeVisible()
  }
  const before = reads
  version = 3
  await realtime.invalidate()
  await expect.poll(() => reads).toBeGreaterThan(before)
  await page.waitForLoadState('networkidle')
  expect(requests).toHaveLength(3)
  expect(new Set(requests).size).toBe(3)
})

test('open gallery discovers added images and removes deleted images across repeated refreshes', async ({ page }) => {
  const realtime = await mediaFolder(page)
  let entries = [entry('first.jpg')], reads = 0
  await page.route('**/api/fs/dir**', route => { reads++; return route.fulfill({ json: { path: '/docs', entries } }) })
  await page.goto('/#path=%2Fdocs&file=first.jpg')
  const next = page.getByRole('button', { name: 'Next image', exact: true })
  await expect(next).toBeDisabled()
  for (const name of ['second.jpg', 'extensionless-image']) {
    entries = [entry('first.jpg'), entry(name)]
    const before = reads
    await realtime.invalidate()
    await expect.poll(() => reads).toBeGreaterThan(before)
    await expect(next).toBeEnabled()
    await expect(page.getByRole('option', { name: `Show ${name}`, exact: true })).toBeVisible()
    entries = [entry('first.jpg')]
    await realtime.invalidate()
    await expect(next).toBeDisabled()
    await expect(page.getByRole('option', { name: `Show ${name}`, exact: true })).toHaveCount(0)
  }
})
