import { expect, test } from '@playwright/test'

test('prompts for temporary elevation and exposes an immediate drop action', async ({ page }) => {
  let elevated = false
  let submittedPassword = ''
  const expiresAt = new Date(Date.now() + 5 * 60_000).toISOString()
  const session = () => ({
    authenticated: true,
    mode: 'pam',
    user: { username: 'alex', uid: 1000, gid: 1000, home: '/home/alex' },
    csrf_token: 'test-csrf',
    elevation_available: true,
    elevated,
    ...(elevated ? { elevation_expires_at: expiresAt } : {}),
  })

  await page.route('**/api/session', route => route.fulfill({ json: session() }))
  await page.route('**/api/elevation', async route => {
    if (route.request().method() === 'POST') {
      submittedPassword = (route.request().postDataJSON() as { password: string }).password
      elevated = true
      await route.fulfill({ json: session() })
      return
    }
    elevated = false
    await route.fulfill({ status: 204 })
  })
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{
        name: 'restricted',
        path: '/restricted',
        is_dir: true,
        can_navigate: elevated,
        size: 0,
        modified: '2000-01-01T00:00:00Z',
        mode: 'd---------',
      }],
    },
  }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Elevate access' }).click()
  const dialog = page.getByRole('dialog', { name: 'Elevate access' })
  await expect(dialog.getByText('Temporarily run file operations with root permissions.')).toBeVisible()
  await dialog.locator('input[type="password"]').fill('correct horse battery staple')
  await dialog.getByRole('button', { name: 'Elevate access' }).click()

  expect(submittedPassword).toBe('correct horse battery staple')
  await expect(page.getByRole('button', { name: 'Drop elevated access' })).toBeVisible()
  await expect(page.getByText(/Elevated until/)).toBeVisible()

  await page.getByRole('button', { name: 'Drop elevated access' }).click()
  await expect(page.getByRole('button', { name: 'Elevate access' })).toBeVisible()
})
