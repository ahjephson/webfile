import { expect, test, type Page } from '@playwright/test'

const entry = (name: string) => ({ name, path: `/Files/${name}`, is_dir: false, size: 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' })
const image = '<svg xmlns="http://www.w3.org/2000/svg" width="50" height="50"><rect width="50" height="50" fill="green"/></svg>'

async function setup(page: Page, entries: ReturnType<typeof entry>[]) {
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'action-performance' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  await page.routeWebSocket('**/api/fs/ws', () => {})
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/Files', entries } }))
}

test('a batch upload coalesces folder refreshes', async ({ page }) => {
  await setup(page, [])
  let reads = 0, completed = 0
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/fs/dir') reads++ })
  const operation = (name: string, state: string) => ({ id: name, kind: 'upload', sources: [`/Files/${name}`], state, phase: state,
    progress_done: state === 'completed' ? 1 : 0, progress_total: 1, progress_unit: 'bytes', bytes_done: 0, bytes_total: 1,
    started_at: new Date().toISOString(), updated_at: new Date().toISOString(), cancellable: state === 'running' })
  await page.route('**/api/fs/uploads/init', route => {
    const { name } = route.request().postDataJSON()
    return route.fulfill({ json: { upload: { id: name, offset: 0, size: 1 }, operation: operation(name, 'running') } })
  })
  await page.route('**/api/fs/uploads/chunk**', route => {
    const name = new URL(route.request().url()).searchParams.get('id')!
    completed++
    return route.fulfill({ json: { upload: { id: name, offset: 1, size: 1 }, complete: true, operation: operation(name, 'completed') } })
  })
  await page.goto('/#path=%2FFiles')
  await page.waitForLoadState('networkidle')
  const initial = reads
  await page.locator('input[type=file]').setInputFiles(Array.from({ length: 25 }, (_, index) => ({ name: `file-${index}.txt`, mimeType: 'text/plain', buffer: Buffer.from('x') })))
  // This checks refresh coalescing, not the throughput of 50 sequential routed
  // requests. Allow the batch to finish on a cold CI browser before counting.
  await expect.poll(() => completed, { timeout: 15_000 }).toBe(25)
  await page.waitForLoadState('networkidle')
  expect(reads - initial).toBeLessThanOrEqual(3)
})

test('closing the gallery cancels obsolete type detection', async ({ page }) => {
  await setup(page, [entry('photo.png'), ...Array.from({ length: 80 }, (_, index) => entry(`document-${index}.pdf`))])
  let inspections = 0
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/content-type**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path')!
    if (path.endsWith('.pdf')) { inspections++; await gate }
    await route.fulfill({ json: { path, content_type: path.endsWith('.png') ? 'image/png' : 'application/pdf' } }).catch(() => {})
  })
  await page.route('**/api/fs/download**', route => route.fulfill({ contentType: 'image/svg+xml', body: image }))
  await page.route('**/api/fs/thumbnail?**', route => route.fulfill({ contentType: 'image/svg+xml', body: image }))
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').filter({ hasText: 'photo.png' }).dblclick()
  try {
    await expect.poll(() => inspections).toBe(4)
    await page.getByRole('button', { name: 'Back to folder', exact: true }).click()
  } finally { release() }
  await page.waitForLoadState('networkidle')
  expect(inspections).toBe(4)
})

test('gallery navigation releases originals outside its preload neighbourhood', async ({ page }) => {
  await page.addInitScript(() => {
    const images: HTMLImageElement[] = []
    ;(window as unknown as { originalImages: HTMLImageElement[] }).originalImages = images
    window.Image = new Proxy(window.Image, { construct(target, argumentsList) {
      const image = Reflect.construct(target, argumentsList) as HTMLImageElement
      images.push(image)
      return image
    } })
  })
  await setup(page, Array.from({ length: 30 }, (_, index) => entry(`photo-${String(index).padStart(2, '0')}.png`)))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'image/png' } }))
  await page.route('**/api/fs/thumbnail?**', route => route.fulfill({ contentType: 'image/svg+xml', body: image }))
  let release!: () => void, started = false
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/download**', async route => {
    if (route.request().url().includes('photo-01.png')) { started = true; await gate }
    await route.fulfill({ contentType: 'image/svg+xml', body: image }).catch(() => {})
  })
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').filter({ hasText: 'photo-00.png' }).dblclick()
  try {
    await expect.poll(() => started).toBe(true)
    await page.getByRole('option', { name: 'Show photo-20.png', exact: true }).click()
    await expect(page.getByRole('img', { name: 'photo-20.png', exact: true })).toBeVisible()
    // Inspect released native image resources: WebKit need not emit a failed
    // network event for a request held by route interception.
    await expect.poll(() => page.evaluate(() => {
      const images = (window as unknown as { originalImages: HTMLImageElement[] }).originalImages
      return images.filter(image => image.src.includes('/api/fs/download')).length
    })).toBeLessThanOrEqual(11)
    await expect.poll(() => page.evaluate(() => {
      const images = (window as unknown as { originalImages: HTMLImageElement[] }).originalImages
      return images.some(image => image.src.includes('download') && image.src.includes('photo-01.png'))
    })).toBe(false)
  } finally { release() }
})

