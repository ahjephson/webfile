import { expect, test, type Page, type Locator } from '@playwright/test'
import { featureFixture } from './feature-fixture'

async function largeFolder(page: Page, view: 'list' | 'grid') {
  let preferences: Record<string, unknown> = { view_mode: view, grid_size: 'medium' }
  await page.route('**/api/preferences', async route => {
    if (route.request().method() === 'PUT') preferences = route.request().postDataJSON()
    await route.fulfill({ json: preferences })
  })
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const folder = path === '/' ? '/Files' : path
    const entries = Array.from({ length: 300 }, (_, index) => ({
      name: `folder-${String(index).padStart(3, '0')}`,
      path: `${folder}/folder-${String(index).padStart(3, '0')}`,
      is_dir: true, can_navigate: true, size: 0,
      modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x',
    }))
    await route.fulfill({ json: { path, entries, single_root_path: '/Files' } })
  })
  await page.route('**/api/fs/details?**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/Files'
    await route.fulfill({ json: { path, name: path.split('/').pop(), is_dir: true, size: 0, mode: 'drwxr-xr-x', uid: 1000, gid: 1000, modified: '2000-01-01T00:00:00Z' } })
  })
  await page.goto('/')
  await expect(page.locator('nav[aria-label="Breadcrumb"]')).toHaveAttribute('data-single-root', 'true')
  if (view === 'grid') await expect(page.locator('[data-grid-size]')).toHaveAttribute('data-grid-size', 'medium')
  else await expect(page.getByLabel('File sorting', { exact: true })).toBeVisible()
  await expect(page.locator('[data-file-entry]').first()).toBeVisible()
}

async function visibleNames(viewport: Locator) {
  return viewport.evaluate(element => {
    const bounds = element.getBoundingClientRect()
    const visible = [...element.querySelectorAll<HTMLElement>('[data-file-entry]')]
      .filter(entry => { const box = entry.getBoundingClientRect(); return box.top >= bounds.top + 2 && box.bottom <= bounds.bottom })
    return visible.map(entry => entry.textContent?.match(/folder-\d{3}/)?.[0] ?? '')
  })
}

async function renderedVisibleNames(viewport: Locator) {
  // Setting scrollTop takes effect before the scroll event updates the virtual
  // rows. Wait for rendered targets rather than passing an undefined name.
  await expect.poll(async () => (await visibleNames(viewport)).length).toBeGreaterThan(0)
  return visibleNames(viewport)
}

for (const view of ['list', 'grid'] as const) {
  test(`${view} restores exact folder scroll positions backward and forward`, async ({ page }) => {
    await largeFolder(page, view)
    const viewport = page.locator('[data-file-viewport]')
    await viewport.evaluate(element => { element.scrollTop = 3307 })
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(3307)
    const names = await renderedVisibleNames(viewport)
    await page.locator('[data-file-entry]').filter({ hasText: names[0] }).dblclick()
    await expect(page.getByRole('button', { name: 'Parent directory', exact: true })).toBeVisible()
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(0)
    await viewport.evaluate(element => { element.scrollTop = 1309 })
    await page.goBack()
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(3307)
    await page.goForward()
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(1309)
  })
}

for (const layout of ['resize', 'split view', 'details pane'] as const) {
  test(`grid restores the visible item after ${layout} changes its width`, async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await largeFolder(page, 'grid')
    const pane = page.getByRole('region', { name: 'Left pane', exact: true })
    const viewport = pane.locator('[data-file-viewport]')
    await viewport.evaluate(element => { element.scrollTop = 3307 })
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(3307)
    const names = await renderedVisibleNames(viewport)
    const anchor = names[0]
    const originalWidth = await viewport.evaluate(element => element.clientWidth)
    await pane.locator('[data-file-entry]').filter({ hasText: anchor }).dblclick()
    await expect(page.getByRole('button', { name: 'Parent directory', exact: true })).toBeVisible()
    if (layout === 'resize') await page.setViewportSize({ width: 700, height: 650 })
    else await page.getByRole('button', { name: layout === 'split view' ? 'Split view' : 'Open details', exact: true }).click()
    await expect.poll(() => viewport.evaluate(element => element.clientWidth)).not.toBe(originalWidth)
    await page.goBack()
    await expect.poll(() => visibleNames(viewport)).toContain(anchor)
  })
}

