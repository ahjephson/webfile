import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

for (const reload of [false, true]) {
  test(`upload batch registers waiting tasks and ${reload ? 'recovers after reload' : 'continues after failure and queued cancellation across navigation'}`, async ({ page }) => {
    const fixture = await featureFixture(page, 'upload-queue')
    let release = () => {}
    const hold = new Promise<void>(resolve => { release = resolve })
    let stalled = false
    await page.route('**/api/fs/uploads/chunk?**', async route => {
      if (new URL(route.request().url()).searchParams.get('offset') === String(8 * 1024 * 1024)) {
        stalled = true
        await hold
        if (!reload) {
          await route.fulfill({ status: 500, contentType: 'text/html', body: 'Proxy upload failure' })
          return
        }
      }
      await route.continue().catch(() => {})
    })
    await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
    await page.locator('input[type=file]').setInputFiles([
      { name: 'first.bin', mimeType: 'application/octet-stream', buffer: Buffer.alloc(9 * 1024 * 1024, 1) },
      { name: 'second.txt', mimeType: 'text/plain', buffer: Buffer.from('second') },
      { name: 'third.txt', mimeType: 'text/plain', buffer: Buffer.from('third') },
    ])
    await expect.poll(() => stalled).toBe(true)
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    const drawer = page.getByRole('complementary')
    const second = drawer.getByRole('article').filter({ hasText: 'Upload second.txt' }).filter({ hasText: fixture.path })
    const third = drawer.getByRole('article').filter({ hasText: 'Upload third.txt' }).filter({ hasText: fixture.path })
    await expect(second).toHaveAttribute('data-task-state', 'queued')
    await expect(third).toHaveAttribute('data-task-state', 'queued')
    await expect(second.getByText('Waiting to upload', { exact: true })).toBeVisible()
    await expect(second.getByRole('button', { name: /Move .* up/ })).toHaveCount(0)
    if (reload) {
      await page.reload()
      release()
      await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
      await expect(second.locator('[data-task-status]')).toHaveText('failed', { timeout: 12000 })
      await expect(third.locator('[data-task-status]')).toHaveText('failed')
      expect(await fixture.exists(`${fixture.path}/second.txt`)).toBe(false)
    } else {
      // A second batch joins the same transfer queue, retaining its destination.
      await drawer.getByRole('button', { name: 'Close', exact: true }).click()
      await expect(drawer).not.toBeVisible()
      const chooserPromise = page.waitForEvent('filechooser')
      await page.getByRole('button', { name: 'Upload', exact: true }).click()
      const chooser = await chooserPromise
      await chooser.setFiles({ name: 'fourth.txt', mimeType: 'text/plain', buffer: Buffer.from('fourth') })
      await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
      const fourth = drawer.getByRole('article').filter({ hasText: 'Upload fourth.txt' }).filter({ hasText: fixture.path })
      await expect(fourth).toHaveAttribute('data-task-state', 'queued')
      await drawer.getByRole('button', { name: 'Close', exact: true }).click()
      await page.getByRole('button', { name: 'Files', exact: true }).click()
      await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
      await third.getByRole('button', { name: 'Cancel', exact: true }).click()
      await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel task', exact: true }).click()
      await expect(third.locator('[data-task-status]')).toHaveText('cancelled')
      release()
      await expect(second.locator('[data-task-status]')).toHaveText('completed', { timeout: 15000 })
      await expect(fourth.locator('[data-task-status]')).toHaveText('completed')
      const first = drawer.getByRole('article').filter({ hasText: 'Upload first.bin' }).filter({ hasText: fixture.path })
      await expect(first.locator('[data-task-status]')).toHaveText('failed')
      expect(await fixture.read(`${fixture.path}/second.txt`)).toBe('second')
      expect(await fixture.read(`${fixture.path}/fourth.txt`)).toBe('fourth')
      expect(await fixture.exists(`${fixture.path}/third.txt`)).toBe(false)
    }
  })
}
