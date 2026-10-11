import { expect, test } from '@playwright/test'

test('proxy-authenticated sessions use the configured external logout URL', async ({ page }) => {
  let logoutRequests = 0
  let externalLogoutMethod = ''
  let externalLogoutOrigin = ''
  await page.route('**/api/session', route => route.fulfill({
    json: {
      authenticated: true,
      mode: 'proxy',
      user: { username: 'proxy-user', uid: 1000, gid: 1000, home: '/home/proxy-user' },
      csrf_token: 'csrf-token',
      elevation_available: true,
      elevated: false,
      logout_url: '/_auth/logout',
    },
  }))
  await page.route('**/api/logout', route => {
    logoutRequests++
    return route.fulfill({ status: 204 })
  })
  await page.route('**/_auth/logout', async route => {
    externalLogoutMethod = route.request().method()
    externalLogoutOrigin = route.request().headers().origin ?? ''
    return route.fulfill({ status: 303, headers: { location: '/_auth/signed-out' } })
  })
  await page.route('**/_auth/signed-out', route => route.fulfill({
    contentType: 'text/html',
    body: '<!doctype html><title>Signed out</title><h1>Signed out</h1>',
  }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Sign out' }).click()

  await expect.poll(() => logoutRequests).toBe(1)
  await expect(page).toHaveURL(/\/_auth\/signed-out$/)
  await expect(page.getByRole('heading', { name: 'Signed out' })).toBeVisible()
  expect(externalLogoutMethod).toBe('POST')
  expect(externalLogoutOrigin).toBe('http://127.0.0.1:8080')
})

test('proxy mode does not offer the WebFile password form when identity is missing', async ({ page }) => {
  await page.route('**/api/session', route => route.fulfill({
    json: { authenticated: false, mode: 'proxy' },
  }))

  await page.goto('/')

  await expect(page.getByRole('heading', { name: 'External authentication required' })).toBeVisible()
  await expect(page.getByLabel('Username')).toHaveCount(0)
  await expect(page.getByLabel('Password')).toHaveCount(0)
})
