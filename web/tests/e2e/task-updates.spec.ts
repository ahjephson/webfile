import { expect, test, type Page } from '@playwright/test'
import { featureFixture } from './feature-fixture'

const task = (id: string, state: string, updated = 1) => ({ id, kind: 'upload', sources: [`/docs/${id}.txt`],
  state, phase: state, message: state, bytes_done: 1, bytes_total: 10,
  progress_done: 1, progress_total: 10, progress_unit: 'bytes', cancellable: state === 'running',
  started_at: '2000-01-01T00:00:00Z', updated_at: `2000-01-01T00:00:00.00000${updated}Z` })

for (const rejectedRequest of ['init-response', 'chunk']) {
  for (const blockedReport of [false, true]) {
    test(`proxy 500 on ${rejectedRequest} fails the task${blockedReport ? ' even while failure reports are blocked and after reload' : ''}`, async ({ page }) => {
      const fixture = await featureFixture(page, 'proxy-upload-failure')
      let reportsBlocked = blockedReport
      let initial: { upload: { id: string; source_modified: number }; operation: { id: string; attempt_id: string } } | undefined
      await page.route('**/api/fs/uploads/init', async route => {
        const response = await route.fetch()
        initial = await response.json()
        if (rejectedRequest === 'init-response') {
          await route.fulfill({ status: 500, contentType: 'text/html', body: '<html>nginx upstream failure</html>' })
        } else await route.fulfill({ response })
      })
      await page.route('**/api/fs/uploads/chunk?**', route => route.fulfill({ status: 500, contentType: 'text/html', body: '<html>nginx upload failure</html>' }))
      await page.route('**/api/fs/uploads/failure', async route => {
        if (reportsBlocked) await route.fulfill({ status: 502, contentType: 'text/html', body: '<html>nginx unavailable</html>' })
        else await route.continue()
      })
      await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
      const name = `proxy-${fixture.path.split('/').pop()}.txt`
      await page.locator('input[type=file]').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from('data') })
      await expect.poll(() => initial?.operation.id).toBeTruthy()
      const readTask = async () => (await (await page.request.get('/api/fs/operations')).json()).operations.find((op: { id: string }) => op.id === initial!.operation.id)
      await expect(page.getByRole('button', { name: 'Open tasks', exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
      const upload = page.getByRole('complementary').getByRole('article').filter({ hasText: name })
      await expect(upload.locator('[data-task-status]')).toHaveText('failed', { timeout: 10000 })
      await expect(upload.getByText('The transfer was interrupted. Please try again to resume it.', { exact: true })).toBeVisible()
      if (blockedReport) {
        // Neither rejected request reached the backend's first-chunk transition.
        expect((await readTask()).state).toBe('queued')
        await page.reload()
        await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
        await expect(upload.locator('[data-task-status]')).toHaveText('failed')
        await expect(upload.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(0)
        reportsBlocked = false
      }
      await expect.poll(async () => (await readTask()).state, { timeout: 12000 }).toBe('failed')
      expect((await readTask()).error).toBe('The transfer was interrupted. Please try again to resume it.')
      // The report retains staging. Retry the same file identity and publish it.
      const resumedResponse = await page.request.post('/api/fs/uploads/init', { data: {
        dir: fixture.path, name, size: 4, last_modified: initial!.upload.source_modified,
      } })
      expect(resumedResponse.ok()).toBe(true)
      const resumed = await resumedResponse.json()
      expect(resumed.upload.id).toBe(initial!.upload.id)
      expect(resumed.operation.attempt_id).not.toBe(initial!.operation.attempt_id)
      const completed = await page.request.put(`/api/fs/uploads/chunk?id=${resumed.upload.id}&offset=0&attempt_id=${resumed.operation.attempt_id}`, { data: 'data', headers: { 'Content-Type': 'application/octet-stream' } })
      expect(completed.ok()).toBe(true)
      expect((await completed.json()).operation.state).toBe('completed')
      await expect.poll(() => fixture.read(`${fixture.path}/${name}`)).toBe('data')
    })
  }
}

test('real task failures expose a useful reason without server paths, including after reload', async ({ page }) => {
  const fixture = await featureFixture(page, 'task-error-privacy')
  const name = `missing-${fixture.path.split('/').pop()}.txt`
  const started = await fixture.start('copy', [`${fixture.path}/${name}`], fixture.path)
  let failed: { state: string; error?: string } | undefined
  await expect.poll(async () => {
    const response = await page.request.get('/api/fs/operations')
    failed = (await response.json()).operations.find((operation: { id: string }) => operation.id === started.id)
    return failed?.state
  }).toBe('failed')
  expect(failed?.error).toBe('The file or folder no longer exists.')
  for (const reload of [false, true]) {
    if (reload) await page.reload()
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const task = page.getByRole('complementary').getByRole('article').filter({ hasText: name })
    await expect(task.locator('[data-task-status]')).toHaveText('failed')
    await expect(task.getByText('The file or folder no longer exists.', { exact: true })).toBeVisible()
    await expect(task).not.toContainText(/lstat|\/home\/|\/tmp\/|\.part/)
    await page.getByRole('complementary').getByRole('button', { name: 'Close', exact: true }).click()
  }
})

test('upload HTTP 500 reconciles failed history without a WebSocket event', async ({ page }) => {
  await taskFolder(page)
  let failed = false
  const running = task('retry', 'running')
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: failed
    ? [{ ...task('retry', 'failed', 2), error: 'Disk full', cancellable: false }] : [] } }))
  await page.route('**/api/fs/uploads/init', route => route.fulfill({ json: {
    upload: { id: 'retry', offset: 0, size: 1 }, operation: running,
  } }))
  await page.route('**/api/fs/uploads/chunk?**', route => {
    failed = true
    return route.fulfill({ status: 500, json: { error: 'Disk full' } })
  })
  await page.goto('/#path=%2Fdocs')
  await page.locator('input[type=file]').setInputFiles({ name: 'retry.txt', mimeType: 'text/plain', buffer: Buffer.from('x') })
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const upload = page.getByRole('complementary').getByRole('article').filter({ hasText: 'Upload retry.txt' })
  await expect(upload.locator('[data-task-status]')).toHaveText('failed')
  await expect(page.getByRole('button', { name: 'Collapse History (1)', exact: true })).toBeVisible()
  await expect(upload.getByText('Disk full', { exact: true })).toBeVisible()
  await expect(upload.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(0)
})