test('split panes retain independent scroll positions during browser history navigation', async ({ page }) => {
  await largeFolder(page, 'list')
  await page.getByRole('button', { name: 'Split view', exact: true }).click()
  const left = page.getByRole('region', { name: 'Left pane', exact: true })
  const right = page.getByRole('region', { name: 'Right pane', exact: true })
  const leftViewport = left.locator('[data-file-viewport]')
  const rightViewport = right.locator('[data-file-viewport]')
  await leftViewport.evaluate(element => { element.scrollTop = 1107 })
  await rightViewport.evaluate(element => { element.scrollTop = 2209 })
  const names = await renderedVisibleNames(leftViewport)
  await left.locator('[data-file-entry]').filter({ hasText: names[0] }).dblclick()
  await expect(left.getByRole('button', { name: 'Parent directory', exact: true })).toBeVisible()
  await expect.poll(() => rightViewport.evaluate(element => element.scrollTop)).toBe(2209)
  await page.goBack()
  await expect.poll(() => leftViewport.evaluate(element => element.scrollTop)).toBe(1107)
  await expect.poll(() => rightViewport.evaluate(element => element.scrollTop)).toBe(2209)
})

test('Rename opens with its full target name selected and typing replaces it', async ({ page }) => {
  const fixture = await featureFixture(page, 'rename-selection')
  await fixture.write(`${fixture.path}/original.name.txt`, 'Rename selection fixture')
  await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
  await page.locator('[data-file-entry]').filter({ hasText: 'original.name.txt' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Rename…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Rename', exact: true })
  const input = dialog.getByRole('textbox')
  await expect(input).toBeFocused()
  await expect(input).toHaveValue('original.name.txt')
  await expect.poll(() => input.evaluate(element => {
    const input = element as HTMLInputElement
    return [input.selectionStart, input.selectionEnd]
  })).toEqual([0, 'original.name.txt'.length])
  await page.keyboard.press('Control+Shift+F')
  await expect(page.getByRole('dialog', { name: 'Search beneath this folder', exact: true })).toHaveCount(0)
  await page.keyboard.type('renamed.txt')
  await expect(input).toHaveValue('renamed.txt')
  await dialog.getByRole('button', { name: 'Rename', exact: true }).click()
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'renamed.txt' })).toBeVisible()
  expect(await fixture.read(`${fixture.path}/renamed.txt`)).toBe('Rename selection fixture')
})

test('Ctrl+Shift+F opens Search while Ctrl+F remains the folder filter', async ({ page }) => {
  await largeFolder(page, 'list')
  await page.keyboard.press('Control+F')
  const filter = page.getByRole('textbox', { name: 'Filter this folder', exact: true })
  await expect(filter).toBeFocused()
  await page.keyboard.press('Control+Shift+F')
  const search = page.getByRole('dialog', { name: 'Search beneath this folder', exact: true })
  await expect(search).toBeVisible()
  await search.locator('.p-dialog-footer').getByRole('button', { name: 'Close', exact: true }).click()
  await expect(search).toBeHidden()
  await page.getByRole('button', { name: 'Split view', exact: true }).click()
  const right = page.getByRole('region', { name: 'Right pane', exact: true })
  await right.locator('[data-file-entry]').filter({ hasText: 'folder-000' }).dblclick()
  await page.keyboard.press('Control+Shift+F')
  await expect(search).toBeVisible()
  await expect(search).toContainText('folder-000')
})

for (const action of ['Copy', 'Cut'] as const) {
  test(`${action} and Paste shows a success toast only after the transfer completes`, async ({ page }) => {
    const fixture = await featureFixture(page, 'paste-toast')
    await fixture.mkdir(`${fixture.path}/destination`)
    await fixture.write(`${fixture.path}/source.txt`, 'Clipboard completion fixture')
    await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
    await page.locator('[data-file-entry]').filter({ hasText: 'source.txt' }).click({ button: 'right' })
    await page.getByRole('menuitem', { name: action, exact: true }).click()
    const summary = `${action === 'Copy' ? 'Copy' : 'Move'} complete`
    const toast = page.locator('.p-toast').getByText(summary, { exact: true })
    await expect(toast).toHaveCount(0)
    await page.locator('[data-file-entry]').filter({ hasText: 'destination' }).dblclick()
    await expect(page.getByRole('button', { name: 'Actions', exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Actions', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Paste', exact: true }).click()
    await expect(toast).toBeVisible()
    await expect(toast).toHaveCount(1)
    expect(await fixture.read(`${fixture.path}/destination/source.txt`)).toBe('Clipboard completion fixture')
    expect(await fixture.exists(`${fixture.path}/source.txt`)).toBe(action === 'Copy')
  })
}
