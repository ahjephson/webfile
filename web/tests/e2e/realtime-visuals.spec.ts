import { expect, test } from '@playwright/test'

test('background folder refreshes preserve detected file icons until the file changes', async ({ page }) => {
  let modified = '2000-01-01T00:00:00Z'
  let detections = 0
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: {
    path: '/', entries: [{ name: 'LICENSE', path: '/LICENSE', is_dir: false, size: 20, modified, mode: '-rw-r--r--' }],
  } }))
  await page.route('**/api/fs/content-type**', route => {
    detections++
    return route.fulfill({ json: { path: '/LICENSE', content_type: 'text/plain' } })
  })
  let invalidate = () => {}
  await page.routeWebSocket('**/api/fs/ws', socket => {
    invalidate = () => socket.send(JSON.stringify({ type: 'directory-invalidated', path: '/' }))
  })
  await page.goto('/')
  const icon = page.locator('[data-file-entry] .pi-file-edit')
  await expect(icon).toBeVisible()
  await icon.evaluate(element => element.setAttribute('data-test-instance', 'original'))
  for (let index = 0; index < 3; index++) {
    const refreshed = page.waitForResponse(response => response.url().includes('/api/fs/dir?'))
    invalidate()
    await refreshed
    await expect(icon).toHaveAttribute('data-test-instance', 'original')
  }
  expect(detections).toBe(1)

  modified = '2000-01-02T00:00:00Z'
  invalidate()
  await expect.poll(() => detections).toBe(2)
  await expect(icon).toBeVisible()
})

test('copy notifications coalesce while a slow folder read is running', async ({ page }) => {
  let reads = 0
  let slowRead = false
  let release!: () => void
  const released = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/dir**', async route => {
    reads++
    if (slowRead) await released
    await route.fulfill({ json: { path: '/', entries: [] } })
  })
  let invalidate = () => {}
  await page.routeWebSocket('**/api/fs/ws', socket => {
    invalidate = () => socket.send(JSON.stringify({ type: 'directory-invalidated', path: '/' }))
  })
  await page.goto('/')
  await expect(page).toHaveURL(/#path=/)
  const initialReads = reads
  slowRead = true
  invalidate()
  await expect.poll(() => reads).toBe(initialReads + 1)
  invalidate()
  // Allow the invalidation debounce to expire while the first read is held.
  await page.waitForTimeout(300)
  expect(reads).toBe(initialReads + 1)
  release()
  await expect.poll(() => reads).toBe(initialReads + 2)
})

test('a late folder response cannot undo a newer refresh', async ({ page }) => {
  let holdNextRead = false
  let release!: () => void
  let held = false
  const released = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/dir**', async route => {
    const stale = holdNextRead
    holdNextRead = false
    if (stale) { held = true; await released }
    await route.fulfill({ json: { path: '/', entries: [{
      name: stale ? 'old.txt' : 'new.txt', path: stale ? '/old.txt' : '/new.txt',
      is_dir: false, size: 20, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--',
    }] } })
  })
  let invalidate = () => {}
  await page.routeWebSocket('**/api/fs/ws', socket => {
    invalidate = () => socket.send(JSON.stringify({ type: 'directory-invalidated', path: '/' }))
  })
  await page.goto('/')
  await expect(page).toHaveURL(/#path=/)
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'new.txt' })).toBeVisible()
  holdNextRead = true
  invalidate()
  await expect.poll(() => held).toBe(true)
  await page.getByRole('button', { name: 'Refresh', exact: true }).click()
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'new.txt' })).toBeVisible()
  const lateResponse = page.waitForResponse(response => response.url().includes('/api/fs/dir?'))
  release()
  await lateResponse
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'new.txt' })).toBeVisible()
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'old.txt' })).toHaveCount(0)
})

test('an invalidation during initial loading does not redirect to the root', async ({ page }) => {
  let folderReads = 0
  let release!: () => void
  const released = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path')
    if (path === '/docs' && ++folderReads === 1) await released
    await route.fulfill({ json: { path, entries: [] } })
  })
  let invalidate = () => {}
  await page.routeWebSocket('**/api/fs/ws', socket => {
    invalidate = () => socket.send(JSON.stringify({ type: 'directory-invalidated', path: '/docs' }))
  })
  await page.goto('/#path=%2Fdocs')
  await expect.poll(() => folderReads).toBe(1)
  invalidate()
  await expect.poll(() => folderReads).toBe(2)
  const initialResponse = page.waitForResponse(response => response.url().includes('/api/fs/dir?'))
  release()
  await initialResponse
  await expect(page.getByRole('button', { name: 'docs', exact: true })).toBeVisible()
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
})
