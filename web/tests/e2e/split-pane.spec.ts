import { expect, test, type Page, type Locator } from '@playwright/test'
import { featureFixture } from './feature-fixture'

type StartedOperation = { kind: string; sources: string[]; destination?: string }
const entry = (path: string, directory = false) => ({
  name: path.split('/').pop()!, path, is_dir: directory, size: 100,
  modified: '2000-01-01T00:00:00Z', mode: directory ? 'drwxr-xr-x' : '-rw-r--r--',
})

async function workspace(page: Page, startSplit = true) {
  const started: StartedOperation[] = []
  const reads = new Map<string, number>()
  await page.addInitScript(() => {
    const sockets: unknown[] = []
    class MockSocket {
      static OPEN = 1
      static CONNECTING = 0
      readyState = 1
      path = ''
      onopen: null | (() => void) = null
      onmessage: null | ((event: { data: string }) => void) = null
      onclose: null | (() => void) = null
      constructor() { sockets.push(this); queueMicrotask(() => this.onopen?.()) }
      send(data: string) { const message = JSON.parse(data); if (message.type === 'subscribe-directory') this.path = message.path }
      close() { this.readyState = 3; this.onclose?.() }
    }
    Object.defineProperty(window, 'WebSocket', { configurable: true, value: MockSocket })
    Object.assign(window, { __splitSockets: sockets })
  })
  await page.route('**/api/preferences', route => route.fulfill({ json: route.request().method() === 'PUT'
    ? route.request().postDataJSON() : { view_mode: 'list', show_hidden: false } }))
  await page.route('**/api/fs/dir**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    reads.set(path, (reads.get(path) ?? 0) + 1)
    const entries = path === '/' ? [entry('/Files', true)]
      : path === '/Files' ? [entry('/Files/a', true), entry('/Files/b', true)]
      : [entry(`${path}/child`, true), entry(`${path}/first.txt`), entry(`${path}/second.txt`)]
    return route.fulfill({ json: { path, entries, single_root_path: '/Files' } })
  })
  await page.route('**/api/fs/operations', route => {
    if (route.request().method() !== 'POST') return route.fulfill({ json: { operations: [] } })
    const body = route.request().postDataJSON() as StartedOperation
    started.push(body)
    return route.fulfill({ json: { ...body, id: `task-${started.length}`, state: 'completed', phase: 'completed',
      created_at: new Date().toISOString(), updated_at: new Date().toISOString(), completed_items: body.sources.length, total_items: body.sources.length } })
  })
  await page.goto('/')
  await expect(page.locator('nav[aria-label="Breadcrumb"]')).toHaveAttribute('data-single-root', 'true')
  await expect(page.getByText('a', { exact: true })).toBeVisible()
  const left = page.locator('[data-pane="left"]')
  const right = page.locator('[data-pane="right"]')
  if (!startSplit) return { left, right, started, reads }
  await page.getByRole('button', { name: 'Split view', exact: true }).click()
  await left.getByText('a', { exact: true }).dblclick()
  await expect(left.getByRole('button', { name: 'a', exact: true })).toBeVisible()
  await right.getByText('b', { exact: true }).dblclick()
  await expect(left.getByText('first.txt', { exact: true })).toBeVisible()
  await expect(right.getByText('first.txt', { exact: true })).toBeVisible()
  return { left, right, started, reads }
}

async function transferToOtherPane(page: Page, pane: Locator, mode: 'Copy' | 'Move') {
  await pane.locator('[tabindex="0"]').last().focus()
  await page.getByRole('button', { name: 'Actions', exact: true }).click()
  await page.getByRole('menuitem', { name: `${mode} to other pane`, exact: true }).click()
}

test('opening split view shares an already loaded folder without refetching it', async ({ page }) => {
  const { left, right, reads } = await workspace(page, false)
  await left.getByText('a', { exact: true }).click()
  const before = [...reads.entries()]
  for (let index = 0; index < 3; index++) {
    await page.getByRole('button', { name: 'Split view', exact: true }).click()
    await expect(right.getByText('a', { exact: true })).toBeVisible()
    await expect(left).toHaveAttribute('data-pane-position', 'left')
    await expect(left.getByText('1 selected', { exact: true })).toBeVisible()
    expect([...reads.entries()]).toEqual(before)
    await left.locator('[tabindex="0"]').last().focus()
    await page.getByRole('button', { name: 'Close split view' }).click()
  }
})

