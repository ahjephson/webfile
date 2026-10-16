import { expect, test, type Page } from '@playwright/test'

const task = (id: string, state: string, updated = 1) => ({ id, kind: 'upload', sources: [`/docs/${id}.txt`],
  state, phase: state, message: state, bytes_done: 1, bytes_total: 10,
  progress_done: 1, progress_total: 10, progress_unit: 'bytes', cancellable: state === 'running',
  started_at: '2000-01-01T00:00:00Z', updated_at: `2000-01-01T00:00:00.00000${updated}Z` })

async function taskFolder(page: Page) {
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'tasks-test' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/docs', entries: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  let send = (_message: unknown) => {}
  await page.routeWebSocket('**/api/fs/ws', socket => { send = message => socket.send(JSON.stringify(message)) })
  return { send: (message: unknown) => send(message) }
}

for (const action of ['clear', 'remove']) {
  test(`a delayed history response cannot undo ${action} while live active updates are retained`, async ({ page }) => {
    const realtime = await taskFolder(page)
    let reads = 0, removed = false
    let release!: () => void
    const held = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/fs/operations', async route => {
      const index = ++reads
      const snapshot = removed ? [] : [task('finished', 'completed')]
      if (index === 2) await held
      await route.fulfill({ json: { operations: snapshot } })
    })
    await page.route('**/api/fs/operations/history', route => { removed = true; return route.fulfill({ json: { removed: 1 } }) })
    await page.route('**/api/fs/operations/finished', route => { removed = true; return route.fulfill({ status: 204 }) })
    await page.goto('/#path=%2Fdocs')
    await expect.poll(() => reads).toBe(2)
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const panel = page.getByRole('complementary')
    await expect(panel.getByText('Upload finished.txt', { exact: true })).toBeVisible()
    if (action === 'clear') {
      await page.getByRole('button', { name: 'Clear task history', exact: true }).click()
      await page.getByRole('alertdialog', { name: 'Clear task history' }).getByRole('button', { name: 'Clear history', exact: true }).click()
    } else {
      await page.getByRole('button', { name: 'Remove Upload finished.txt from history', exact: true }).click()
      await page.getByRole('alertdialog', { name: 'Remove task from history' }).getByRole('button', { name: 'Remove', exact: true }).click()
    }
    await expect(panel.getByText('Upload finished.txt', { exact: true })).toHaveCount(0)
    realtime.send({ type: 'operation-updated', operation: task('active', 'running', 2) })
    await expect(panel.getByText('Upload active.txt', { exact: true })).toBeVisible()
    release()
    await page.waitForLoadState('networkidle')
    await expect(panel.getByText('Upload finished.txt', { exact: true })).toHaveCount(0)
    await expect(panel.getByText('Upload active.txt', { exact: true })).toBeVisible()
  })
}

test('reused upload IDs show each new run and reject stale updates from earlier runs', async ({ page }) => {
  const realtime = await taskFolder(page)
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [task('repeat', 'completed')] } }))
  await page.goto('/#path=%2Fdocs')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const panel = page.getByRole('complementary')
  await expect(panel.getByText('Upload repeat.txt', { exact: true })).toBeVisible()
  for (const update of [2, 4]) {
    realtime.send({ type: 'operation-updated', operation: task('repeat', 'running', update) })
    await expect(page.getByRole('button', { name: 'Collapse Active (1)', exact: true })).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible()
    realtime.send({ type: 'operation-updated', operation: task('repeat', 'completed', update - 1) })
    await expect(panel.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible()
    realtime.send({ type: 'operation-updated', operation: task('repeat', 'completed', update + 1) })
    await expect(panel.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Collapse History (1)', exact: true })).toBeVisible()
    await expect(page.getByText('Upload complete', { exact: true })).toBeVisible()
    await expect(page.getByText('Upload complete', { exact: true })).toHaveCount(0)
  }
})

test('completed copy tasks still ignore newer active messages', async ({ page }) => {
  const realtime = await taskFolder(page)
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [{ ...task('copy', 'completed'), kind: 'copy' }] } }))
  await page.goto('/#path=%2Fdocs')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect(page.getByRole('complementary').getByText('Copy copy.txt', { exact: true })).toBeVisible()
  realtime.send({ type: 'operation-updated', operation: { ...task('copy', 'running', 2), kind: 'copy' } })
  await expect(page.getByRole('button', { name: 'Collapse History (1)', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Collapse Active (1)', exact: true })).toHaveCount(0)
})
