import { expect, test } from '@playwright/test'
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

test('recursive search finds files beneath folders and locates the selected result', async ({ page }) => {
  const review = readFileSync('/tmp/webfile-review-current', 'utf8').trim()
  const nested = join(review, 'files', 'search-fixture')
  mkdirSync(nested, { recursive: true })
  writeFileSync(join(nested, 'Report.TXT'), 'fixture report')
  mkdirSync(join(review, 'files', 'excluded'), { recursive: true })
  writeFileSync(join(review, 'files', 'excluded', 'Report.TXT'), 'excluded')
  await page.goto('/')
  await page.getByRole('button', { name: 'Show selection controls', exact: true }).click()
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  await page.getByRole('textbox', { name: 'Search filenames' }).fill('*.txt')
  await page.getByLabel('Wildcards (* and ?)').check()
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Search complete')
  const row = page.locator('[data-search-result]').filter({ hasText: 'Report.TXT' })
  await expect(row).toHaveCount(1)
  await expect(row).toContainText('/Files/search-fixture')
  await row.getByRole('button', { name: 'Show /Files/search-fixture/Report.TXT in folder' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
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
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('status')).toContainText('Partial results')
  await expect(page.getByRole('status')).toContainText('3 inaccessible or unavailable')
  await expect(page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).last()).toBeVisible()
  const box = await page.getByRole('dialog').boundingBox()
  expect(box!.width).toBeLessThanOrEqual(390)
})
