import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: {
    path: '/Files', single_root_path: '/Files', entries: [{ name: 'locked.7z', path: '/Files/locked.7z', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
  } }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'application/x-7z-compressed' } }))
})

test('archive preview requests a password inline, retries and browses folders without extraction', async ({ page }) => {
  const passwords: string[] = []
  let operations = 0
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'POST') operations++
    await route.fulfill({ json: [] })
  })
  await page.route('**/api/fs/archive/preview', async route => {
    const { password } = route.request().postDataJSON() as { password: string }
    passwords.push(password)
    if (password !== 'secret') {
      await route.fulfill({ status: 422, json: { code: password ? 'archive_password_incorrect' : 'archive_password_required', error: password ? 'Incorrect password. Please try again.' : 'Enter the archive password.' } })
    } else {
      await route.fulfill({ json: { entries: [{ path: 'folder/hello.txt', size: 5, is_dir: false, encrypted: true }] } })
    }
  })
  await page.goto('/#path=%2FFiles')
  await page.locator('[data-file-entry]').filter({ hasText: 'locked.7z' }).dblclick()
  const dialog = page.getByRole('dialog', { name: 'Password required — locked.7z' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Later', exact: true })).toHaveCount(0)
  await dialog.locator('input[type=password]').fill('wrong')
  await dialog.getByRole('button', { name: 'Continue' }).click()
  await expect(dialog.getByRole('alert')).toHaveText('Incorrect password. Please try again.')
  await dialog.locator('input[type=password]').fill('secret')
  await dialog.getByRole('button', { name: 'Continue' }).click()
  await expect(dialog).toBeHidden()
  const contents = page.getByLabel('Archive contents', { exact: true })
  await contents.getByRole('button', { name: 'folder', exact: true }).click()
  await expect(contents.getByText('hello.txt', { exact: true })).toBeVisible()
  await contents.getByRole('button', { name: 'Up within archive' }).click()
  await expect(contents.getByRole('button', { name: 'folder', exact: true })).toBeVisible()
  expect(passwords).toEqual(['', 'wrong', 'secret'])
  expect(operations).toBe(0)
  expect(await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))).not.toContain('secret')
})

test('failed archive opening shows an error and retry instead of unsupported preview', async ({ page }) => {
  let fail = true
  await page.route('**/api/fs/archive/preview', route => fail
    ? route.fulfill({ status: 503, json: { error: 'The archive tool is unavailable. Ask your administrator to check the configured 7-Zip installation.' } })
    : route.fulfill({ json: { entries: [] } }))
  await page.goto('/#path=%2FFiles&file=%2FFiles%2Flocked.7z')
  await expect(page.getByRole('heading', { name: 'Could not open archive' })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText('archive tool is unavailable')
  await expect(page.getByText('No preview available', { exact: true })).toHaveCount(0)
  fail = false
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByText('This folder is empty.')).toBeVisible()
})

test('cancelling the archive password keeps an explicit cancelled state', async ({ page }) => {
  await page.route('**/api/fs/archive/preview', route => route.fulfill({ status: 422, json: { code: 'archive_password_required', error: 'Enter the archive password.' } }))
  await page.goto('/#path=%2FFiles&file=%2FFiles%2Flocked.7z')
  const dialog = page.getByRole('dialog', { name: 'Password required — locked.7z' })
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Archive preview cancelled')
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(dialog).toBeVisible()
})