test('destination folders are virtualised and distant folders remain selectable', async ({ page }) => {
  await setup(page, Array.from({ length: 2000 }, (_, index) => ({ ...entry(`folder-${String(index).padStart(4, '0')}`), is_dir: true })))
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').first().click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Copy to…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Copy to…', exact: true })
  await expect(dialog.getByRole('button', { name: 'folder-0000', exact: true })).toBeVisible()
  expect(await dialog.getByRole('button', { name: /^folder-/ }).count()).toBeLessThan(50)
  await dialog.locator('[data-destination-viewport]').evaluate(element => { element.scrollTop = element.scrollHeight })
  await dialog.getByRole('button', { name: 'folder-1999', exact: true }).click()
  await expect(dialog.getByText('Destination:')).toContainText('/Files/folder-1999')
})

test('folder size refreshes stale results without displaying scan metadata', async ({ page }) => {
  await page.clock.install()
  await setup(page, [{ ...entry('folder'), is_dir: true }])
  let scans = 0, cancelled = 0
  await page.route('**/api/fs/details**', route => {
    if (route.request().method() === 'DELETE') { cancelled++; return route.fulfill({ status: 204 }) }
    expect(new URL(route.request().url()).searchParams.get('refresh')).toBe('false')
    scans++
    return route.fulfill({ json: { ...entry('folder'), is_dir: true, is_symlink: false, size: scans * 10,
      size_status: 'ready', size_calculated_at: new Date(Date.now()).toISOString(), mode_octal: '0755', uid: 1, gid: 1, owner: 'test', group: 'test' } })
  })
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').first().click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Details', exact: true }).click()
  const panel = page.locator('aside').filter({ has: page.getByRole('heading', { name: 'Details', exact: true }) })
  await expect(panel.getByText('10 B', { exact: true })).toBeVisible()
  await expect(panel.getByText('Size calculated', { exact: true })).toHaveCount(0)
  await page.clock.fastForward(5 * 60_000 + 1000)
  await expect.poll(() => scans).toBe(2)
  await expect(panel.getByText('20 B', { exact: true })).toBeVisible()
  await panel.getByRole('button', { name: 'Close details', exact: true }).click()
  await expect.poll(() => cancelled).toBe(1)
})

test('partial folder sizes show a warning in details and the cached listing', async ({ page }) => {
  await setup(page, [{ ...entry('folder'), is_dir: true }])
  await page.route('**/api/fs/details**', route => route.request().method() === 'DELETE'
    ? route.fulfill({ status: 204 })
    : route.fulfill({ json: { ...entry('folder'), is_dir: true, is_symlink: false, size: 10905190,
      size_status: 'ready', size_incomplete: true, size_calculated_at: new Date().toISOString(),
      mode_octal: '0755', uid: 1, gid: 1, owner: 'test', group: 'test' } }))
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').first().click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Details', exact: true }).click()
  const panel = page.locator('aside').filter({ has: page.getByRole('heading', { name: 'Details', exact: true }) })
  const warning = 'Some items could not be accessed and were excluded from the total.'
  const size = panel.locator('[tabindex="0"][title]').filter({ hasText: '10.4 MiB' })
  await expect(size).toHaveClass(/text-yellow-500/)
  await expect(size.locator('.pi-exclamation-triangle')).toBeVisible()
  await size.hover()
  await expect(page.getByRole('tooltip')).toHaveText(warning)
  await expect(size).toHaveAttribute('aria-label', `10.4 MiB. ${warning}`)
  await expect(page.locator('[data-file-entry]').first().locator('.pi-exclamation-triangle')).toBeVisible()
  await panel.getByRole('button', { name: 'Close details', exact: true }).click()
  await expect(page.locator('[data-file-entry]').first()).toContainText('10.4 MiB')
})

test('listing renews previously scanned sizes and polls only while recalculation is pending', async ({ page }) => {
  const scanned = '2026-10-16T12:00:00Z'
  await page.clock.install({ time: new Date(scanned) })
  await setup(page, [])
  let changed = false, recalculatingReads = 0
  await page.route('**/api/fs/dir**', route => {
    const pending = changed && recalculatingReads++ === 0
    return route.fulfill({ json: { path: '/Files', entries: [{ ...entry('folder'), is_dir: true,
      size: changed ? 17 : 7, size_status: pending ? 'calculating' : 'ready', size_refreshing: pending,
      size_calculated_at: changed && !pending ? '2026-10-16T12:05:30Z' : scanned }] } })
  })
  await page.goto('/#path=%2FFiles')
  await expect(page.locator('[data-file-entry]')).toContainText('7 B')
  changed = true
  await page.clock.fastForward(5 * 60_000 + 30_000)
  await expect.poll(() => recalculatingReads).toBe(1)
  await page.clock.fastForward(500)
  await expect(page.locator('[data-file-entry]')).toContainText('17 B')
  const completedReads = recalculatingReads
  await page.clock.fastForward(10_000)
  expect(recalculatingReads).toBe(completedReads)
})