test('reopening split view keeps the current view on the left without reloading or remounting it', async ({ page }) => {
  const { left, right, reads } = await workspace(page)
  await right.getByText('first.txt', { exact: true }).click()
  const viewport = await right.locator('[data-file-viewport]').elementHandle()
  await page.getByRole('button', { name: 'Close split view' }).click()
  const beforeB = reads.get('/Files/b')
  const beforeA = reads.get('/Files/a')!
  await page.getByRole('button', { name: 'Split view', exact: true }).click()
  await expect(right).toHaveAttribute('data-pane-position', 'left')
  await expect(left).toHaveAttribute('data-pane-position', 'right')
  await expect(left.getByText('first.txt', { exact: true })).toBeVisible()
  expect((await right.boundingBox())!.x).toBeLessThan((await left.boundingBox())!.x)
  await expect(right.getByText('1 selected', { exact: true })).toBeVisible()
  expect(await viewport!.evaluate(el => el.isConnected)).toBe(true)
  expect(reads.get('/Files/b')).toBe(beforeB)
  await expect.poll(() => reads.get('/Files/a')).toBeGreaterThan(beforeA)
  await page.reload()
  await expect(right.getByText('first.txt', { exact: true })).toBeVisible()
  await expect(right).toHaveAttribute('data-pane-position', 'left')
})

for (const view of ['list', 'grid'] as const) {
  for (const input of ['mouse', 'touch'] as const) {
    test(`full ${view} ${input} current breadcrumb selects the open folder without reloading`, async ({ page }) => {
      const { left } = await workspace(page)
      let reads = 0
      await page.route('**/api/fs/dir**', route => {
        const path = new URL(route.request().url()).searchParams.get('path')!
        reads++
        return route.fulfill({ json: { path, single_root_path: '/Files', entries: Array.from({ length: 100 }, (_, index) => entry(`${path}/filled-${index}.txt`)) } })
      })
      await left.getByRole('button', { name: `${view === 'list' ? 'List' : 'Grid'} view`, exact: true }).click()
      await left.getByRole('button', { name: 'Refresh', exact: true }).click()
      const item = left.getByText('filled-0.txt', { exact: true })
      await expect(item).toBeVisible()
      await left.getByRole('button', { name: 'Show selection controls', exact: true }).click()
      await item.click()
      await expect(left.getByText('1 selected', { exact: true })).toBeVisible()
      const before = reads
      const current = left.locator('nav [aria-current="page"]')
      if (input === 'touch') await current.tap()
      else await current.click()
      await expect(left).toHaveAttribute('data-current-folder-selected', 'true')
      await expect(left.getByText('1 selected', { exact: true })).toHaveCount(0)
      expect(reads).toBe(before)
      if (input === 'touch') await item.tap()
      else await item.click()
      await expect(left).not.toHaveAttribute('data-current-folder-selected', 'true')
      await expect(left.getByText('1 selected', { exact: true })).toBeVisible()
      await current.click()
      await expect(left).toHaveAttribute('data-current-folder-selected', 'true')
      await left.locator('nav').getByRole('button', { name: 'Files', exact: true }).click()
      await expect(left).not.toHaveAttribute('data-current-folder-selected', 'true')
    })
    for (const name of ['child', 'first.txt']) {
      test(`${view} ${input} background selects only the current folder and ${name} clears its border`, async ({ page }) => {
        await page.setViewportSize({ width: 1100, height: 1000 })
        const { left, right } = await workspace(page)
        await left.getByRole('button', { name: `${view === 'list' ? 'List' : 'Grid'} view`, exact: true }).click()
        await left.getByRole('button', { name: 'Show selection controls', exact: true }).click()
        await expect(left).not.toHaveAttribute('data-current-folder-selected', 'true')
        const viewport = left.locator('[data-file-viewport]')
        const box = (await viewport.boundingBox())!
        const background = { position: { x: box.width - 20, y: box.height - 20 } }
        if (input === 'touch') await viewport.tap(background)
        else await viewport.click(background)
        await expect(left).toHaveAttribute('data-current-folder-selected', 'true')
        expect(await viewport.evaluate(el => getComputedStyle(el).boxShadow)).not.toBe('none')
        expect(await left.evaluate(el => getComputedStyle(el, '::after').content)).toBe('none')
        await expect(left.getByText('1 selected', { exact: true })).toHaveCount(0)
        const item = left.getByText(name, { exact: true })
        if (input === 'touch') await item.tap()
        else await item.click()
        await expect(left).not.toHaveAttribute('data-current-folder-selected', 'true')
        expect(await viewport.evaluate(el => getComputedStyle(el).boxShadow)).toBe('none')
        await expect(left.getByText('1 selected', { exact: true })).toBeVisible()
        await viewport.click(background)
        await right.locator('[tabindex="0"]').last().focus()
        await expect(left).toHaveAttribute('data-current-folder-selected', 'true')
        await expect(right).not.toHaveAttribute('data-current-folder-selected', 'true')
        await left.locator('nav').getByRole('button', { name: 'Files', exact: true }).click()
        await expect(left).not.toHaveAttribute('data-current-folder-selected', 'true')
      })
    }
  }
}

