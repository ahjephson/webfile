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

test('undo preserves edited copies and exposes a useful failure', async ({ page }) => {
  const fixture = await featureFixture(page, 'undo-edited')
  const destination = `${fixture.path}/destination`
  const name = `edited-${fixture.path.split('/').pop()}.txt`
  await fixture.mkdir(destination)
  await fixture.write(`${fixture.path}/${name}`, 'original')
  const started = await fixture.start('copy', [`${fixture.path}/${name}`], destination)
  await expect.poll(async () => (await (await page.request.get('/api/fs/operations')).json()).operations.find((op: { id: string }) => op.id === started.id)?.undo_available).toBe(true)
  await fixture.write(`${destination}/${name}`, 'edited after copy')
  await page.goto(`/#path=${encodeURIComponent(destination)}`)
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await page.getByRole('button', { name: `Undo Copy ${name}`, exact: true }).click()
  await page.getByRole('alertdialog', { name: 'Undo copy?', exact: true }).getByRole('button', { name: 'Undo', exact: true }).click()
  const undo = page.getByRole('complementary').getByRole('article').filter({ hasText: `Undo copy ${name}` })
  await expect(undo.locator('[data-task-status]')).toHaveText('failed')
  await expect(undo).toContainText('Undo stopped because files or folders changed.')
  expect(await fixture.read(`${destination}/${name}`)).toBe('edited after copy')
  expect(await fixture.read(`${fixture.path}/${name}`)).toBe('original')
})