for (const kind of ['upload', 'copy', 'move', 'delete', 'extract', 'archive']) {
  test(`${kind} reconciles a dropped failure event while tasks remain closed`, async ({ page }) => {
    await taskFolder(page)
    let failed = false, reads = 0
    await page.route('**/api/fs/operations', route => {
      reads++
      return route.fulfill({ json: { operations: [{ ...task('lost', failed ? 'failed' : 'running', failed ? 2 : 1), kind,
        error: failed ? 'Filesystem write failed' : undefined, cancellable: !failed }] } })
    })
    await page.goto('/#path=%2Fdocs')
    await expect.poll(() => reads).toBeGreaterThanOrEqual(2)
    failed = true
    await expect(page.getByText(/^(Upload|Copy|Move|Delete|Extract|Archive) failed$/, { exact: true })).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const operation = page.getByRole('complementary').getByRole('article').filter({ hasText: 'lost.txt' })
    await expect(operation.locator('[data-task-status]')).toHaveText('failed')
    await expect(operation.getByText('Filesystem write failed', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Collapse History (1)', exact: true })).toBeVisible()
    await expect(operation.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(0)
  })
}

test('large-folder deletion shows an indeterminate bar and removed count without inventing a total', async ({ page }) => {
  const realtime = await taskFolder(page)
  const operation = { ...task('large-folder', 'running'), kind: 'delete', phase: 'deleting', sources: ['/docs/large-folder'],
    message: 'Deleting large-folder', progress_done: 25, progress_total: 0, progress_unit: 'items',
    bytes_done: 0, bytes_total: 0, items_done: 25, items_total: 0 }
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [operation] } }))
  await page.goto('/#path=%2Fdocs')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const deletion = page.getByRole('complementary').getByRole('article').filter({ hasText: 'Delete large-folder' })
  await expect(deletion.getByText('Deleting large-folder', { exact: true })).toBeVisible()
  await expect(deletion.getByText('25 items', { exact: true })).toBeVisible()
  const progress = deletion.getByRole('progressbar', { name: 'Delete large-folder progress', exact: true })
  await expect(progress).toBeVisible()
  await expect(progress).not.toHaveAttribute('aria-valuenow')
  await expect(progress).toHaveAttribute('aria-valuetext', '25 items')
  const indicator = progress.locator('.webfile-indeterminate')
  const transform = await indicator.evaluate(element => getComputedStyle(element).transform)
  await expect.poll(() => indicator.evaluate(element => getComputedStyle(element).transform)).not.toBe(transform)
  realtime.send({ type: 'operation-updated', operation: { ...operation, progress_done: 256, items_done: 256,
    updated_at: '2000-01-01T00:00:02Z' } })
  await expect(deletion.getByText('256 items', { exact: true })).toBeVisible()
  await expect(progress).toHaveAttribute('aria-valuetext', '256 items')
  await expect(deletion.getByText(/\/ 0 items/)).toHaveCount(0)
  await expect(deletion.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await expect(indicator).toHaveCSS('transform', 'none')
  const barWidth = (await progress.boundingBox())!.width
  await expect.poll(async () => (await indicator.boundingBox())!.width).toBeCloseTo(barWidth, 0)
  realtime.send({ type: 'operation-updated', operation: { ...operation, state: 'cancelling', phase: 'cancelling', cancellable: false,
    progress_done: 256, items_done: 256, updated_at: '2000-01-01T00:00:03Z' } })
  await expect(deletion).toHaveAttribute('data-task-state', 'cancelling')
  await expect(progress).toBeVisible()
  await expect(progress).not.toHaveAttribute('aria-valuenow')
  realtime.send({ type: 'operation-updated', operation: { ...operation, state: 'completed', phase: 'completed', cancellable: false,
    message: 'Deleted 256 items', progress_done: 256, progress_total: 256, items_done: 256, items_total: 256,
    updated_at: '2000-01-01T00:00:04Z', finished_at: '2000-01-01T00:00:04Z' } })
  await expect(page.getByRole('button', { name: 'Collapse History (1)', exact: true })).toBeVisible()
  await expect(deletion.getByRole('progressbar')).toHaveCount(0)
})

