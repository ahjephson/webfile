import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

for (const kind of ['copy', 'move'] as const) {
  test(`undo ${kind} from task history after reload`, async ({ page }) => {
    const fixture = await featureFixture(page, `undo-${kind}`)
    const name = `original-${fixture.path.split('/').pop()}.txt`
    const source = `${fixture.path}/${name}`
    const destination = `${fixture.path}/destination`
    await fixture.mkdir(destination)
    await fixture.write(source, 'keep this content')
    const started = await fixture.start(kind, [source], destination)
    const current = async () => (await (await page.request.get('/api/fs/operations')).json()).operations.find((op: { id: string }) => op.id === started.id)
    await expect.poll(async () => (await current())?.undo_available).toBe(true)
    await page.goto(`/#path=${encodeURIComponent(destination)}`)
    await page.reload()
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const original = page.getByRole('complementary').getByRole('article').filter({ has: page.getByRole('button', { name: `Undo ${kind[0].toUpperCase()}${kind.slice(1)} ${name}`, exact: true }) })
    await original.getByRole('button', { name: /^Undo / }).click()
    const dialog = page.getByRole('alertdialog', { name: `Undo ${kind}?`, exact: true })
    await expect(dialog).toContainText('can only be attempted once')
    await dialog.getByRole('button', { name: 'Undo', exact: true }).click()
    const undo = page.getByRole('complementary').getByRole('article').filter({ hasText: `Undo ${kind} ${name}` })
    await expect(undo.locator('[data-task-status]')).toHaveText('completed')
    await expect.poll(() => fixture.exists(`${destination}/${name}`)).toBe(false)
    expect(await fixture.read(source)).toBe('keep this content')
    await expect.poll(async () => (await current())?.undo_available ?? false).toBe(false)
    await expect(original.getByRole('button', { name: /^Undo / })).toHaveCount(0)
    // A repeated request cannot replay the filesystem mutation.
    expect((await page.request.post(`/api/fs/operations/${started.id}/undo`)).status()).toBe(409)
  })
}

test('saving a file clears the previous undo set across tabs', async ({ page }) => {
  const fixture = await featureFixture(page, 'undo-edited')
  const destination = `${fixture.path}/destination`
  const name = `edited-${fixture.path.split('/').pop()}.txt`
  await fixture.mkdir(destination)
  await fixture.write(`${fixture.path}/${name}`, 'original')
  const started = await fixture.start('copy', [`${fixture.path}/${name}`], destination)
  await expect.poll(async () => (await (await page.request.get('/api/fs/operations')).json()).operations.find((op: { id: string }) => op.id === started.id)?.undo_available).toBe(true)
  await page.goto(`/#path=${encodeURIComponent(destination)}`)
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  const other = await page.context().newPage()
  await other.goto(`/#path=${encodeURIComponent(destination)}`)
  await other.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect(other.getByRole('button', { name: `Undo Copy ${name}`, exact: true })).toBeVisible()
  await fixture.write(`${destination}/${name}`, 'edited after copy')
  await expect(page.getByRole('button', { name: `Undo Copy ${name}`, exact: true })).toHaveCount(0)
  await expect(other.getByRole('button', { name: `Undo Copy ${name}`, exact: true })).toHaveCount(0)
  expect((await page.request.post(`/api/fs/operations/${started.id}/undo`)).status()).toBe(409)
  expect(await fixture.read(`${destination}/${name}`)).toBe('edited after copy')
  expect(await fixture.read(`${fixture.path}/${name}`)).toBe('original')
  await other.close()
})

test('only the newest transfer is undoable and delete clears it', async ({ page }) => {
  const f = await featureFixture(page, 'undo-latest')
  await f.mkdir(`${f.path}/to`)
  await f.write(`${f.path}/first.txt`, 'first')
  await f.write(`${f.path}/second.txt`, 'second')
  await f.write(`${f.path}/delete-me.txt`, 'disposable')
  const first = await f.start('copy', [`${f.path}/first.txt`], `${f.path}/to`)
  const current = async (id: string) => (await (await page.request.get('/api/fs/operations')).json()).operations.find((op: { id: string }) => op.id === id)
  await expect.poll(async () => (await current(first.id))?.undo_available).toBe(true)
  // Directory reads, previews and downloads preserve the set.
  expect(await f.read(`${f.path}/first.txt`)).toBe('first')
  expect((await page.request.get(`/api/fs/download?path=${encodeURIComponent(`${f.path}/first.txt`)}`)).ok()).toBe(true)
  expect((await current(first.id)).undo_available).toBe(true)
  const second = await f.start('move', [`${f.path}/second.txt`], `${f.path}/to`)
  await expect.poll(async () => (await current(second.id))?.undo_available).toBe(true)
  expect((await current(first.id)).undo_available ?? false).toBe(false)
  expect((await page.request.post(`/api/fs/operations/${first.id}/undo`)).status()).toBe(409)
  await f.start('delete', [`${f.path}/delete-me.txt`], '')
  await expect.poll(async () => (await current(second.id))?.undo_available ?? false).toBe(false)
  expect((await page.request.post(`/api/fs/operations/${second.id}/undo`)).status()).toBe(409)
  expect(await f.read(`${f.path}/to/second.txt`)).toBe('second')
})

test('the undo button expires locally even if a history response is stale', async ({ page }) => {
  const expires = new Date(Date.now() + 8000).toISOString()
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [{
    id: 'expiring-copy', kind: 'copy', sources: ['/file.txt'], destination: '/to', state: 'completed',
    undo_available: true, undo_expires_at: expires, started_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    cancellable: false, progress_done: 1, progress_total: 1, bytes_done: 0, bytes_total: 0, items_done: 1, items_total: 1,
  }] } }))
  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Undo Copy file.txt', exact: true })).toBeVisible()
  await expect(page.getByRole('complementary')).toContainText('Undo available until')
  await expect(page.getByRole('button', { name: 'Undo Copy file.txt', exact: true })).toHaveCount(0, { timeout: 12000 })
})