test('panes navigate, select, filter and restore independently with one shared toolbar', async ({ page }) => {
  const { left, right } = await workspace(page)
  await left.getByText('first.txt', { exact: true }).click()
  await right.getByText('second.txt', { exact: true }).click()
  await expect(left).toContainText('1 selected')
  await expect(right).toContainText('1 selected')
  await page.keyboard.press('Control+f')
  await expect(right.getByRole('textbox')).toBeVisible()
  await expect(left.getByRole('textbox')).toHaveCount(0)
  await right.getByRole('textbox').fill('second')
  await expect(right.getByText('first.txt', { exact: true })).toHaveCount(0)
  await expect(left.getByText('first.txt', { exact: true })).toBeVisible()
  await right.getByRole('textbox').press('Escape')
  await right.getByText('child', { exact: true }).dblclick()
  await expect(page).toHaveURL(/rightPath=%2Fb%2Fchild/)
  await page.goBack()
  await expect(right.getByRole('button', { name: 'child', exact: true })).toHaveCount(0)
  await expect(left.getByRole('button', { name: 'a', exact: true })).toBeVisible()
  await page.reload()
  await expect(left.getByRole('button', { name: 'a', exact: true })).toBeVisible()
  await expect(right.getByRole('button', { name: 'b', exact: true })).toBeVisible()
  await expect(left).toContainText('1 selected')
  await expect(page.getByRole('button', { name: 'New folder', exact: true })).toHaveCount(1)
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toHaveCount(1)
})

