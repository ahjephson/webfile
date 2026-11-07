import { expect, test } from '@playwright/test'

test('task speeds and estimates respect units, pauses, stalls, unknown totals and native archive progress', async ({ page }) => {
  const mib = 1024 * 1024
  const base = {
    state: 'running', phase: 'copying', paused: false, cancellable: true,
    progress_done: 50 * mib, progress_total: 100 * mib, progress_unit: 'bytes',
    bytes_done: 50 * mib, bytes_total: 100 * mib, items_done: 0, items_total: 1,
    started_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  }
  const tasks = [
    { ...base, id: 'copy', kind: 'copy', sources: ['/copy.bin'], bytes_per_second: 10 * mib, eta_seconds: 5 },
    { ...base, id: 'move', kind: 'move', sources: ['/move.bin'], bytes_per_second: 5 * mib, eta_seconds: 65 },
    { ...base, id: 'upload', kind: 'upload', sources: ['/upload.bin'], phase: 'running', bytes_per_second: 2 * mib, eta_seconds: 120 },
    { ...base, id: 'delete', kind: 'delete', sources: ['/folder'], phase: 'deleting', progress_unit: 'items', progress_done: 25, progress_total: 100, items_done: 25, items_total: 100, items_per_second: 12.5, eta_seconds: 6 },
    { ...base, id: 'unknown', kind: 'delete', sources: ['/unknown'], phase: 'deleting', progress_unit: 'items', progress_done: 25, progress_total: 0, items_per_second: 0.5, eta_seconds: 100 },
    { ...base, id: 'paused', kind: 'copy', sources: ['/paused.bin'], paused: true, bytes_per_second: 10 * mib, eta_seconds: 5 },
    { ...base, id: 'finishing', kind: 'copy', sources: ['/finishing.bin'], phase: 'flushing', progress_done: 100 * mib, bytes_done: 100 * mib, bytes_per_second: 10 * mib, eta_seconds: 1 },
    { ...base, id: 'stalled', kind: 'copy', sources: ['/stalled.bin'], bytes_per_second: 0 },
    { ...base, id: 'warming', kind: 'copy', sources: ['/warming.bin'] },
    ...['extract', 'archive'].map(kind => ({ ...base, id: kind, kind, sources: [`/${kind}.zip`], phase: kind === 'extract' ? 'extracting' : 'archiving', progress_unit: 'percent', progress_done: 50, progress_total: 100 })),
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: tasks } }))
  await page.route(/\/api\/fs\/operations\/copy\/(pause|resume)$/, route => {
    const task = tasks[0]!
    task.paused = new URL(route.request().url()).pathname.endsWith('/pause')
    task.updated_at = task.paused ? '2026-01-01T00:00:01Z' : '2026-01-01T00:00:02Z'
    return route.fulfill({ json: task })
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const drawer = page.getByRole('complementary')
  const task = (name: string) => drawer.getByRole('article').filter({ hasText: name })
  await expect(task('Copy copy.bin')).toContainText('10.0 MiB/s')
  await expect(task('Copy copy.bin').locator('[data-task-eta]')).toHaveText('About 5s remaining')
  await expect(task('Move move.bin').locator('[data-task-eta]')).toHaveText('About 2m remaining')
  await expect(task('Upload upload.bin')).toContainText('2.00 MiB/s')
  await expect(task('Upload upload.bin').locator('[data-task-eta]')).toHaveText('About 2m remaining')
  await expect(task('Delete folder')).toContainText('12.5 items/s')
  await expect(task('Delete folder').locator('[data-task-eta]')).toHaveText('About 6s remaining')
  await expect(task('Delete unknown')).toContainText('0.5 items/s')
  await expect(task('Copy stalled.bin')).toContainText('Waiting for disk')
  await expect(task('Copy finishing.bin')).toContainText('Finishing')
  for (const name of ['Delete unknown', 'Copy paused.bin', 'Copy stalled.bin', 'Copy warming.bin', 'Copy finishing.bin', 'Extract extract.zip', 'Archive archive.zip']) {
    await expect(task(name).locator('[data-task-eta]')).toHaveCount(0)
  }
  for (const name of ['Copy paused.bin', 'Copy warming.bin', 'Extract extract.zip', 'Archive archive.zip']) {
    await expect(task(name)).not.toContainText('/s')
  }
  await task('Copy copy.bin').getByRole('button', { name: 'Pause Copy copy.bin', exact: true }).click()
  await expect(task('Copy copy.bin').locator('[data-task-eta]')).toHaveCount(0)
  await expect(task('Copy copy.bin')).not.toContainText('/s')
})