test('delete progress switches from scanning to a counted bar and retains it during cancellation', async ({ page }) => {
  const realtime = await taskFolder(page)
  const operation = { ...task('counted-folder', 'running'), kind: 'delete', phase: 'scanning', sources: ['/docs/counted-folder'],
    message: 'Scanning', progress_done: 0, progress_total: 0, progress_unit: '',
    bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0 }
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [operation] } }))
  await page.goto('/#path=%2Fdocs')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const deletion = page.getByRole('complementary').getByRole('article').filter({ hasText: 'Delete counted-folder' })
  const progress = deletion.getByRole('progressbar')
  await expect(deletion.getByText('Scanning', { exact: true })).toBeVisible()
  await expect(progress).not.toHaveAttribute('aria-valuenow')
  await expect(progress.locator('.webfile-indeterminate')).toBeVisible()
  const counted = { ...operation, phase: 'deleting', message: 'Deleting counted-folder', progress_done: 25, progress_total: 100,
    progress_unit: 'items', items_done: 25, items_total: 100, updated_at: '2000-01-01T00:00:02Z' }
  realtime.send({ type: 'operation-updated', operation: counted })
  await expect(progress).toHaveAttribute('aria-valuenow', '25')
  await expect(progress.locator('.webfile-indeterminate')).toHaveCount(0)
  await expect(deletion.getByText('25 / 100 items', { exact: true })).toBeVisible()
  realtime.send({ type: 'operation-updated', operation: { ...counted, progress_done: 50, items_done: 50,
    updated_at: '2000-01-01T00:00:03Z' } })
  await expect(progress).toHaveAttribute('aria-valuenow', '50')
  realtime.send({ type: 'operation-updated', operation: { ...counted, state: 'cancelling', phase: 'cancelling', cancellable: false,
    progress_done: 50, items_done: 50, updated_at: '2000-01-01T00:00:04Z' } })
  await expect(deletion).toHaveAttribute('data-task-state', 'cancelling')
  await expect(progress).toHaveAttribute('aria-valuenow', '50')
  await expect(progress).toHaveAttribute('aria-valuetext', '50 / 100 items')
})

