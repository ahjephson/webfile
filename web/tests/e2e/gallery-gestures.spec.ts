import { expect, test, type Locator, type Page } from '@playwright/test'

const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="1200"><rect width="1600" height="1200" fill="red"/></svg>'
const imageName = (index: number) => `image-${String(index).padStart(2, '0')}.svg`
const imagePath = (index: number) => `/Files/${imageName(index)}`
const unexpectedRequests = new WeakMap<Page, string[]>()

test.afterEach(async ({ page }) => {
  expect(unexpectedRequests.get(page) ?? [], 'gallery API fixture must not fall through to the real server').toEqual([])
})

async function openGallery(page: Page, pauseClock = false) {
  // Keep Vue's bubbled pointer-event timestamps on one monotonic timeline,
  // even when the host's wall clock is corrected during a browser run.
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') })
  const requestedImages = new Set<string>()
  const unexpected: string[] = []
  unexpectedRequests.set(page, unexpected)
  const entries = Array.from({ length: 12 }, (_, index) => ({
    name: imageName(index + 1), path: imagePath(index + 1), is_dir: false,
    size: 100 + index, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--',
  }))
  await page.routeWebSocket('**/api/fs/ws', () => {})
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    switch (url.pathname) {
      case '/api/session': return route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'gallery-test' } } })
      case '/api/preferences': return route.fulfill({ json: { view_mode: 'list', show_hidden: true, theme: 'dark' } })
      case '/api/fs/dir': return route.fulfill({ json: { path: '/Files', entries, single_root_path: '/Files' } })
      case '/api/fs/operations': return route.fulfill({ json: { operations: [] } })
      case '/api/fs/thumbnails/jobs': return route.fulfill({ json: { jobs: [] } })
      case '/api/fs/content-type': return route.fulfill({ json: { path: url.searchParams.get('path'), content_type: 'image/png' } })
      case '/api/fs/download': requestedImages.add(url.searchParams.get('path')!); return route.fulfill({ contentType: 'image/svg+xml', body: svg })
      case '/api/fs/thumbnail': return route.fulfill({ contentType: 'image/svg+xml', body: svg })
      default: unexpected.push(`${route.request().method()} ${url.pathname}`); return route.fulfill({ status: 501 })
    }
  })
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').filter({ hasText: imageName(6) }).dblclick()
  const preview = page.getByRole('region', { name: 'File preview', exact: true })
  const viewport = preview.locator('[data-image-viewport]')
  await expect(preview.getByRole('img', { name: imageName(6), exact: true })).toBeVisible()
  await expect.poll(() => viewport.evaluate(element => {
    const image = element.querySelector('img')!
    return image.complete && image.naturalWidth === 1600 && image.getBoundingClientRect().width <= element.clientWidth
  })).toBe(true)
  await expect(preview.getByLabel('Zoom level')).not.toHaveText('100%')
  // The current original can load before content-type discovery has made its
  // neighbours navigable. A swipe during that phase correctly does nothing.
  await expect(preview.getByRole('button', { name: 'Previous image', exact: true })).toBeEnabled()
  await expect(preview.getByRole('button', { name: 'Next image', exact: true })).toBeEnabled()
  if (pauseClock) await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  return { preview, viewport, requestedImages, unexpected }
}

async function point(target: Locator, horizontal = 0.5, vertical = 0.5) {
  await expect(target).toBeVisible()
  const box = await target.boundingBox()
  if (!box) throw new Error('Gesture target has no layout box')
  return { x: box.x + box.width * horizontal, y: box.y + box.height * vertical }
}

async function enterFullscreen(preview: Locator, viewport: Locator) {
  const nativeFullscreen = await preview.evaluate(element => document.fullscreenEnabled && typeof element.requestFullscreen === 'function')
  await preview.getByRole('button', { name: 'Enter fullscreen', exact: true }).click()
  await expect(preview).toHaveAttribute('data-immersive', 'true')
  // Wait for native fullscreen where supported. iPad WebKit uses the app's
  // immersive fallback, which must retain the same gallery controls.
  if (nativeFullscreen) await expect.poll(() => preview.evaluate(element => document.fullscreenElement === element)).toBe(true)
  await expect.poll(() => viewport.evaluate(element => {
    const image = element.querySelector('img')!
    return image.getBoundingClientRect().width <= element.clientWidth && element.scrollLeft === 0
  })).toBe(true)
}

test('gallery preloads nearby originals and advances the preload window on navigation', async ({ page }) => {
  const { preview, requestedImages, unexpected } = await openGallery(page)
  await expect(preview.getByRole('button', { name: 'Download', exact: true })).toHaveCount(0)
  await expect.poll(() => [...requestedImages].sort()).toEqual(Array.from({ length: 11 }, (_, index) => imagePath(index + 1)))
  await preview.getByRole('button', { name: 'Next image', exact: true }).click()
  await expect(preview.getByRole('img', { name: imageName(7), exact: true })).toBeVisible()
  await expect.poll(() => [...requestedImages].sort()).toEqual(Array.from({ length: 12 }, (_, index) => imagePath(index + 1)))
  expect(unexpected).toEqual([])
})

test('gallery touch swipe commits the next image after its animation', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Native touch movement requires Chromium CDP')
  const { preview, viewport } = await openGallery(page)
  const center = await point(viewport)
  const session = await page.context().newCDPSession(page)
  try {
    await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: center.x + 125, y: center.y }] })
    await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: center.x - 125, y: center.y }] })
    await expect.poll(() => preview.locator('[data-gallery-track]').evaluate(element =>
      new DOMMatrixReadOnly(getComputedStyle(element).transform).m41 + element.clientWidth)).toBeCloseTo(-250, 0)
    await expect(preview.getByRole('img', { name: imageName(6), exact: true })).toBeVisible()
    await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    // Let the actual transition complete rather than injecting transitionend.
    await expect(preview.getByRole('img', { name: imageName(7), exact: true })).toBeVisible()
    await expect(preview.locator('[data-gallery-track]')).toHaveAttribute('data-gallery-animating', 'false')
  } finally { await session.detach() }
})

