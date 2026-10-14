import { expect, test } from '@playwright/test'
import { sortEntries } from '../../src/utils/sorting'
import type { FileEntry } from '../../src/types/files'

const entry = (name: string, size: number, is_dir = false, size_status?: 'ready'): FileEntry => ({
  name, size, is_dir, path: `/Files/${name}`, mode: '-rw-r--r--', modified: '2000-01-01T00:00:00Z', size_status,
})

test('sorting retains folder groups, host name ties and unknown sizes', () => {
  const entries = [entry('cached', 5, true, 'ready'), entry('unknown', 0, true), entry('a.txt', 30), entry('b.csv', 10)]
  expect(sortEntries(entries, 'size', 'desc').map(e => e.name)).toEqual(['cached', 'unknown', 'a.txt', 'b.csv'])
  expect(sortEntries(entries, 'size', 'asc').map(e => e.name)).toEqual(['cached', 'unknown', 'b.csv', 'a.txt'])
  expect(sortEntries(entries, 'name', 'desc').map(e => e.name)).toEqual(['unknown', 'cached', 'b.csv', 'a.txt'])
  expect(sortEntries(entries, 'type', 'asc').map(e => e.name)).toEqual(['cached', 'unknown', 'b.csv', 'a.txt'])
  expect(sortEntries(entries, 'modified', 'desc').map(e => e.name)).toEqual(entries.map(e => e.name))
  expect(entries.map(e => e.name)).toEqual(['cached', 'unknown', 'a.txt', 'b.csv'])
})

test('sort changes preserve focus and selection in list and grid and persist after reload', async ({ page }) => {
  const entries = [entry('folder', 0, true), entry('a.txt', 30), entry('b.csv', 10)]
  let preferences: Record<string, unknown> = {}
  await page.route('**/api/preferences', async route => {
    if (route.request().method() === 'PUT') preferences = route.request().postDataJSON()
    await route.fulfill({ json: preferences })
  })
  await page.route('**/api/fs/dir?*', route => route.fulfill({ json: { path: '/Files', entries, single_root_path: '/Files' } }))
  await page.goto('/')
  await expect(page.locator('[data-file-entry]')).toHaveCount(3)
  await page.getByRole('button', { name: 'Show selection controls' }).click()
  await page.getByRole('button', { name: 'Select a.txt', exact: true }).click()
  await page.getByRole('button', { name: 'Sort by Size', exact: true }).click()
  await expect(page.locator('[data-file-entry]').nth(1)).toContainText('b.csv')
  await expect(page.getByRole('button', { name: 'Unselect a.txt', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(page.locator('[data-file-entry]').nth(1)).toContainText('b.csv')
  await expect.poll(() => preferences.sort_field).toBe('size')
  await page.reload()
  await expect(page.locator('[data-file-entry]').nth(1)).toContainText('b.csv')
  await page.getByRole('button', { name: 'Sort files' }).click()
  await page.getByRole('menuitem', { name: 'Descending', exact: true }).click()
  await expect(page.locator('[data-file-entry]').nth(1)).toContainText('a.txt')
})