test('pane toolbars keep selection fixed at the left and browsing controls at the right at every width', async ({ page }) => {
  const { left, right } = await workspace(page)
  await left.getByText('first.txt', { exact: true }).click()
  await expect(page.getByRole('button', { name: 'Activate left pane' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Copy to right pane' })).toHaveCount(0)
  for (const width of [1440, 900, 390, 320]) {
    await page.setViewportSize({ width, height: 900 })
    for (const pane of [left, right]) {
      await expect.poll(async () => {
        const paneBox = (await pane.boundingBox())!, controls = (await pane.locator('[data-pane-view-controls]').boundingBox())!
        return Math.abs(paneBox.x + paneBox.width - controls.x - controls.width - 12)
      }).toBeLessThan(2)
      await expect.poll(async () => {
        const paneBox = (await pane.boundingBox())!, toggle = (await pane.locator('[data-selection-toolbar] button').first().boundingBox())!
        return Math.abs(toggle.x - paneBox.x - 12)
      }).toBeLessThan(2)
      const toolbar = pane.locator('.webfile-pane-toolbar')
      expect(await toolbar.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1)
    }
  }
  await page.setViewportSize({ width: 390, height: 900 })
  expect((await page.locator('#webfile-workspace-toolbar header').boundingBox())!.height).toBeLessThan(60)
  await right.getByRole('button', { name: 'More folder actions' }).click()
  await page.getByRole('menuitem', { name: 'Filter this folder', exact: true }).click()
  await right.getByRole('textbox').fill('second')
  await expect(right.getByText('first.txt', { exact: true })).toHaveCount(0)
  await right.getByRole('textbox').press('Escape')
  await expect(right.getByText('first.txt', { exact: true })).toBeVisible()
  await expect(right).toHaveAttribute('data-active', 'true')
  await page.getByRole('button', { name: 'Close split view' }).click()
  const toggle = right.locator('[data-selection-toolbar] button').first()
  const before = (await toggle.boundingBox())!.x
  await right.getByText('first.txt', { exact: true }).click()
  await expect.poll(async () => (await toggle.boundingBox())!.x).toBe(before)
  await expect(right.locator('.webfile-selection-count-label')).toBeHidden()
  await right.getByRole('button', { name: 'More folder actions' }).click()
  await page.getByRole('menuitem', { name: 'Filter this folder', exact: true }).click()
  await expect(right.getByRole('textbox')).toBeVisible()
})

for (const mode of ['Copy', 'Cut'] as const) {
  test(`touch ${mode.toLowerCase()} and Paste targets the open folder without selecting a destination item`, async ({ page }) => {
    const { left, right, started } = await workspace(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await left.getByRole('button', { name: 'Show selection controls', exact: true }).tap()
    await left.getByText('first.txt', { exact: true }).tap()
    await page.getByRole('button', { name: 'Actions', exact: true }).tap()
    await page.getByRole('menuitem', { name: mode, exact: true }).tap()
    await right.locator('nav [aria-current="page"]').tap()
    await expect(right).toHaveAttribute('data-active', 'true')
    await expect(right.getByText('1 selected', { exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: 'Actions', exact: true }).tap()
    await page.getByRole('menuitem', { name: 'Paste', exact: true }).tap()
    await expect.poll(() => started.length).toBe(1)
    expect(started[0]).toMatchObject({ kind: mode === 'Copy' ? 'copy' : 'move', sources: ['/Files/a/first.txt'], destination: '/Files/b' })
  })
}

test('copy/move actions and the clipboard use the other pane and one task list', async ({ page }) => {
  const { left, right, started } = await workspace(page)
  await left.getByText('first.txt', { exact: true }).click()
  await transferToOtherPane(page, left, 'Copy')
  await expect.poll(() => started.length).toBe(1)
  expect(started[0]).toMatchObject({ kind: 'copy', sources: ['/Files/a/first.txt'], destination: '/Files/b' })
  await right.getByText('second.txt', { exact: true }).click()
  await right.getByText('second.txt', { exact: true }).click({ button: 'right' })
  await page.locator('.p-contextmenu').getByRole('menuitem', { name: 'Move to other pane', exact: true }).click()
  await expect.poll(() => started.length).toBe(2)
  expect(started[1]).toMatchObject({ kind: 'move', sources: ['/Files/b/second.txt'], destination: '/Files/a' })
  await left.getByText('first.txt', { exact: true }).click()
  await left.locator('[tabindex="0"]').last().press('Control+x')
  await right.getByText('second.txt', { exact: true }).click()
  await right.locator('[tabindex="0"]').last().press('Control+v')
  await expect.poll(() => started.length).toBe(3)
  expect(started[2]).toMatchObject({ kind: 'move', sources: ['/Files/a/first.txt'], destination: '/Files/b' })
  await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
  await expect(page.getByRole('complementary')).toHaveCount(1)
})

async function drag(page: Page, source: Locator, target: Locator, ctrlKey = false, shiftKey = false) {
  const dataTransfer = await page.evaluateHandle(() => new DataTransfer())
  await source.dispatchEvent('dragstart', { dataTransfer })
  await target.dispatchEvent('dragover', { dataTransfer, ctrlKey: !ctrlKey, shiftKey: false })
  await target.dispatchEvent('dragover', { dataTransfer, ctrlKey, shiftKey })
  await target.dispatchEvent('drop', { dataTransfer, ctrlKey, shiftKey })
  await source.dispatchEvent('dragend', { dataTransfer })
}

for (const view of ['list', 'grid'] as const) {
  test(`${view} highlights the hovered folder and uses the pane only for background drops`, async ({ page }) => {
    const { left, right, started } = await workspace(page)
    await left.getByRole('button', { name: `${view === 'grid' ? 'Grid' : 'List'} view`, exact: true }).click()
    await right.getByRole('button', { name: `${view === 'grid' ? 'Grid' : 'List'} view`, exact: true }).click()
    const source = left.locator('[data-file-entry]').filter({ hasText: 'first.txt' })
    const folder = right.locator('[data-file-entry]').filter({ hasText: 'child' })
    const file = right.locator('[data-file-entry]').filter({ hasText: 'second.txt' })
    const background = right.locator('[data-file-viewport]')
    const dataTransfer = await page.evaluateHandle(() => new DataTransfer())
    await source.dispatchEvent('dragstart', { dataTransfer })
    await folder.getByText('child', { exact: true }).dispatchEvent('dragover', { dataTransfer, ctrlKey: true })
    await expect(folder).toHaveAttribute('data-drop-target', 'true')
    await expect(folder).toHaveClass(/webfile-drop-target/)
    await expect(right.locator('[data-pane-drop-target]')).toHaveCount(0)
    await background.dispatchEvent('dragover', { dataTransfer, ctrlKey: true })
    await expect(folder).not.toHaveAttribute('data-drop-target')
    await expect(right.locator('[data-pane-drop-target]')).toContainText('Copy to /b')
    await file.dispatchEvent('dragover', { dataTransfer, ctrlKey: true })
    await expect(file).not.toHaveAttribute('data-drop-target')
    await expect(right.locator('[data-pane-drop-target]')).toBeVisible()
    await folder.dispatchEvent('dragover', { dataTransfer })
    await folder.dispatchEvent('dragleave', { dataTransfer, relatedTarget: null })
    await expect(folder).not.toHaveAttribute('data-drop-target')
    await expect(right.locator('[data-pane-drop-target]')).toHaveCount(0)
    await folder.dispatchEvent('dragover', { dataTransfer })
    await folder.dispatchEvent('drop', { dataTransfer })
    await source.dispatchEvent('dragend', { dataTransfer })
    await expect.poll(() => started.length).toBe(1)
    expect(started[0]).toMatchObject({ kind: 'move', destination: '/Files/b/child' })
    await expect(folder).not.toHaveAttribute('data-drop-target')
  })

  test(`${view} highlights folders for external file uploads too`, async ({ page }) => {
    const { right } = await workspace(page)
    await right.getByRole('button', { name: `${view === 'grid' ? 'Grid' : 'List'} view`, exact: true }).click()
    const folder = right.locator('[data-file-entry]').filter({ hasText: 'child' })
    const dataTransfer = await page.evaluateHandle(() => {
      const data = new DataTransfer()
      data.items.add(new File(['test'], 'upload.txt', { type: 'text/plain' }))
      return data
    })
    await folder.dispatchEvent('dragover', { dataTransfer })
    await expect(folder).toHaveAttribute('data-drop-target', 'true')
    await expect(right.locator('[data-pane-drop-target]')).toHaveCount(0)
    await right.locator('[data-file-viewport]').dispatchEvent('dragover', { dataTransfer })
    await expect(folder).not.toHaveAttribute('data-drop-target')
    await expect(right.locator('[data-pane-drop-target]')).toHaveText('Drop files to upload')
  })

  for (const modifier of ['plain', 'control', 'shift'] as const) {
    test(`${view} drag uses ${modifier} modifiers at drop time`, async ({ page }) => {
      const { left, right, started } = await workspace(page)
      await left.getByRole('button', { name: `${view === 'grid' ? 'Grid' : 'List'} view`, exact: true }).click()
      await right.getByRole('button', { name: `${view === 'grid' ? 'Grid' : 'List'} view`, exact: true }).click()
      const source = left.locator('[data-file-entry]').filter({ hasText: 'first.txt' })
      const target = right.locator('[data-file-entry]').filter({ hasText: 'child' })
      await drag(page, source, target, modifier === 'control', modifier === 'shift')
      await expect.poll(() => started.length).toBe(1)
      expect(started[0]).toMatchObject({ kind: modifier === 'control' ? 'copy' : 'move', sources: ['/Files/a/first.txt'], destination: '/Files/b/child' })
    })
  }
}

test('invalid directory drops and forged external payloads do not start operations', async ({ page }) => {
  const { left, started } = await workspace(page)
  const folder = left.locator('[data-file-entry]').filter({ hasText: 'child' })
  await drag(page, folder, folder)
  expect(started).toHaveLength(0)
  const dataTransfer = await page.evaluateHandle(() => { const data = new DataTransfer(); data.setData('application/x-webfile-items', '/Files/a/child'); return data })
  await left.locator('[data-file-viewport]').dispatchEvent('drop', { dataTransfer })
  expect(started).toHaveLength(0)
})

test('closing split view keeps the active location and repeated toggles retain pane state', async ({ page }) => {
  const { left, right } = await workspace(page)
  await right.locator('nav [aria-current="page"]').click()
  for (let index = 0; index < 3; index++) {
    await page.getByRole('button', { name: 'Close split view' }).click()
    await expect(left).toBeHidden()
    await expect(right.getByRole('button', { name: 'b', exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Split view', exact: true }).click()
    await expect(left.getByRole('button', { name: 'a', exact: true })).toBeVisible()
  }
  const divider = page.getByRole('separator', { name: 'Resize panes' })
  await divider.press('ArrowRight')
  await expect(divider).toHaveAttribute('aria-valuenow', '55')
  await divider.press('Home')
  await expect(divider).toHaveAttribute('aria-valuenow', '20')
  await divider.press('End')
  await expect(divider).toHaveAttribute('aria-valuenow', '80')
})

test('external changes refresh each pane independently and hidden panes release their watch', async ({ page }) => {
  const { left, right, reads } = await workspace(page)
  const beforeA = reads.get('/Files/a') ?? 0
  const beforeB = reads.get('/Files/b') ?? 0
  await page.evaluate(() => {
    const sockets = (window as unknown as { __splitSockets: Array<{ readyState: number; path: string; onmessage: (event: { data: string }) => void }> }).__splitSockets
    for (const socket of sockets.filter(item => item.readyState === 1 && item.path)) socket.onmessage({ data: JSON.stringify({ type: 'directory-invalidated', path: socket.path }) })
  })
  await expect.poll(() => reads.get('/Files/a') ?? 0).toBeGreaterThan(beforeA)
  await expect.poll(() => reads.get('/Files/b') ?? 0).toBeGreaterThan(beforeB)
  await right.locator('nav [aria-current="page"]').click()
  await page.getByRole('button', { name: 'Close split view' }).click()
  await expect(left).toBeHidden()
  await expect.poll(() => page.evaluate(() => (window as unknown as { __splitSockets: Array<{ readyState: number; path: string }> }).__splitSockets.filter(socket => socket.readyState === 1 && socket.path).length)).toBe(1)
})

test('only the active preview handles Escape and the other pane keeps its location', async ({ page }) => {
  const { left, right } = await workspace(page)
  // A deliberately unsupported MIME type avoids loading an editor or decoder.
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'application/pdf' } }))
  await left.getByText('first.txt', { exact: true }).dblclick()
  await right.getByText('second.txt', { exact: true }).dblclick()
  await expect(left.getByLabel('File preview', { exact: true })).toBeVisible()
  await expect(right.getByLabel('File preview', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(right.getByLabel('File preview', { exact: true })).toHaveCount(0)
  await expect(left.getByLabel('File preview', { exact: true })).toBeVisible()
  await expect(right.getByRole('button', { name: 'b', exact: true })).toBeVisible()
})

test('compact panes fit a narrow viewport and the divider resizes by pointer', async ({ page }) => {
  const { left, right } = await workspace(page)
  const divider = page.getByRole('separator', { name: 'Resize panes' })
  const box = (await divider.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + 20)
  await page.mouse.down()
  await page.mouse.move(box.x + 80, box.y + 20)
  await page.mouse.up()
  await expect.poll(async () => Number(await divider.getAttribute('aria-valuenow'))).toBeGreaterThan(50)
  await page.setViewportSize({ width: 390, height: 844 })
  const first = (await left.boundingBox())!, second = (await right.boundingBox())!
  expect(second.y).toBeGreaterThan(first.y + first.height)
  expect(first.width).toBeLessThanOrEqual(391)
  expect(second.width).toBeLessThanOrEqual(391)
})

test.describe('real filesystem transfers', () => {
  test.use({ isMobile: false, hasTouch: false, viewport: { width: 1280, height: 900 } })
  test('native Ctrl-drag copies and a pane action moves, refreshing both folders', async ({ page }) => {
    await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
    const fixture = await featureFixture(page, 'split-pane')
    await fixture.mkdir(`${fixture.path}/a`)
    await fixture.mkdir(`${fixture.path}/b`)
    await fixture.write(`${fixture.path}/a/copy.txt`, 'copied through split view')
    await fixture.write(`${fixture.path}/a/move.txt`, 'moved through split view')
    await page.goto(`/#path=${encodeURIComponent(`${fixture.path}/a`)}`)
    await expect(page.getByText('copy.txt', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Split view', exact: true }).click()
    const left = page.locator('[data-pane="left"]'), right = page.locator('[data-pane="right"]')
    await right.getByRole('button', { name: 'Parent directory' }).click()
    await right.getByText('b', { exact: true }).dblclick()
    await expect(right.getByRole('button', { name: 'b', exact: true })).toBeVisible()
    await page.keyboard.down('Control')
    try {
      await left.locator('[data-file-entry]').filter({ hasText: 'copy.txt' }).dragTo(right.locator('[data-file-viewport]'), { targetPosition: { x: 120, y: 120 } })
    } finally { await page.keyboard.up('Control') }
    await expect.poll(() => fixture.exists(`${fixture.path}/b/copy.txt`)).toBe(true)
    expect(await fixture.read(`${fixture.path}/a/copy.txt`)).toBe('copied through split view')
    expect(await fixture.read(`${fixture.path}/b/copy.txt`)).toBe('copied through split view')
    await expect(right.getByText('copy.txt', { exact: true })).toBeVisible()
    await left.getByText('move.txt', { exact: true }).click()
    await transferToOtherPane(page, left, 'Move')
    await expect(right.getByText('move.txt', { exact: true })).toBeVisible()
    await expect(left.getByText('move.txt', { exact: true })).toHaveCount(0)
    expect(await fixture.exists(`${fixture.path}/a/move.txt`)).toBe(false)
    expect(await fixture.read(`${fixture.path}/b/move.txt`)).toBe('moved through split view')
    await right.locator('nav [aria-current="page"]').click()
    await page.getByRole('button', { name: 'Close split view' }).click()
    await fixture.start('copy', [`${fixture.path}/a/copy.txt`], `${fixture.path}/b`)
    const conflict = page.getByRole('dialog', { name: 'Resolve transfer conflict' })
    await expect(conflict).toHaveCount(1)
    await expect(conflict).toBeVisible()
    await conflict.getByRole('button', { name: 'Skip', exact: true }).click()
    await expect(conflict).toHaveCount(0)
    await page.getByRole('button', { name: 'Open tasks', exact: true }).click()
    await expect(page.getByRole('complementary')).toBeVisible()
  })
})

test('closing the inactive pane asks before discarding unsaved editor changes', async ({ page }) => {
  const { left, right } = await workspace(page)
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'text/plain' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({ json: {
    path: '/Files/a/first.txt', content: 'Original', modified: '2000-01-01T00:00:00Z', etag: 'original',
  } }))
  await left.getByText('first.txt', { exact: true }).dblclick()
  const editor = left.getByRole('textbox', { name: 'Editor content' })
  await editor.focus()
  await page.keyboard.type(' unsaved')
  await right.locator('nav [aria-current="page"]').click()
  page.once('dialog', dialog => dialog.dismiss())
  await page.getByRole('button', { name: 'Close split view' }).click()
  await expect(editor).toBeVisible()
  await expect(page.getByRole('button', { name: 'Close split view' })).toBeVisible()
  page.once('dialog', dialog => dialog.accept())
  await page.getByRole('button', { name: 'Close split view' }).click()
  await expect(left).toBeHidden()
  await page.getByRole('button', { name: 'Split view', exact: true }).click()
  await expect(left.getByLabel('Text editor', { exact: true })).toHaveCount(0)
})