test('gallery double tap zooms at the tap position and returns to fit', async ({ page }) => {
  const { preview, viewport } = await openGallery(page, true)
  const zoom = preview.getByLabel('Zoom level')
  const fitted = await zoom.innerText()
  const focal = await point(viewport, 0.7)
  const doubleTap = async () => {
    await page.touchscreen.tap(focal.x, focal.y)
    await page.clock.runFor(80)
    await page.touchscreen.tap(focal.x, focal.y)
  }
  await doubleTap()
  await expect(zoom).toHaveText('100%')
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)
  await page.clock.runFor(80)
  await doubleTap()
  await expect(zoom).toHaveText(fitted)
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBe(0)
})

test('gallery pinch increases zoom without navigating', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Native multi-touch input requires Chromium CDP')
  const { preview, viewport } = await openGallery(page)
  const before = Number.parseInt(await preview.getByLabel('Zoom level').innerText(), 10)
  const center = await point(viewport)
  const session = await page.context().newCDPSession(page)
  try {
    await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: center.x - 30, y: center.y, id: 1 }, { x: center.x + 30, y: center.y, id: 2 }] })
    await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: center.x - 90, y: center.y, id: 1 }, { x: center.x + 90, y: center.y, id: 2 }] })
    await expect.poll(async () => Number.parseInt(await preview.getByLabel('Zoom level').innerText(), 10)).toBeGreaterThan(before)
    await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await expect.poll(async () => Number.parseInt(await preview.getByLabel('Zoom level').innerText(), 10)).toBeGreaterThan(before)
    await expect(preview.getByRole('img', { name: imageName(6), exact: true })).toBeVisible()
  } finally { await session.detach() }
})

test('gallery fullscreen controls can be hidden by touch and restored explicitly', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Native touch movement requires Chromium CDP')
  const { preview, viewport } = await openGallery(page)
  await enterFullscreen(preview, viewport)
  await expect(preview.getByRole('button', { name: 'Close preview', exact: true })).toBeHidden()
  const controls = preview.getByRole('navigation', { name: 'Fullscreen image controls' })
  const start = await point(controls, 0.5, 0.9)
  const session = await page.context().newCDPSession(page)
  try {
    await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [start] })
    await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: start.x, y: start.y - 44 }] })
    await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await expect(controls).toBeHidden()
    await preview.getByRole('button', { name: 'Show fullscreen image controls', exact: true }).click()
    await expect(controls).toBeVisible()
    await preview.getByRole('button', { name: 'Exit fullscreen', exact: true }).click()
    await expect(preview).toHaveAttribute('data-immersive', 'false')
    await expect(preview.getByRole('button', { name: 'Back to folder', exact: true })).toBeVisible()
  } finally { await session.detach() }
})

test('gallery fullscreen mouse swipe navigates and actual-size drag pans the image', async ({ page }) => {
  const { preview, viewport } = await openGallery(page)
  await enterFullscreen(preview, viewport)
  const start = await point(viewport, 0.75)
  const end = await point(viewport, 0.25)
  await page.mouse.move(start.x, start.y)
  await page.mouse.down()
  await page.mouse.move(end.x, end.y, { steps: 6 })
  await page.mouse.up()
  await expect(preview.getByRole('img', { name: imageName(7), exact: true })).toBeVisible()
  await expect(preview.locator('[data-gallery-track]')).toHaveAttribute('data-gallery-animating', 'false')
  const controls = preview.getByRole('navigation', { name: 'Fullscreen image controls' })
  await controls.getByRole('button', { name: 'Actual size', exact: true }).click()
  await expect(controls.getByLabel('Zoom level')).toHaveText('100%')
  await expect.poll(() => viewport.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
  const center = await point(viewport)
  const before = await viewport.evaluate(element => element.scrollLeft)
  await page.mouse.move(center.x, center.y)
  await page.mouse.down()
  await page.mouse.move(center.x - 140, center.y, { steps: 4 })
  await page.mouse.up()
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBeGreaterThan(before)
  await controls.getByRole('button', { name: 'Fit to window', exact: true }).click()
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBe(0)
})

test('gallery fullscreen single taps toggle thumbnails without becoming a double tap', async ({ page }) => {
  const { preview, viewport } = await openGallery(page, true)
  await enterFullscreen(preview, viewport)
  await page.clock.runFor(32)
  const strip = preview.locator('[data-thumbnail-strip]')
  const zoom = await preview.getByRole('navigation', { name: 'Fullscreen image controls' }).getByLabel('Zoom level').innerText()
  const center = await point(viewport)
  await page.touchscreen.tap(center.x, center.y)
  await page.clock.runFor(399)
  await expect(strip).toBeVisible()
  await page.clock.runFor(1)
  await expect(strip).toBeHidden()
  // Showing/hiding thumbnails changes the viewport. Complete its scheduled
  // layout frames and locate the second tap in the new geometry.
  await page.clock.runFor(32)
  const next = await point(viewport)
  await page.touchscreen.tap(next.x, next.y)
  await page.clock.runFor(399)
  await expect(strip).toBeHidden()
  await page.clock.runFor(1)
  await expect(strip).toBeVisible()
  await page.clock.runFor(32)
  await expect(preview.getByRole('navigation', { name: 'Fullscreen image controls' }).getByLabel('Zoom level')).toHaveText(zoom)
})
