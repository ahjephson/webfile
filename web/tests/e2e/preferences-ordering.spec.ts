import { expect, test } from '@playwright/test'

for (const firstFails of [false, true]) {
  test(`preference writes preserve choice order when the first save ${firstFails ? 'fails' : 'is slow'}`, async ({ page }) => {
    await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'preferences-test' } } }))
    await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/docs', entries: [] } }))
    await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
    await page.routeWebSocket('**/api/fs/ws', () => {})
    let saved = { view_mode: 'list', theme: 'dark' }, writes = 0, active = 0, peak = 0
    let release!: () => void
    const held = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/preferences', async route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: saved })
      const index = ++writes
      peak = Math.max(peak, ++active)
      const snapshot = route.request().postDataJSON()
      if (index === 1) await held
      active--
      if (index === 1 && firstFails) return route.fulfill({ status: 500, json: { error: 'temporary failure' } })
      saved = snapshot
      return route.fulfill({ json: saved })
    })
    await page.goto('/#path=%2Fdocs')
    const list = page.getByRole('button', { name: 'List view', exact: true })
    await expect(list).toHaveClass(/webfile-view-active/)
    await page.getByRole('button', { name: 'Grid view', exact: true }).click()
    await expect.poll(() => writes).toBe(1)
    await list.click()
    await expect(list).toHaveClass(/webfile-view-active/)
    // Keep the first response held through the second action's watcher and
    // request dispatch; this is the network overlap that caused the defect.
    await page.waitForTimeout(200)
    release()
    await expect.poll(() => writes).toBe(2)
    await page.waitForLoadState('networkidle')
    expect(peak).toBe(1)
    expect(saved.view_mode).toBe('list')
    await page.reload()
    await expect(list).toHaveClass(/webfile-view-active/)
  })
}
