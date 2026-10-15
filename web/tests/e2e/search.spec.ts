import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

for (const olderResult of ['success', 'error'] as const) {
  test(`Show in folder waits for the newest listing after an older ${olderResult}`, async ({ page }) => {
    const entry = { name: 'report.txt', path: '/Files/nested/report.txt', is_dir: false, size: 12, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }
    let releaseOlder!: () => void
    let releaseNewer!: () => void
    const older = new Promise<void>(resolve => { releaseOlder = resolve })
    const newer = new Promise<void>(resolve => { releaseNewer = resolve })
    let targetReads = 0
    await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
    await page.route('**/api/fs/dir**', async route => {
      const path = new URL(route.request().url()).searchParams.get('path')
      if (path === '/Files/nested') {
        const read = ++targetReads
        await (read === 1 ? older : newer)
        if (read === 1 && olderResult === 'error') return route.fulfill({ status: 500, json: { error: 'superseded listing failed' } })
        return route.fulfill({ json: { path, entries: [entry] } })
      }
      await route.fulfill({ json: { path, entries: [{ name: 'nested', path: '/Files/nested', is_dir: true, size: 0, modified: entry.modified, mode: 'drwxr-xr-x' }] } })
    })
    await page.route('**/api/fs/search', route => route.fulfill({
      contentType: 'application/x-ndjson',
      body: [JSON.stringify({ entry, scanned: 1, skipped: 0 }), JSON.stringify({ done: true, scanned: 1, skipped: 0 })].join('\n') + '\n',
    }))
    await page.goto('/#path=%2FFiles')
    await expect(page).toHaveURL(/#path=%2FFiles$/)
    await page.getByRole('button', { name: 'Show selection controls', exact: true }).click()
    await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
    await page.getByRole('textbox', { name: 'Search filenames' }).fill('report')
    await page.getByRole('button', { name: 'Search', exact: true }).click()
    await expect(page.getByRole('status')).toContainText('Search complete')
    await page.locator('[data-file-entry]').filter({ hasText: 'report.txt' }).click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Show in folder', exact: true }).click()
    await expect.poll(() => targetReads).toBe(2)
    const olderResponse = page.waitForResponse(response => new URL(response.url()).searchParams.get('path') === '/Files/nested')
    releaseOlder()
    await olderResponse
    releaseNewer()
    await expect(page.getByRole('button', { name: 'Unselect report.txt', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByText('This search result is no longer available', { exact: true })).toHaveCount(0)
    await expect(page.getByText('superseded listing failed', { exact: true })).toHaveCount(0)
  })
}

test('recursive search finds files beneath folders and locates the selected result', async ({ page }) => {
  const fixture = await featureFixture(page, 'search')
  await fixture.mkdir(`${fixture.path}/nested`)
  await fixture.write(`${fixture.path}/nested/Report.TXT`, 'fixture report')
  await page.goto(`/?fixture=${encodeURIComponent(fixture.path)}#path=${encodeURIComponent(fixture.path)}`)
  await expect(page.getByRole('button', { name: fixture.path.split('/').pop()!, exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Show selection controls', exact: true }).click()
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('*.txt')
  await page.getByLabel('Wildcards (* and ?)').check()
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Search complete')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Search results for “*.txt”' })).toBeVisible()
  await page.getByRole('button', { name: 'List view', exact: true }).click()
  const row = page.locator('[data-file-entry]').filter({ hasText: 'Report.TXT' })
  await expect(row).toHaveCount(1)
  await expect(row).toContainText(`${fixture.path}/nested`)
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(row.locator('[data-result-location]')).toContainText(`${fixture.path}/nested`)
  await row.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Show in folder', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Search results for “*.txt”' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Unselect Report.TXT', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Open filter', exact: true }).click()
  await page.getByRole('textbox', { name: 'Filter this folder' }).fill('absent')
  await expect(page.getByText('No matching files')).toBeVisible()
})

test('search reports truncation and remains usable in a narrow viewport', async ({ page }) => {
  await page.route('**/api/fs/search', route => route.fulfill({
    contentType: 'application/x-ndjson', body: JSON.stringify({ scanned: 100000, skipped: 3, truncated: true, done: true }) + '\n',
  }))
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('report')
  const box = await page.getByRole('dialog').boundingBox()
  expect(box!.width).toBeLessThanOrEqual(390)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Partial results')
  await expect(page.getByRole('status')).toContainText('3 inaccessible or unavailable')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Back to folder', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Refine search', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Search filenames' })).toHaveValue('report')
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).last().click()
  await page.getByRole('button', { name: 'Back to folder', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Search results', exact: true })).toHaveCount(0)
})

test('renaming a nested search result preserves its folder and refreshes results', async ({ page }) => {
  const fixture = await featureFixture(page, 'search-rename')
  await fixture.mkdir(`${fixture.path}/nested`)
  const original = `${fixture.path}/nested/report.txt`
  const renamed = `${fixture.path}/nested/report-renamed.txt`
  await fixture.write(original, 'search rename content')
  await page.goto(`/?fixture=${encodeURIComponent(fixture.path)}#path=${encodeURIComponent(fixture.path)}`)
  await expect(page.getByRole('button', { name: fixture.path.split('/').pop()!, exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('report')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Search complete')
  await page.getByRole('button', { name: 'List view', exact: true }).click()
  await page.locator('[data-file-entry]').filter({ hasText: 'report.txt' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Rename…', exact: true }).click()
  await page.getByRole('dialog').getByRole('textbox').fill('report-renamed.txt')
  await page.getByRole('dialog').getByRole('button', { name: 'Rename', exact: true }).click()
  const row = page.locator('[data-file-entry]').filter({ hasText: 'report-renamed.txt' })
  await expect(row).toHaveCount(1)
  await expect(row.locator('[data-result-location]')).toHaveText(`${fixture.path}/nested`)
  expect(await fixture.read(renamed)).toBe('search rename content')
  expect(await fixture.exists(original)).toBe(false)
  expect(await fixture.exists(`${fixture.path}/report-renamed.txt`)).toBe(false)
})

test('search results distinguish duplicate names and retain normal sorting and selection', async ({ page }) => {
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  const entries = ['a', 'b'].map((folder, index) => ({ name: 'report.txt', path: `/Files/${folder}/report.txt`, is_dir: false, size: index + 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }))
  await page.route('**/api/fs/search', route => route.fulfill({ contentType: 'application/x-ndjson', body: [...entries.map(entry => ({ entry, scanned: 2, skipped: 0 })), { done: true, scanned: 2, skipped: 0 }].map(update => JSON.stringify(update)).join('\n') + '\n' }))
  await page.goto('/')
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('report')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Search complete')
  const rows = page.locator('[data-file-entry]')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0).locator('[data-result-location]')).toHaveText('/Files/a')
  await expect(rows.nth(1).locator('[data-result-location]')).toHaveText('/Files/b')
  await rows.nth(0).click()
  await rows.nth(1).click({ modifiers: ['Shift'] })
  await expect(page.locator('[data-selection-toolbar]')).toContainText('2 selected')
  await page.getByRole('button', { name: 'Sort by Size', exact: true }).click()
  await page.getByRole('button', { name: 'Sort by Size', exact: true }).click()
  await expect(rows.nth(0).locator('[data-result-location]')).toHaveText('/Files/b')
  await expect(page.locator('[data-selection-toolbar]')).toContainText('2 selected')
  await page.getByRole('button', { name: 'Open filter', exact: true }).click()
  await page.getByRole('textbox', { name: 'Filter search results' }).fill('absent')
  await expect(page.getByText('No matching files')).toBeVisible()
})

test('search can be cancelled in the main view and late responses cannot restore a closed search', async ({ page }) => {
  let release: () => void = () => {}
  const held = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/search', async route => {
    await held
    await route.fulfill({ contentType: 'application/x-ndjson', body: JSON.stringify({ done: true, scanned: 0, skipped: 0 }) + '\n' }).catch(() => {})
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('pending')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Cancel search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Search cancelled')
  await page.getByRole('button', { name: 'Back to folder', exact: true }).click()
  release()
  await expect(page.getByRole('region', { name: 'Search results', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toBeVisible()
})
