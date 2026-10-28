import { expect, test, type WebSocketRoute } from '@playwright/test'

const queued = {
  id: 'video-job', path: '/Files/movie.mp4', name: 'movie.mp4', state: 'queued',
  cancellable: true, queue_position: 2, updated_at: '2000-01-01T00:00:00.000000001Z',
}

test('cancelling a stale queued thumbnail refreshes a job that has already finished', async ({ page }) => {
  let finished = false
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/Files', entries: [], single_root_path: '/Files' } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: finished ? [] : [queued] } }))
  await page.route('**/api/fs/thumbnails/jobs/video-job', route => {
    finished = true
    return route.fulfill({ status: 404, json: { error: 'video thumbnail job is not active' } })
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await page.locator('[data-thumbnail-job="video-job"]').getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.locator('[data-thumbnail-job="video-job"]')).toHaveCount(0)
  await expect(page.getByText('Could not cancel thumbnail', { exact: true })).toHaveCount(0)
})

test('thumbnail tasks reconcile a lost terminal event while the drawer stays open', async ({ page }) => {
  let finished = false
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/Files', entries: [], single_root_path: '/Files' } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: finished ? [] : [queued] } }))
  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect(page.locator('[data-thumbnail-job="video-job"]')).toBeVisible()
  finished = true
  await expect(page.locator('[data-thumbnail-job="video-job"]')).toHaveCount(0, { timeout: 10000 })
})

test('a delayed thumbnail list cannot restore queued state over a newer terminal event', async ({ page }) => {
  let socket: WebSocketRoute | undefined
  await page.routeWebSocket('**/api/fs/ws', ws => { socket = ws })
  let hold = false
  let captured = false
  let released = false
  let release = () => {}
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/Files', entries: [], single_root_path: '/Files' } }))
  await page.route('**/api/fs/thumbnails/jobs', async route => {
    if (hold) { captured = true; await pending }
    await route.fulfill({ json: { jobs: [queued] } })
    if (hold) released = true
  })
  await page.goto('/')
  await expect.poll(() => !!socket).toBe(true)
  await expect(page.getByRole('button', { name: 'Open tasks', exact: true })).toContainText('1')
  hold = true
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect.poll(() => captured).toBe(true)
  socket!.send(JSON.stringify({ type: 'thumbnail-job-updated', thumbnail_job: { ...queued, state: 'ready', cancellable: false, updated_at: '2000-01-01T00:00:00.000000002Z' } }))
  await expect(page.locator('[data-thumbnail-job="video-job"]')).toHaveCount(0)
  release()
  await expect.poll(() => released).toBe(true)
  await expect(page.locator('[data-thumbnail-job="video-job"]')).toHaveCount(0)
})