test('transfer progress retains its main details without redundant file phase text', async ({ page }) => {
  const realtime = await taskFolder(page)
  const operation = { ...task('folder', 'running'), kind: 'move', phase: 'scanning', message: 'Scanning',
    progress_done: 0, progress_total: 0, progress_unit: '', bytes_done: 0, bytes_total: 0 }
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [operation] } }))
  await page.goto('/#path=%2Fdocs')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const panel = page.getByRole('complementary')
  const transfer = panel.getByRole('article').filter({ hasText: 'Move folder.txt' })
  await expect(transfer.getByText('Scanning', { exact: true })).toHaveCount(1)
  let updated = 2
  for (const phase of ['copying', 'flushing', 'publishing', 'copying']) {
    realtime.send({ type: 'operation-updated', operation: { ...operation, phase, message: 'Copying folder',
      progress_done: updated, progress_total: 10, progress_unit: 'bytes', bytes_done: updated, bytes_total: 10,
      updated_at: `2000-01-01T00:00:0${updated++}Z` } })
    await expect(transfer.getByRole('progressbar')).toHaveAttribute('aria-valuenow', `${(updated - 1) * 10}`)
    await expect(transfer.getByText('Copying folder', { exact: true })).toBeVisible()
    await expect(transfer.getByText(/^(Copying|Flushing|Publishing)$/)).toHaveCount(0)
    await expect(transfer.getByText(/B.*·.*(Copying|Flushing|Publishing)/)).toHaveCount(0)
  }
})

async function taskFolder(page: Page) {
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'tasks-test' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/docs', entries: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  let send = (_message: unknown) => {}
  await page.routeWebSocket('**/api/fs/ws', socket => { send = message => socket.send(JSON.stringify(message)) })
  return { send: (message: unknown) => send(message) }
}

test('a proxy failure still fails newer realtime progress when history and reporting are unavailable', async ({ page }) => {
  const realtime = await taskFolder(page)
  const running = { ...task('newer-progress', 'running'), attempt_id: 'a'.repeat(32) }
  await page.route('**/api/fs/operations', route => route.fulfill({ status: 502, body: 'proxy unavailable' }))
  await page.route('**/api/fs/uploads/failure', route => route.fulfill({ status: 502, body: 'proxy unavailable' }))
  await page.route('**/api/fs/uploads/init', route => route.fulfill({ json: { upload: { id: 'newer-progress', offset: 0, size: 4 }, operation: running } }))
  await page.route('**/api/fs/uploads/chunk?**', async route => {
    realtime.send({ type: 'operation-updated', operation: { ...running, bytes_done: 2, progress_done: 2, updated_at: '2000-01-01T00:00:01Z' } })
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    await expect(page.getByRole('complementary').getByRole('article').getByRole('progressbar')).toHaveAttribute('aria-valuenow', '20')
    await route.fulfill({ status: 500, contentType: 'text/html', body: '<html>nginx error</html>' })
  })
  await page.goto('/#path=%2Fdocs')
  await page.locator('input[type=file]').setInputFiles({ name: 'newer-progress.txt', mimeType: 'text/plain', buffer: Buffer.from('data') })
  const upload = page.getByRole('complementary').getByRole('article').filter({ hasText: 'newer-progress.txt' })
  await expect(upload.locator('[data-task-status]')).toHaveText('failed')
})

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
