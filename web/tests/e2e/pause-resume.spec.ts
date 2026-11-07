import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

test('all file task controls retain paused status after reload and exclude video thumbnails', async ({ page }) => {
  let revision = 0
  const kinds = ['copy', 'move', 'delete', 'extract', 'archive', 'upload']
  const tasks = kinds.map((kind, index) => ({
    id: `pause-${kind}`, kind, sources: [`/${kind}.bin`], destination: '/target',
    state: index % 2 ? 'queued' : 'running', phase: 'running', paused: false, cancellable: true,
    progress_done: 30, progress_total: 100, bytes_done: 30, bytes_total: 100,
    items_done: 0, items_total: 1, queue_position: index + 1,
    started_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: tasks } }))
  await page.route(/\/api\/fs\/operations\/pause-[^/]+\/(pause|resume)$/, route => {
    const parts = new URL(route.request().url()).pathname.split('/')
    const task = tasks.find(item => item.id === parts.at(-2))!
    task.paused = parts.at(-1) === 'pause'
    task.updated_at = new Date(Date.UTC(2026, 0, 1, 0, 0, ++revision)).toISOString()
    return route.fulfill({ json: task })
  })
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [{
    id: 'video-job', path: '/movie.mp4', name: 'movie.mp4', state: 'generating', updated_at: '2026-01-01T00:00:00Z',
  }] } }))
  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const drawer = page.getByRole('complementary')
  for (const kind of kinds) {
    const label = `${kind[0]!.toUpperCase()}${kind.slice(1)} ${kind}.bin`
    await drawer.getByRole('button', { name: `Pause ${label}`, exact: true }).click()
    await expect(drawer.getByRole('article').filter({ hasText: label })).toHaveAttribute('data-task-state', 'paused')
  }
  await expect(drawer.locator('[data-thumbnail-job]')).toHaveCount(1)
  await expect(drawer.locator('[data-thumbnail-job]').getByRole('button', { name: /Pause|Resume/ })).toHaveCount(0)
  await page.reload()
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  for (const kind of kinds) {
    const label = `${kind[0]!.toUpperCase()}${kind.slice(1)} ${kind}.bin`
    await expect(drawer.getByRole('article').filter({ hasText: label })).toHaveAttribute('data-task-state', 'paused')
    await drawer.getByRole('button', { name: `Resume ${label}`, exact: true }).click()
    await expect(drawer.getByRole('button', { name: `Pause ${label}`, exact: true })).toBeVisible()
  }
})

for (const cancel of [false, true]) {
  test(`a paused upload ${cancel ? 'can be cancelled' : 'resumes from its existing chunk offset'}`, async ({ page }) => {
    const fixture = await featureFixture(page, 'pause-upload')
    let release = () => {}
    const hold = new Promise<void>(resolve => { release = resolve })
    let held = false
    let lastChunkRequests = 0
    await page.route('**/api/fs/uploads/chunk?**', async route => {
      if (new URL(route.request().url()).searchParams.get('offset') === String(8 * 1024 * 1024)) {
        lastChunkRequests++
        if (!held) { held = true; await hold }
      }
      await route.continue().catch(() => {})
    })
    await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
    await page.locator('input[type=file]').setInputFiles({ name: 'paused.bin', mimeType: 'application/octet-stream', buffer: Buffer.alloc(9 * 1024 * 1024, 7) })
    await expect.poll(() => held).toBe(true)
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const task = page.getByRole('complementary').getByRole('article').filter({ hasText: 'Upload paused.bin' }).filter({ hasText: fixture.path })
    await task.getByRole('button', { name: 'Pause Upload paused.bin', exact: true }).click()
    await expect(task).toHaveAttribute('data-task-state', 'paused')
    release()
    // Let the browser observe the paused response and reconcile without sending another chunk.
    await page.waitForTimeout(1300)
    expect(lastChunkRequests).toBe(1)
    expect(await fixture.exists(`${fixture.path}/paused.bin`)).toBe(false)
    if (cancel) {
      await task.getByRole('button', { name: 'Cancel', exact: true }).click()
      await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel task', exact: true }).click()
      await expect(task.locator('[data-task-status]')).toHaveText('cancelled')
      expect(await fixture.exists(`${fixture.path}/paused.bin`)).toBe(false)
    } else {
      await task.getByRole('button', { name: 'Resume Upload paused.bin', exact: true }).click()
      await expect(task.locator('[data-task-status]')).toHaveText('completed', { timeout: 15000 })
      expect(lastChunkRequests).toBe(2)
      const listing = await (await page.request.get(`/api/fs/dir?path=${encodeURIComponent(fixture.path)}`)).json()
      expect(listing.entries.find((entry: { name: string }) => entry.name === 'paused.bin').size).toBe(9 * 1024 * 1024)
    }
  })
}
