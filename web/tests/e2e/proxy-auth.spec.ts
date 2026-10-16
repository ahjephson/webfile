import { expect, test } from '@playwright/test'

test('failed session deletion is visible and sign out can be retried', async ({ page }) => {
  let attempts = 0, signedOut = false
  await page.route('**/api/session', route => route.fulfill({ json: {
    authenticated: !signedOut, mode: 'pam', user: { username: 'logout-test' }, csrf_token: 'test-csrf',
  } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.routeWebSocket('**/api/fs/ws', () => {})
  await page.route('**/api/logout', route => {
    if (++attempts === 1) return route.fulfill({ status: 500, json: { error: 'could not delete session; try signing out again' } })
    signedOut = true
    return route.fulfill({ status: 204 })
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await expect(page.getByText('Could not sign out', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await expect(page.getByLabel('Username', { exact: true })).toBeVisible()
  expect(attempts).toBe(2)
})

test('proxy-authenticated sessions use the configured external logout URL', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Playwright WebKit cannot fulfill intercepted requests with redirect responses')
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
  const applicationOrigin = new URL(page.url()).origin
  await page.getByRole('button', { name: 'Sign out' }).click()

  await expect.poll(() => logoutRequests).toBe(1)
  await expect(page).toHaveURL(/\/_auth\/signed-out$/)
  await expect(page.getByRole('heading', { name: 'Signed out' })).toBeVisible()
  expect(externalLogoutMethod).toBe('POST')
  expect(externalLogoutOrigin).toBe(applicationOrigin)
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

test('session network errors do not show the WebFile password form', async ({ page }) => {
  await page.route('**/api/session', route => route.abort('connectionfailed'))

  await page.goto('/')

  await expect(page.getByRole('heading', { name: 'Unable to connect to WebFile' })).toBeVisible()
  await expect(page.getByText('WebFile could not be reached. Check your connection and try again.')).toBeVisible()
  await expect(page.getByLabel('Username')).toHaveCount(0)
  await expect(page.getByLabel('Password')).toHaveCount(0)
})

test('a transient session refresh failure retains an established proxy session', async ({ page }) => {
  let sessionRequests = 0
  await page.route('**/api/session', route => {
    sessionRequests++
    if (sessionRequests > 1) return route.abort('connectionfailed')
    return route.fulfill({
      json: {
        authenticated: true,
        mode: 'proxy',
        user: { username: 'proxy-user', uid: 1000, gid: 1000, home: '/home/proxy-user' },
        csrf_token: 'csrf-token',
        elevation_available: true,
        elevated: true,
        elevation_expires_at: new Date(Date.now() + 50).toISOString(),
      },
    })
  })

  await page.goto('/')

  await expect(page.getByText('proxy-user', { exact: true })).toBeVisible()
  await expect.poll(() => sessionRequests).toBeGreaterThan(1)
  await expect(page.getByText('proxy-user', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Unable to connect to WebFile' })).toHaveCount(0)
  await expect(page.getByLabel('Username')).toHaveCount(0)
  await expect(page.getByLabel('Password')).toHaveCount(0)
})
