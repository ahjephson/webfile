import { expect, test, type Locator, type Page, type WebSocketRoute } from '@playwright/test'

async function touchPoint(page: Page, target: Locator) {
  const box = await target.boundingBox()
  if (!box) {
    throw new Error('Touch target is not visible')
  }

  await page.touchscreen.tap(box.x + box.width / 2, box.y + box.height / 2)
}

async function longPressTouch(page: Page, target: Locator, moveX = 0, whileHeld?: () => Promise<void>) {
  const box = await target.boundingBox()
  if (!box) {
    throw new Error('Touch target is not visible')
  }

  const x = box.x + box.width / 2
  const y = box.y + box.height / 2
  const session = await page.context().newCDPSession(page)
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x, y, radiusX: 2, radiusY: 2, force: 1 }],
  })
  if (moveX !== 0) {
    await page.waitForTimeout(200)
    await session.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x: x + moveX, y, radiusX: 2, radiusY: 2, force: 1 }],
    })
  }
  await page.waitForTimeout(moveX === 0 ? 550 : 400)
  await whileHeld?.()
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await session.detach()
}

async function longPressWithEmulatedMouse(page: Page, target: Locator, whileHeld?: () => Promise<void>) {
  const box = await target.boundingBox()
  if (!box) {
    throw new Error('Touch target is not visible')
  }

  const session = await page.context().newCDPSession(page)
  const x = Math.round(box.x + box.width / 2)
  const y = Math.round(box.y + box.height / 2)
  await session.send('Input.emulateTouchFromMouseEvent', {
    type: 'mousePressed',
    x,
    y,
    button: 'left',
    clickCount: 1,
  })
  await page.waitForTimeout(550)
  await whileHeld?.()
  await session.send('Input.emulateTouchFromMouseEvent', {
    type: 'mouseReleased',
    x,
    y,
    button: 'left',
    clickCount: 1,
  })
  await session.detach()
}

async function dispatchContextMenu(target: Locator) {
  await target.click({ button: 'right' })
}

async function dispatchContextMenuProbe(target: Locator) {
  return target.evaluate(element => element.dispatchEvent(new MouseEvent('contextmenu', {
    bubbles: true,
    cancelable: true,
    button: 2,
  })))
}

async function openConfiguredRoot(page: Page) {
  await page.goto('/')
  await expect(page.locator('nav[aria-label="Breadcrumb"]')).toHaveAttribute('data-single-root', 'true')
  const rootRow = page.locator('[data-file-entry]').first()
  await expect(rootRow).toBeVisible()
}

async function chooseView(page: Page, view: 'list' | 'grid') {
  await page.getByRole('button', { name: view === 'list' ? 'List view' : 'Grid view' }).click()
}

function selectionButtons(page: Page) {
  return page.locator('[data-file-entry] [data-selection-control]')
}

function entryFor(selectionButton: Locator) {
  return selectionButton.locator('xpath=ancestor::*[@data-file-entry][1]')
}

async function showSelectionControls(page: Page) {
  await page.getByRole('button', { name: 'Show selection controls' }).click()
  await expect(page.getByRole('button', { name: 'Hide selection controls' })).toHaveAttribute('aria-pressed', 'true')
}

test('inaccessible entries show a lock indicator in list and grid views', async ({ page }) => {
  let preferences: Record<string, unknown> = { hide_inaccessible: false }
  await page.route('**/api/preferences', async route => {
    if (route.request().method() === 'PUT') {
      preferences = route.request().postDataJSON() as Record<string, unknown>
    }
    await route.fulfill({ json: preferences })
  })

  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const entries = path === '/'
      ? [{
          name: 'Files',
          path: '/Files',
          is_dir: true,
          can_navigate: true,
          size: 0,
          modified: '2000-01-01T00:00:00Z',
          mode: 'drwxr-xr-x',
        }]
      : [{
          name: 'restricted',
          path: '/Files/restricted',
          is_dir: true,
          can_navigate: false,
          size: 0,
          modified: '2000-01-01T00:00:00Z',
          mode: 'd---------',
        }, {
          name: 'DumpStack.log',
          path: '/Files/DumpStack.log',
          is_dir: false,
          can_read: false,
          size: 4096,
          modified: '2000-01-01T00:00:00Z',
          mode: '----------',
        }]
    await route.fulfill({ json: { path, entries } })
  })

  await page.goto('/')
  const rootRow = page.locator('[data-file-entry]').filter({ hasText: 'Files' })
  await expect(rootRow).toBeVisible()
  await rootRow.dblclick()
  const restricted = page.locator('[data-file-entry]').filter({ hasText: 'restricted' })
  await expect(restricted).toBeVisible()
  const restrictedFile = page.locator('[data-file-entry]').filter({ hasText: 'DumpStack.log' })

  for (const view of ['grid', 'list'] as const) {
    await chooseView(page, view)
    await expect(restricted.getByRole('img', { name: 'No permission to open restricted' })).toBeVisible()
    await expect(restricted.locator('[data-directory-lock]')).toBeVisible()

    await expect(restrictedFile.getByRole('img', { name: 'No permission to read DumpStack.log' })).toBeVisible()
    await expect(restrictedFile.locator('[data-file-lock]')).toBeVisible()
    await restrictedFile.dblclick()
    await expect(page).not.toHaveURL(/file=/)
  }

  await page.getByRole('button', { name: 'Settings' }).click()
  const settings = page.getByRole('dialog', { name: 'Settings' })
  await settings.getByRole('switch', { name: 'Hide inaccessible files and folders' }).click()
  await settings.getByRole('button', { name: 'Save settings' }).click()
  await expect(restricted).toBeHidden()
  await expect(restrictedFile).toBeHidden()
  await expect.poll(() => preferences.hide_inaccessible).toBe(true)
})

for (const view of ['list', 'grid'] as const) {
  test.describe(`${view} file interactions`, () => {
    test.beforeEach(async ({ page }) => {
      await openConfiguredRoot(page)
      await chooseView(page, view)
    })

    test('touch tap on the checkbox only toggles selection', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await showSelectionControls(page)
      await touchPoint(page, checkbox)

      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await expect(entryFor(checkbox)).toHaveClass(/outline-sky-500/)
      await expect(entryFor(checkbox)).toHaveClass(/webfile-selection-tint/)
      await expect(entryFor(checkbox)).not.toHaveClass(/webfile-focus-tint/)
      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore)
    })

    test('touch tap on the whole tile toggles selection while selection controls are enabled', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const entry = entryFor(checkbox)
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await showSelectionControls(page)
      await touchPoint(page, entry)

      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await expect(entry).toHaveClass(/webfile-selection-tint/)
      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore)
    })

    test('clicking empty file-pane space clears selection and item focus', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const entry = entryFor(checkbox)

      await showSelectionControls(page)
      await checkbox.click()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await expect(entry).toHaveClass(/webfile-selection-tint/)

      await page.locator('[data-file-viewport]').dispatchEvent('click')

      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
      await expect(page.getByText('0 selected', { exact: true })).toBeVisible()
      await expect(entry).not.toHaveClass(/webfile-selection-tint/)
      await expect(entry).not.toHaveClass(/outline-sky-500/)
    })

    test('selection controls are opt-in with a touch-sized target', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const toggle = page.getByRole('button', { name: 'Show selection controls' })
      const toggleBefore = await toggle.boundingBox()

      await expect(checkbox).toBeHidden()
      await showSelectionControls(page)
      await expect(checkbox).toBeVisible()

      const toggleAfter = await page.getByRole('button', { name: 'Hide selection controls' }).boundingBox()
      expect(toggleAfter?.x).toBe(toggleBefore?.x)
      const box = await checkbox.boundingBox()
      expect(box?.width).toBeGreaterThanOrEqual(40)
      expect(box?.height).toBeGreaterThanOrEqual(40)
      await expect(page.getByText('0 selected', { exact: true })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Clear selection' })).toBeDisabled()
    })

    test('light theme leaves unchecked selection boxes unfilled', async ({ page }) => {
      await page.route('**/api/preferences', route => route.fulfill({
        json: route.request().postDataJSON(),
      }))
      await page.getByRole('button', { name: 'Choose theme' }).click()
      await page.getByRole('menuitem', { name: 'Light' }).click()
      await showSelectionControls(page)

      const checkbox = selectionButtons(page).first()
      const box = checkbox.locator('.webfile-selection-box')
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
      await expect(box).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')

      await checkbox.click()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await expect(box).toHaveCSS('background-color', 'rgb(2, 132, 199)')
      await expect(box).toHaveCSS('color', 'rgb(255, 255, 255)')
      await expect(entryFor(checkbox)).toHaveCSS('background-color', 'rgba(224, 242, 254, 0.82)')

      const activeView = page.getByRole('button', { name: view === 'list' ? 'List view' : 'Grid view' })
      await expect(activeView).toHaveCSS('background-color', 'rgb(224, 242, 254)')
      await expect(activeView).toHaveCSS('color', 'rgb(7, 89, 133)')
    })

    test('control-clicking the only selected item removes its selection background', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const entry = entryFor(checkbox)

      await showSelectionControls(page)
      await entry.click()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await expect(entry).toHaveClass(/webfile-focus-tint/)

      await entry.click({ modifiers: ['Control'] })

      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(0)
      await expect(page.getByText('0 selected', { exact: true })).toBeVisible()
      await expect(entry).not.toHaveClass(/webfile-selection-tint|webfile-focus-tint/)
      await expect(entry).toHaveClass(/outline-sky-500/)
    })

    test('mouse click modifiers follow desktop multi-selection rules', async ({ page }) => {
      const checkboxes = selectionButtons(page)
      const first = checkboxes.nth(0)
      const second = checkboxes.nth(1)
      const third = checkboxes.nth(2)
      const fourth = checkboxes.nth(3)

      await entryFor(first).click()

      await expect(first).toHaveAttribute('aria-pressed', 'true')
      await expect(first).toBeHidden()
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(1)
      await expect(entryFor(first)).toHaveClass(/outline-sky-500/)
      await expect(entryFor(first)).toHaveClass(/webfile-focus-tint/)
      await expect(entryFor(first)).not.toHaveClass(/ring-sky-600|border-sky-600/)

      await entryFor(third).click({ modifiers: ['Control'] })

      await expect(first).toHaveAttribute('aria-pressed', 'true')
      await expect(third).toHaveAttribute('aria-pressed', 'true')
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(2)
      await expect(entryFor(first)).toHaveClass(/webfile-selection-tint/)
      await expect(entryFor(third)).toHaveClass(/webfile-focus-tint/)
      await expect(entryFor(third)).toHaveClass(/outline-sky-500/)

      await entryFor(first).click({ modifiers: ['Control'] })

      await expect(first).toHaveAttribute('aria-pressed', 'false')
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(1)

      await entryFor(second).click()
      await entryFor(fourth).click({ modifiers: ['Shift'] })

      await expect(first).toHaveAttribute('aria-pressed', 'false')
      await expect(second).toHaveAttribute('aria-pressed', 'true')
      await expect(third).toHaveAttribute('aria-pressed', 'true')
      await expect(fourth).toHaveAttribute('aria-pressed', 'true')
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(3)

      await entryFor(first).click({ modifiers: ['Control', 'Shift'] })

      await expect(first).toHaveAttribute('aria-pressed', 'true')
      await expect(page.locator('[data-selection-control][aria-pressed="true"]')).toHaveCount(4)
      await expect(entryFor(first)).toHaveClass(/outline-sky-500/)
    })

    test('touch tap on a folder opens it without selecting it', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await touchPoint(page, entryFor(checkbox))

      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore + 1)
      await expect(page.locator('[aria-pressed="true"][data-selection-control]')).toHaveCount(0)
    })

    test('Safari compatibility click after a touch pointer opens a folder', async ({ page }) => {
      const entry = entryFor(selectionButtons(page).first())
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await entry.dispatchEvent('pointerdown', { pointerType: 'touch', pointerId: 1, isPrimary: true })
      await entry.dispatchEvent('pointerup', { pointerType: 'touch', pointerId: 1, isPrimary: true })
      await entry.dispatchEvent('click', { detail: 1 })

      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore + 1)
      await expect(page.locator('[aria-pressed="true"][data-selection-control]')).toHaveCount(0)
    })

    test('parent navigation works with a single mouse click', async ({ page }) => {
      await entryFor(selectionButtons(page).first()).dblclick()
      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(2)
      const parent = page.getByRole('button', { name: 'Parent directory' })
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await expect(parent.locator('[data-level-up-icon]')).toBeVisible()
      await expect(parent.locator('.pi-folder')).toHaveCount(0)
      await expect(parent).not.toContainText('..')
      await parent.click()
      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore - 1)
    })

    test('parent navigation works with a single touch tap', async ({ page }) => {
      await entryFor(selectionButtons(page).first()).dblclick()
      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(2)
      const parent = page.getByRole('button', { name: 'Parent directory' })
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()

      await touchPoint(page, parent)

      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore - 1)
    })

    test('rapid double tap only opens one folder level', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const entry = entryFor(checkbox)
      const box = await entry.boundingBox()
      if (!box) throw new Error('Touch target is not visible')
      const breadcrumbBefore = await page.locator('nav[aria-label="Breadcrumb"] button').count()
      const x = box.x + box.width / 2
      const y = box.y + box.height / 2

      await page.touchscreen.tap(x, y)
      await page.touchscreen.tap(x, y)

      await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(breadcrumbBefore + 1)
    })

    test('browser context-menu event focuses the item without checking it and opens an actionable PrimeVue menu', async ({ page }) => {
      const checkbox = selectionButtons(page).first()

      await dispatchContextMenu(entryFor(checkbox))

      await expect(page.locator('.p-contextmenu')).toBeVisible()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
      await expect(entryFor(checkbox)).toHaveClass(/outline-sky-500/)
      await page.getByRole('menuitem', { name: 'Details' }).click()
      await expect(page.getByRole('heading', { name: 'Details' })).toBeVisible()
    })

    test('actions button opens the selected item context actions', async ({ page }) => {
      const checkbox = selectionButtons(page).first()

      await showSelectionControls(page)
      await checkbox.click()
      await expect(page.getByRole('button', { name: 'Copy', exact: true })).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Move', exact: true })).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0)
      const detailsBox = await page.getByRole('button', { name: 'Open details' }).boundingBox()
      const actionsBox = await page.getByRole('button', { name: 'Actions' }).boundingBox()
      expect(detailsBox).not.toBeNull()
      expect(actionsBox).not.toBeNull()
      expect(actionsBox!.x).toBeGreaterThan(detailsBox!.x)

      await page.getByRole('button', { name: 'Actions' }).click()

      const menu = page.getByRole('menu').filter({ visible: true })
      await expect(menu).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Open' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Download' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Details' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Copy', exact: true })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Cut', exact: true })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Copy to…' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Move to…' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Rename…' })).toBeVisible()
      await expect(menu.getByRole('menuitem', { name: 'Delete' })).toBeVisible()
    })

    test('multi-item actions do not start with an empty separator', async ({ page }) => {
      const checkboxes = selectionButtons(page)

      await showSelectionControls(page)
      await checkboxes.nth(0).click()
      await checkboxes.nth(1).click()
      await checkboxes.nth(2).click()
      await page.getByRole('button', { name: 'Actions' }).click()

      const menu = page.getByRole('menu').filter({ visible: true })
      await expect(menu.getByRole('menuitem', { name: 'Copy 3 items', exact: true })).toBeVisible()
      await expect(menu.locator('[role="separator"]')).toHaveCount(0)
    })

    test('touch long-press opens while held and remains open after release', async ({ page, browserName }) => {
      test.skip(browserName !== 'chromium', 'Playwright exposes raw touch hold injection through Chromium CDP only')
      const checkbox = selectionButtons(page).first()
      const menu = page.locator('.p-contextmenu')

      const entry = entryFor(checkbox)
      await longPressTouch(page, entry, 0, async () => {
        await expect(menu).toBeVisible()
        expect(await dispatchContextMenuProbe(entry)).toBe(false)
      })

      await expect(menu).toBeVisible()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
    })

    test('touch movement cancels long-press detection', async ({ page, browserName }) => {
      test.skip(browserName !== 'chromium', 'Playwright exposes raw touch hold injection through Chromium CDP only')
      const checkbox = selectionButtons(page).first()

      await longPressTouch(page, entryFor(checkbox), 20)

      await expect(page.locator('.p-contextmenu')).toBeHidden()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
    })

    test('stationary mouse-emulated long-press remains open after release', async ({ page, browserName }) => {
      test.skip(browserName !== 'chromium', 'Playwright exposes touch-from-mouse injection through Chromium CDP only')
      const checkbox = selectionButtons(page).first()
      const menu = page.locator('.p-contextmenu')

      await longPressWithEmulatedMouse(page, entryFor(checkbox), async () => {
        await expect(menu).toBeVisible()
      })

      await expect(menu).toBeVisible()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
    })

    test('single-pane dragging exposes only internal transfer data', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const entry = entryFor(checkbox)

      await expect(entry).toHaveAttribute('draggable', 'true')
      const dataTransfer = await page.evaluateHandle(() => new DataTransfer())
      await entry.dispatchEvent('dragstart', { dataTransfer })
      expect(await dataTransfer.evaluate(data => [...data.types])).toEqual(['application/x-webfile-items'])
      expect(await dataTransfer.evaluate(data => data.getData('application/x-webfile-items'))).toMatch(/^[0-9a-f-]{36}$/)
      await expect(checkbox).toHaveAttribute('aria-pressed', 'true')
      await entry.dispatchEvent('dragend', { dataTransfer })
    })

    test('PrimeVue dismisses the menu on an outside click', async ({ page }) => {
      const checkbox = selectionButtons(page).first()
      const oppositeView = page.getByRole('button', { name: view === 'list' ? 'Grid view' : 'List view' })
      const listHeader = page.locator('[aria-label="File sorting"]')

      await entryFor(checkbox).click({ button: 'right' })
      const menu = page.locator('.p-contextmenu')
      await expect(menu).toBeVisible()
      await expect(menu).not.toHaveClass(/p-contextmenu-enter-active/)
      await oppositeView.click()

      await expect(menu).toBeHidden()
      if (view === 'list') await expect(listHeader).toBeHidden()
      else await expect(listHeader).toBeVisible()
    })

    test('mouse context-menu focuses the item without checking it and commands remain actionable', async ({ page }) => {
      const checkbox = selectionButtons(page).first()

      await entryFor(checkbox).click({ button: 'right' })

      await expect(page.locator('.p-contextmenu')).toBeVisible()
      await expect(checkbox).toHaveAttribute('aria-pressed', 'false')
      await expect(entryFor(checkbox)).toHaveClass(/outline-sky-500/)
      await page.getByRole('menuitem', { name: 'Details' }).click()
      await expect(page.getByRole('heading', { name: 'Details' })).toBeVisible()
    })
  })
}

for (const action of ['Copy', 'Cut'] as const) {
  for (const view of ['list', 'grid'] as const) {
  test(`${view} ${action.toLowerCase()} and background paste targets the currently open directory`, async ({ page }) => {
    let submitted: Record<string, unknown> | undefined
    await page.route('**/api/fs/dir**', async route => {
      const path = new URL(route.request().url()).searchParams.get('path') ?? '/Files'
      const entries = path === '/Files'
        ? [
            { name: 'source', path: '/Files/source', is_dir: true, can_navigate: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
            { name: 'destination', path: '/Files/destination', is_dir: true, can_navigate: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
          ]
        : path === '/Files/source'
          ? [{ name: 'item.txt', path: '/Files/source/item.txt', is_dir: false, can_read: true, size: 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }]
          : []
      await route.fulfill({ json: { path, entries, single_root_path: '/Files' } })
    })
    await page.route('**/api/fs/operations', async route => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ json: { operations: [] } })
        return
      }
      submitted = route.request().postDataJSON() as Record<string, unknown>
      await route.fulfill({
        status: 202,
        json: {
          id: `${action.toLowerCase()}-paste`,
          kind: action === 'Copy' ? 'copy' : 'move',
          sources: ['/Files/source/item.txt'],
          destination: '/Files/destination',
          state: 'queued',
          phase: 'queued',
          message: 'Queued',
          progress_done: 0,
          progress_total: 1,
          progress_unit: 'items',
          bytes_done: 0,
          bytes_total: 1,
          items_done: 0,
          items_total: 1,
          cancellable: true,
          cleanup_policy: 'Incomplete output is removed.',
          started_at: '2000-01-01T00:00:00Z',
          updated_at: '2000-01-01T00:00:00Z',
        },
      })
    })

    await page.goto('/#path=%2Fsource')
    await chooseView(page, view)
    await showSelectionControls(page)
    await page.getByRole('button', { name: 'Select item.txt' }).click()
    await page.getByRole('button', { name: 'Actions' }).click()
    await page.getByRole('menu').filter({ visible: true }).getByRole('menuitem', { name: action, exact: true }).click()

    const sourceEntry = page.locator('[data-file-entry]').filter({ hasText: 'item.txt' })
    if (action === 'Cut') {
      await expect(sourceEntry).toHaveAttribute('data-cut', 'true')
    } else {
      await expect(sourceEntry).not.toHaveAttribute('data-cut', 'true')
    }
    await expect(page.getByText(`${action === 'Copy' ? 'Copied' : 'Cut'} 1 item`, { exact: false })).toHaveCount(0)
    await expect(page.getByText('Navigate to the destination and choose Paste.', { exact: true })).toHaveCount(0)

    const sourceViewport = page.locator('[data-file-viewport]')
    const sourceBox = (await sourceViewport.boundingBox())!
    await sourceViewport.click({ button: 'right', position: { x: sourceBox.width - 20, y: sourceBox.height - 20 } })
    await expect(page.locator('.p-contextmenu').getByRole('menuitem', { name: 'Paste', exact: true })).toBeVisible()
    await expect(page.locator('.p-contextmenu').getByRole('menuitem', { name: 'Delete', exact: true })).toHaveCount(0)
    await page.keyboard.press('Escape')

    await page.getByRole('button', { name: 'Files', exact: true }).click()
    await page.locator('[data-file-entry]').filter({ hasText: 'destination' }).dblclick()
    await expect(page).toHaveURL(/#path=%2Fdestination$/)
    await expect(page.getByRole('button', { name: 'Actions' })).toBeVisible()
    const viewport = page.locator('[data-file-viewport]')
    const box = (await viewport.boundingBox())!
    await viewport.click({ button: 'right', position: { x: box.width - 20, y: box.height - 20 } })
    await page.locator('.p-contextmenu').getByRole('menuitem', { name: 'Paste', exact: true }).click()

    await expect(page.getByText(/^(Copy|Move) complete$/)).toHaveCount(0)

    await expect.poll(() => submitted).toMatchObject({
      kind: action === 'Copy' ? 'copy' : 'move',
      sources: ['/Files/source/item.txt'],
      destination: '/Files/destination',
    })
    if (action === 'Copy') await expect(page.getByRole('button', { name: 'Actions' })).toBeVisible()
    else await expect(page.getByRole('button', { name: 'Actions' })).toHaveCount(0)
  })
  }
}

test('the grid split button changes and persists tile size', async ({ page }) => {
  let preferences = { view_mode: 'list', grid_size: 'medium', show_hidden: false, theme: 'dark' }
  await page.route('**/api/preferences', async route => {
    if (route.request().method() === 'PUT') {
      preferences = route.request().postDataJSON()
    }
    await route.fulfill({ json: preferences })
  })
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: Array.from({ length: 20 }, (_, index) => ({
        name: `file-${index}.txt`,
        path: `/file-${index}.txt`,
        is_dir: false,
        size: 100,
        modified: '2000-01-01T00:00:00Z',
        mode: '-rw-r--r--',
      })),
    },
  }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Grid view' }).click()
  const grid = page.locator('[data-grid-size]')
  await expect(grid).toHaveAttribute('data-grid-size', 'medium')
  const mediumWidth = (await page.locator('[data-file-entry]').first().boundingBox())?.width ?? 0

  await page.getByRole('button', { name: 'Grid size' }).click()
  await page.getByRole('menuitem', { name: 'Large tiles' }).click()
  await expect(grid).toHaveAttribute('data-grid-size', 'large')
  const largeWidth = (await page.locator('[data-file-entry]').first().boundingBox())?.width ?? 0
  expect(largeWidth).toBeGreaterThan(mediumWidth)

  await page.getByRole('button', { name: 'Grid size' }).click()
  await page.getByRole('menuitem', { name: 'Small tiles' }).click()
  await expect(grid).toHaveAttribute('data-grid-size', 'small')
  const smallWidth = (await page.locator('[data-file-entry]').first().boundingBox())?.width ?? 0
  expect(smallWidth).toBeLessThan(mediumWidth)
  await expect.poll(() => preferences.grid_size).toBe('small')

  await page.reload()
  await expect(page.locator('[data-grid-size]')).toHaveAttribute('data-grid-size', 'small')
})

test('settings persist appearance, editor defaults, and copy chunk size', async ({ page }) => {
  let preferences: Record<string, unknown> = {
    view_mode: 'list',
    grid_size: 'medium',
    show_hidden: false,
    theme: 'dark',
  }
  await page.route('**/api/preferences', async route => {
    if (route.request().method() === 'PUT') preferences = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({ json: preferences })
  })
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/folder',
      entries: [{
        name: 'notes.md',
        path: '/folder/notes.md',
        is_dir: false,
        size: 20,
        modified: '2000-01-01T00:00:00Z',
        mode: '-rw-r--r--',
      }],
    },
  }))
  await page.route('**/api/fs/file?**', route => route.fulfill({
    json: { path: '/folder/notes.md', content: '# Notes', modified: '2000-01-01T00:00:00Z', etag: 'notes-1' },
  }))
  await page.route('**/api/health', route => route.fulfill({
    json: { ok: true, version: '1.0.0', commit: 'abc123', build_date: '2026-10-12T10:00:00Z' },
  }))

  await page.goto('/#path=%2Ffolder')
  await page.getByRole('button', { name: 'Settings' }).click()
  const dialog = page.getByRole('dialog', { name: 'Settings' })
  await expect(dialog.getByRole('heading', { name: 'Appearance' })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: 'Editor' })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: 'Transfers' })).toBeVisible()
  await expect(dialog.getByRole('heading', { name: 'About' })).toBeVisible()
  await expect(dialog).toContainText('WebFile 1.0.0 · commit abc123 · built 2026-10-12T10:00:00Z')

  await dialog.getByRole('combobox', { name: 'Default view' }).click()
  await page.getByRole('option', { name: 'Grid', exact: true }).click()
  await dialog.getByRole('combobox', { name: 'Grid tile size' }).click()
  await page.getByRole('option', { name: 'Large', exact: true }).click()
  await dialog.getByRole('switch', { name: 'Show dotfiles by default' }).click()
  await dialog.getByRole('switch', { name: 'Hide inaccessible files and folders' }).click()
  await dialog.getByRole('switch', { name: 'Word wrap by default' }).click()
  await dialog.getByRole('spinbutton', { name: 'Editor font size' }).fill('18')
  await dialog.getByRole('switch', { name: 'Show minimap by default' }).click()
  await dialog.getByRole('combobox', { name: 'Default editor toolbar' }).click()
  await page.getByRole('option', { name: 'Rich', exact: true }).click()
  await dialog.getByRole('switch', { name: 'Markdown preview by default' }).click()
  await dialog.getByRole('combobox', { name: 'Copy chunk size' }).click()
  await page.getByRole('option', { name: '16 MiB', exact: true }).click()
  await dialog.getByRole('combobox', { name: 'Existing files when extracting' }).click()
  await page.getByRole('option', { name: 'Overwrite existing files', exact: true }).click()
  await dialog.getByRole('button', { name: 'Save settings' }).click()

  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: 'Grid view' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('[data-grid-size]')).toHaveAttribute('data-grid-size', 'large')
  await expect.poll(() => preferences).toMatchObject({
    view_mode: 'grid',
    grid_size: 'large',
    show_hidden: true,
    hide_inaccessible: true,
    extract_existing_files: 'overwrite',
    editor_word_wrap: true,
    editor_font_size: 18,
    editor_minimap: true,
    editor_toolbar: 'rich',
    editor_markdown_preview: true,
    copy_chunk_mib: 16,
  })

  await page.locator('[data-file-entry]').dblclick()
  const editor = page.getByRole('region', { name: 'Text editor' })
  await expect(editor).toHaveAttribute('data-toolbar-mode', 'rich')
  await expect(editor).toHaveAttribute('data-markdown-preview', 'true')
  await expect(page.getByRole('navigation', { name: 'Rich editor toolbar' })).toBeVisible()
  await expect(editor.getByText('18px · Wrap · Minimap', { exact: false })).toBeVisible()
})

test('delete is permanent and no recycle bin is exposed', async ({ page }) => {
  let submitted: Record<string, unknown> | undefined
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/folder',
      entries: [{
        name: 'delete-me.txt',
        path: '/folder/delete-me.txt',
        is_dir: false,
        size: 20,
        modified: '2000-01-01T00:00:00Z',
        mode: '-rw-r--r--',
      }],
    },
  }))
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { operations: [] } })
      return
    }
    submitted = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({
      status: 202,
      json: {
        id: 'delete-1',
        kind: 'delete',
        sources: ['/folder/delete-me.txt'],
        state: 'queued',
        phase: 'queued',
        message: 'Queued',
        progress_done: 0,
        progress_total: 1,
        progress_unit: 'items',
        bytes_done: 0,
        bytes_total: 0,
        items_done: 0,
        items_total: 1,
        cancellable: true,
        cleanup_policy: 'Deleted items cannot be recovered. Cancellation keeps items not yet deleted.',
        started_at: '2000-01-01T00:00:00Z',
        updated_at: '2000-01-01T00:00:00Z',
      },
    })
  })

  await page.goto('/#path=%2Ffolder')
  await expect(page.getByRole('button', { name: 'Open Recycle Bin' })).toHaveCount(0)
  await showSelectionControls(page)
  await selectionButtons(page).first().click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menu').filter({ visible: true }).getByRole('menuitem', { name: 'Delete' }).click()

  const confirmation = page.getByRole('alertdialog', { name: 'Delete' })
  await expect(confirmation).toContainText('Permanently delete “delete-me.txt”? This cannot be undone.')
  await confirmation.getByRole('button', { name: 'Delete permanently' }).click()

  await expect.poll(() => submitted).toEqual({
    kind: 'delete',
    sources: ['/folder/delete-me.txt'],
  })
  const tasksButton = page.getByRole('button', { name: 'Open tasks' })
  await expect(tasksButton).toHaveClass(/webfile-task-attention/)
  await expect(page.getByRole('complementary')).toHaveCount(0)
  await tasksButton.click()
  await expect(page.getByRole('button', { name: 'Close tasks' })).not.toHaveClass(/webfile-task-attention/)
  await expect(page.getByRole('complementary')).toBeVisible()
})

test('folder navigation updates the hash and browser back restores the previous folder', async ({ page }) => {

  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const child = path === '/' ? '/one' : path === '/one' ? '/one/two' : ''
    await route.fulfill({
      json: {
        path,
        entries: child ? [{ name: child.split('/').pop(), path: child, is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }] : [],
      },
    })
  })

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  await expect(page).toHaveURL(/#path=%2Fone$/)
  await page.locator('[data-file-entry]').dblclick()
  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)

  await page.goBack()

  await expect(page).toHaveURL(/#path=%2Fone$/)
  await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveCount(2)
})

test('a single configured root becomes Files and is hidden from navigation', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const entries = path === '/'
      ? [{ name: 'Only Root', path: '/Only Root', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }]
      : path === '/Only Root'
        ? [{ name: 'child', path: '/Only Root/child', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }]
        : []
    await route.fulfill({ json: { path, entries, single_root_path: '/Only Root' } })
  })

  await page.goto('/')
  await expect(page).toHaveURL(/#path=%2F$/)
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'Only Root' })).toHaveCount(0)
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'child' })).toBeVisible()
  await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveText(['Files'])
  await expect(page.getByRole('button', { name: 'Parent directory' })).toHaveCount(0)

  await page.locator('[data-file-entry]').filter({ hasText: 'child' }).dblclick()
  await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveText(['Files', 'child'])
  await expect(page).toHaveURL(/#path=%2Fchild$/)
  await page.getByRole('button', { name: 'Files', exact: true }).click()
  await expect(page).toHaveURL(/#path=%2F$/)
})

test('a root-relative deep link resolves within the single configured root', async ({ page }) => {
  const requestedPaths: string[] = []
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    requestedPaths.push(path)
    await route.fulfill({ json: { path, entries: [], single_root_path: '/Only Root' } })
  })

  await page.goto('/#path=%2Fchild')

  await expect(page).toHaveURL(/#path=%2Fchild$/)
  await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveText(['Files', 'child'])
  await expect.poll(() => requestedPaths).toContain('/Only Root/child')

  await page.goto('/#path=%2FOnly%20Root%2Fchild')
  await expect(page).toHaveURL(/#path=%2Fchild$/)
})

test('multiple configured roots remain visible below Files', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    await route.fulfill({
      json: {
        path,
        entries: path === '/' ? [
          { name: 'First Root', path: '/First Root', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
          { name: 'Second Root', path: '/Second Root', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
        ] : [],
      },
    })
  })

  await page.goto('/')
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'First Root' })).toBeVisible()
  await expect(page.locator('[data-file-entry]').filter({ hasText: 'Second Root' })).toBeVisible()
  await expect(page.locator('nav[aria-label="Breadcrumb"] button')).toHaveText(['Files'])
})

test('the destination browser also treats a single configured root as Files', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/Only Root'
    const entries = path === '/Only Root'
      ? [
          { name: 'folder', path: '/Only Root/folder', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
          { name: 'file.txt', path: '/Only Root/file.txt', is_dir: false, size: 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        ]
      : []
    await route.fulfill({ json: { path, entries, single_root_path: '/Only Root' } })
  })

  await page.goto('/#path=%2FOnly%20Root')
  await showSelectionControls(page)
  await selectionButtons(page).nth(1).click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Copy to…' }).click()

  const dialog = page.getByRole('dialog', { name: 'Copy to…' })
  await expect(dialog.getByRole('button', { name: 'Files', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Only Root', exact: true })).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: 'Parent directory' })).toHaveCount(0)
})

test('destination folders select on one click or tap and open on a double action', async ({ page }) => {
  const requestedPaths: string[] = []
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/Files'
    requestedPaths.push(path)
    const entries = path === '/Files'
      ? [
          { name: 'folder', path: '/Files/folder', is_dir: true, can_navigate: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' },
          { name: 'source.txt', path: '/Files/source.txt', is_dir: false, can_read: true, size: 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        ]
      : []
    await route.fulfill({ json: { path, entries, single_root_path: '/Files' } })
  })

  await page.goto('/#path=%2FFiles')
  await showSelectionControls(page)
  await page.locator('[data-file-entry]').filter({ hasText: 'source.txt' }).locator('[data-selection-control]').click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Copy to…' }).click()

  let dialog = page.getByRole('dialog', { name: 'Copy to…' })
  let folder = dialog.getByRole('button', { name: 'folder', exact: true })
  const requestsBeforeClick = requestedPaths.length

  await folder.click()

  await expect(dialog).toContainText('Destination: /Files/folder')
  await expect(dialog.getByRole('button', { name: 'Files', exact: true })).toBeVisible()
  expect(requestedPaths).toHaveLength(requestsBeforeClick)

  await folder.dblclick()

  await expect(dialog.getByRole('button', { name: 'folder', exact: true })).toBeVisible()
  await expect(dialog).toContainText('Destination: /Files/folder')
  await expect.poll(() => requestedPaths).toContain('/Files/folder')

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Copy to…' }).click()
  dialog = page.getByRole('dialog', { name: 'Copy to…' })
  folder = dialog.getByRole('button', { name: 'folder', exact: true })
  const requestsBeforeTap = requestedPaths.length

  await touchPoint(page, folder)

  await expect(dialog).toContainText('Destination: /Files/folder')
  await expect(dialog.getByRole('button', { name: 'Files', exact: true })).toBeVisible()
  expect(requestedPaths).toHaveLength(requestsBeforeTap)

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Copy to…' }).click()
  dialog = page.getByRole('dialog', { name: 'Copy to…' })
  folder = dialog.getByRole('button', { name: 'folder', exact: true })
  const box = await folder.boundingBox()
  if (!box) throw new Error('Touch target is not visible')
  const x = box.x + box.width / 2
  const y = box.y + box.height / 2

  await page.touchscreen.tap(x, y)
  await page.touchscreen.tap(x, y)

  await expect(dialog.getByRole('button', { name: 'folder', exact: true })).toBeVisible()
  await expect.poll(() => requestedPaths).toContain('/Files/folder')
})

for (const mode of ['Copy', 'Move'] as const) {
  test(`${mode.toLowerCase()} destination can create and select a new folder`, async ({ page }) => {
    const entries = [
      { name: 'source.txt', path: '/Files/source.txt', is_dir: false, can_read: true, size: 1, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
    ]
    const createdPaths: string[] = []
    await page.route('**/api/fs/dir**', route => route.fulfill({
      json: { path: '/Files', entries, single_root_path: '/Files' },
    }))
    await page.route('**/api/fs/mkdir', async route => {
      const { path } = route.request().postDataJSON() as { path: string }
      createdPaths.push(path)
      entries.push({
        name: path.split('/').pop()!,
        path,
        is_dir: true,
        can_read: true,
        size: 0,
        modified: '2000-01-01T00:00:00Z',
        mode: 'drwxr-xr-x',
      })
      await route.fulfill({ json: { ok: true } })
    })

    await page.goto('/#path=%2FFiles')
    await showSelectionControls(page)
    await page.locator('[data-file-entry]').filter({ hasText: 'source.txt' }).locator('[data-selection-control]').click()
    await page.getByRole('button', { name: 'Actions' }).click()
    await page.getByRole('menuitem', { name: `${mode} to…` }).click()

    const destinationDialog = page.getByRole('dialog', { name: `${mode} to…` })
    await destinationDialog.getByRole('button', { name: 'New folder' }).click()
    const nameDialog = page.getByRole('dialog', { name: 'New folder' })
    await nameDialog.locator('input').fill(`${mode.toLowerCase()}-destination`)
    await nameDialog.getByRole('button', { name: 'Create', exact: true }).click()

    const createdPath = `/Files/${mode.toLowerCase()}-destination`
    await expect.poll(() => createdPaths).toEqual([createdPath])
    await expect(destinationDialog.getByRole('button', { name: `${mode.toLowerCase()}-destination`, exact: true })).toBeVisible()
    await expect(destinationDialog).toContainText(`Destination: ${createdPath}`)
  })
}

test('the destination browser uses the main listing visibility preferences', async ({ page }) => {
  await page.route('**/api/preferences', route => route.fulfill({
    json: { show_hidden: false, hide_inaccessible: true },
  }))
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/Files'
    await route.fulfill({
      json: {
        path,
        single_root_path: '/Files',
        entries: [{
          name: 'visible',
          path: '/Files/visible',
          is_dir: true,
          can_navigate: true,
          size: 0,
          modified: '2000-01-01T00:00:00Z',
          mode: 'drwxr-xr-x',
        }, {
          name: '.hidden',
          path: '/Files/.hidden',
          is_dir: true,
          can_navigate: true,
          size: 0,
          modified: '2000-01-01T00:00:00Z',
          mode: 'drwxr-xr-x',
        }, {
          name: 'restricted',
          path: '/Files/restricted',
          is_dir: true,
          can_navigate: false,
          size: 0,
          modified: '2000-01-01T00:00:00Z',
          mode: 'd---------',
        }, {
          name: 'source.txt',
          path: '/Files/source.txt',
          is_dir: false,
          can_read: true,
          size: 1,
          modified: '2000-01-01T00:00:00Z',
          mode: '-rw-r--r--',
        }],
      },
    })
  })

  await page.goto('/#path=%2FFiles')
  await showSelectionControls(page)
  await page.locator('[data-file-entry]').filter({ hasText: 'source.txt' }).locator('[data-selection-control]').click()
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Copy to…' }).click()

  const dialog = page.getByRole('dialog', { name: 'Copy to…' })
  await expect(dialog.getByRole('button', { name: 'visible', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '.hidden', exact: true })).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: 'restricted', exact: true })).toHaveCount(0)
})

test('breadcrumb navigation pops directly to a visited ancestor with one popstate', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const child = path === '/' ? '/one' : path === '/one' ? '/one/two' : path === '/one/two' ? '/one/two/three' : ''
    await route.fulfill({
      json: {
        path,
        entries: child ? [{ name: child.split('/').pop(), path: child, is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }] : [],
      },
    })
  })

  await page.goto('/')
  for (const path of ['/one', '/one/two', '/one/two/three']) {
    await page.locator('[data-file-entry]').dblclick()
    await expect(page).toHaveURL(new RegExp(`#path=${encodeURIComponent(path)}$`))
  }
  await page.evaluate(() => {
    const target = window as typeof window & { __breadcrumbPopCount?: number }
    target.__breadcrumbPopCount = 0
    window.addEventListener('popstate', () => { target.__breadcrumbPopCount = (target.__breadcrumbPopCount ?? 0) + 1 })
  })

  await page.getByRole('button', { name: 'one', exact: true }).click()

  await expect(page).toHaveURL(/#path=%2Fone$/)
  await expect.poll(() => page.evaluate(() => (window as typeof window & { __breadcrumbPopCount?: number }).__breadcrumbPopCount)).toBe(1)
  await page.goForward()
  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)
})

test('breadcrumb navigation pushes when a deep-linked ancestor is absent from history', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    await route.fulfill({ json: { path, entries: [] } })
  })

  await page.goto('/#path=%2Fone%2Ftwo')
  await page.getByRole('button', { name: 'one', exact: true }).click()
  await expect(page).toHaveURL(/#path=%2Fone$/)

  await page.goBack()
  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)
})

test('parent directory navigation pops to its existing history entry', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const child = path === '/' ? '/one' : path === '/one' ? '/one/two' : path === '/one/two' ? '/one/two/three' : ''
    await route.fulfill({
      json: {
        path,
        entries: child ? [{ name: child.split('/').pop(), path: child, is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }] : [],
      },
    })
  })

  await page.goto('/')
  for (const path of ['/one', '/one/two', '/one/two/three']) {
    await page.locator('[data-file-entry]').dblclick()
    await expect(page).toHaveURL(new RegExp(`#path=${encodeURIComponent(path)}$`))
  }
  await page.evaluate(() => {
    const target = window as typeof window & { __parentPopCount?: number }
    target.__parentPopCount = 0
    window.addEventListener('popstate', () => { target.__parentPopCount = (target.__parentPopCount ?? 0) + 1 })
  })

  await page.getByRole('button', { name: 'Parent directory' }).click()

  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)
  await expect.poll(() => page.evaluate(() => (window as typeof window & { __parentPopCount?: number }).__parentPopCount)).toBe(1)
  await page.goForward()
  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo%2Fthree$/)
})

test('parent directory navigation pushes when its target is absent from history', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    await route.fulfill({ json: { path, entries: [] } })
  })

  await page.goto('/#path=%2Fone%2Ftwo')
  await page.getByRole('button', { name: 'Parent directory' }).click()
  await expect(page).toHaveURL(/#path=%2Fone$/)

  await page.goBack()
  await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)
})

for (const view of ['list', 'grid'] as const) {
  test(`rapid stationary mouse clicks navigate successive folders in ${view} view`, async ({ page, browserName }) => {
    test.skip(browserName !== 'chromium', 'Exact repeated mouse click counts require Chromium CDP')
    await page.route('**/api/fs/dir**', async route => {
      const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
      const children: Record<string, string> = {
        '/one': '/one/two',
        '/one/two': '/one/two/three',
        '/one/two/three': '/one/two/three/four',
        '/one/two/three/four': '/one/two/three/four/five',
      }
      const child = children[path]
      await route.fulfill({
        json: {
          path,
          entries: child ? [{ name: child.split('/').pop(), path: child, is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }] : [],
        },
      })
    })

    await page.goto('/#path=%2Fone')
    await chooseView(page, view)
    const session = await page.context().newCDPSession(page)
    const entryBox = await page.locator('[data-file-entry]').boundingBox()
    expect(entryBox).not.toBeNull()
    const entryX = entryBox!.x + entryBox!.width / 2
    const entryY = entryBox!.y + entryBox!.height / 2
    for (let clickCount = 1; clickCount <= 8; clickCount++) {
      await session.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: entryX, y: entryY, button: 'left', clickCount })
      await session.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: entryX, y: entryY, button: 'left', clickCount })
      if (clickCount % 2 === 0) await page.waitForTimeout(80)
    }
    await expect(page).toHaveURL(/#path=%2Fone%2Ftwo%2Fthree%2Ffour%2Ffive$/)

    const parentBox = await page.getByRole('button', { name: 'Parent directory' }).boundingBox()
    expect(parentBox).not.toBeNull()
    const parentX = parentBox!.x + parentBox!.width / 2
    const parentY = parentBox!.y + parentBox!.height / 2
    for (let clickCount = 1; clickCount <= 3; clickCount++) {
      await session.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: parentX, y: parentY, button: 'left', clickCount })
      await session.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: parentX, y: parentY, button: 'left', clickCount })
      await page.waitForTimeout(80)
    }
    await session.detach()
    await expect(page).toHaveURL(/#path=%2Fone%2Ftwo$/)
  })
}

test('Alt+Enter toggles details for the focused item', async ({ page }) => {
  await openConfiguredRoot(page)
  const checkbox = selectionButtons(page).first()
  const entry = entryFor(checkbox)

  await entry.click()
  await page.keyboard.press('Alt+Enter')

  await expect(page.getByRole('heading', { name: 'Details' })).toBeVisible()
  await expect(page.locator('button[aria-label="Close details"][aria-pressed="true"]')).toBeVisible()
  await expect(entry).toHaveClass(/outline-sky-500/)

  await page.keyboard.press('Alt+Enter')

  await expect(page.getByRole('heading', { name: 'Details' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Open details' })).toHaveAttribute('aria-pressed', 'false')
})

test('an overflowing breadcrumb starts at the current rightmost location', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const path = '/root/very-long-project-name/source/generated/build/output/framework/current-location'
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path, entries: [] } }))

  await page.goto(`/#path=${encodeURIComponent(path)}`)

  const breadcrumb = page.locator('[data-breadcrumb-scroll]')
  await expect.poll(() => breadcrumb.evaluate(element => element.scrollWidth - element.clientWidth)).toBeGreaterThan(0)
  await expect.poll(() => breadcrumb.evaluate(element => Math.abs(element.scrollLeft - (element.scrollWidth - element.clientWidth)))).toBeLessThanOrEqual(1)

  const current = breadcrumb.locator('[aria-current="page"]')
  await expect(current).toHaveText('current-location')
  await expect(current).toBeInViewport()
})

test('breadcrumb follows content width changes without resizing its viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const path = '/root/very-long-project-name/source/generated/build/output/framework/current-location'
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path, entries: [] } }))
  await page.goto(`/#path=${encodeURIComponent(path)}`)
  const breadcrumb = page.locator('[data-breadcrumb-scroll]')
  await expect.poll(() => breadcrumb.evaluate(element => element.scrollWidth - element.clientWidth)).toBeGreaterThan(0)
  const originalWidth = await breadcrumb.evaluate(element => element.scrollWidth)
  // Reproduce late font/icon metrics changing content while the viewport stays fixed.
  await breadcrumb.locator(':scope > div').evaluate(element => { element.style.columnGap = '32px' })
  await expect.poll(() => breadcrumb.evaluate(element => element.scrollWidth)).toBeGreaterThan(originalWidth)
  await expect.poll(() => breadcrumb.evaluate(element => Math.abs(element.scrollLeft - (element.scrollWidth - element.clientWidth)))).toBeLessThanOrEqual(1)
  await expect(breadcrumb.locator('[aria-current="page"]')).toBeInViewport()
})

test('browser back closes an open preview before navigating the file tree', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/pictures',
      entries: [{ name: 'photo.svg', path: '/pictures/photo.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/pictures/photo.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="red"/></svg>',
  }))

  await page.goto('/#path=%2Fpictures')
  await page.locator('[data-file-entry]').dblclick()
  await expect(page.getByLabel('File preview')).toBeVisible()
  await expect(page).toHaveURL(/#path=%2Fpictures&file=photo\.svg$/)

  await page.reload()
  await expect(page.getByLabel('File preview')).toBeVisible()

  await page.goBack()

  await expect(page).toHaveURL(/#path=%2Fpictures$/)
  await expect(page.getByLabel('File preview')).toHaveCount(0)
  await expect(page.locator('[data-file-entry]')).toBeVisible()

  await page.goForward()
  await expect(page.getByLabel('File preview')).toBeVisible()
})

test('gallery back always returns to the current folder list', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/pictures',
      entries: [{ name: 'photo.svg', path: '/pictures/photo.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/pictures/photo.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="red"/></svg>',
  }))

  await page.goto('/#path=%2Fpictures&file=photo.svg')
  await expect(page.getByLabel('File preview')).toBeVisible()

  await page.getByRole('button', { name: 'Back to folder' }).click()

  await expect(page).toHaveURL(/#path=%2Fpictures$/)
  await expect(page.getByLabel('File preview')).toHaveCount(0)
  await expect(page.locator('[data-file-entry]')).toBeVisible()

  await page.locator('[data-file-entry]').dblclick()
  await expect(page.getByLabel('File preview')).toBeVisible()
  await page.keyboard.press('Escape')

  await expect(page).toHaveURL(/#path=%2Fpictures$/)
  await expect(page.getByLabel('File preview')).toHaveCount(0)
})

test('the file hash restores an editor across reload, back, and forward navigation', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/docs',
      entries: [{ name: 'notes.txt', path: '/docs/notes.txt', is_dir: false, size: 12, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/docs/notes.txt', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: { path: '/docs/notes.txt', content: 'Editor text', modified: '2000-01-01T00:00:00Z', etag: 'etag' },
  }))

  await page.goto('/#path=%2Fdocs')
  await page.locator('[data-file-entry]').dblclick()

  await expect(page).toHaveURL(/#path=%2Fdocs&file=notes\.txt$/)
  await expect(page.getByLabel('Text editor')).toBeVisible()

  await page.reload()
  await expect(page.getByLabel('Text editor')).toBeVisible()
  await expect(page.getByText('/docs/notes.txt', { exact: true })).toBeVisible()

  await page.goBack()
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await expect(page.getByLabel('Text editor')).toHaveCount(0)
  await expect(page.locator('[data-file-entry]')).toBeVisible()

  await page.goForward()
  await expect(page.getByLabel('Text editor')).toBeVisible()

  await page.keyboard.press('Escape')

  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await expect(page.getByLabel('Text editor')).toHaveCount(0)
  await expect(page.locator('[data-file-entry]')).toBeVisible()
})

test('saving recreates an edited file that was removed after opening', async ({ page }) => {
  let saveAttempts = 0
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/docs',
      entries: [{ name: 'notes.txt', path: '/docs/notes.txt', is_dir: false, size: 12, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/docs/notes.txt', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        json: { path: '/docs/notes.txt', content: 'Editor text', modified: '2000-01-01T00:00:00Z', etag: 'original-etag' },
      })
      return
    }

    saveAttempts += 1
    if (saveAttempts === 1) {
      expect(route.request().headers()['if-match']).toBe('original-etag')
      await route.fulfill({ status: 404, json: { error: 'file not found' } })
      return
    }

    expect(route.request().headers()['if-none-match']).toBe('*')
    await route.fulfill({
      json: {
        path: '/docs/notes.txt',
        content: route.request().postData() ?? '',
        modified: '2000-01-01T00:00:01Z',
        etag: 'recreated-etag',
      },
    })
  })

  await page.goto('/#path=%2Fdocs&file=notes.txt')
  const editor = page.getByRole('textbox', { name: 'Editor content' })
  await editor.focus()
  await page.keyboard.type(' updated')
  await page.getByRole('button', { name: 'Save', exact: true }).click()

  await expect(page.getByText('Saved', { exact: true })).toBeVisible()
  expect(saveAttempts).toBe(2)
})

test('Ctrl+S saves editor content instead of invoking the browser save action', async ({ page }) => {
  let savedContent = ''
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/docs',
      entries: [{ name: 'notes.txt', path: '/docs/notes.txt', is_dir: false, size: 12, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/docs/notes.txt', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        json: { path: '/docs/notes.txt', content: 'Editor text', modified: '2000-01-01T00:00:00Z', etag: 'original-etag' },
      })
      return
    }

    expect(route.request().headers()['if-match']).toBe('original-etag')
    savedContent = route.request().postData() ?? ''
    await route.fulfill({
      json: {
        path: '/docs/notes.txt',
        content: savedContent,
        modified: '2000-01-01T00:00:01Z',
        etag: 'saved-etag',
      },
    })
  })

  await page.goto('/#path=%2Fdocs&file=notes.txt')
  const editor = page.getByRole('textbox', { name: 'Editor content' })
  await editor.focus()
  await page.keyboard.type(' updated')
  await page.keyboard.press('Control+s')

  await expect.poll(() => savedContent).toContain(' updated')
  await expect(page.getByText('Saved', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Text editor')).toBeVisible()
})

test('a legacy full-path file hash is restored and normalized to a filename', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/docs',
      entries: [{ name: 'notes.txt', path: '/docs/notes.txt', is_dir: false, size: 12, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/docs/notes.txt', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: { path: '/docs/notes.txt', content: 'Editor text', modified: '2000-01-01T00:00:00Z', etag: 'etag' },
  }))

  await page.goto('/#path=%2Fdocs&file=%2Fdocs%2Fnotes.txt')

  await expect(page.getByLabel('Text editor')).toBeVisible()
  await expect(page).toHaveURL(/#path=%2Fdocs&file=notes\.txt$/)
})

for (const view of ['list', 'grid'] as const) {
  test(`creating a folder aligns it to the top of the scrolled ${view} view but preserves the top position`, async ({ page }) => {
    const entries = Array.from({ length: 60 }, (_, index) => ({
      name: `folder-${index.toString().padStart(2, '0')}`,
      path: `/folder/folder-${index.toString().padStart(2, '0')}`,
      is_dir: true,
      size: 0,
      modified: '2000-01-01T00:00:00Z',
      mode: 'drwxr-xr-x',
    }))

    await page.route('**/api/fs/dir**', route => route.fulfill({
      json: { path: '/folder', entries },
    }))
    await page.route('**/api/fs/mkdir', async route => {
      const { path } = route.request().postDataJSON() as { path: string }
      const name = path.split('/').pop()!
      entries.push({ name, path, is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' })
      entries.sort((left, right) => left.name.localeCompare(right.name))
      await route.fulfill({ json: { ok: true } })
    })

    await page.goto('/#path=%2Ffolder')
    await chooseView(page, view)
    const viewport = page.locator('[data-file-viewport]')
    await viewport.evaluate(element => element.scrollTo(0, 900))
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBeGreaterThan(0)

    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('dialog', { name: 'New folder' }).locator('input').fill('folder-30-created')
    await page.getByRole('button', { name: 'Create', exact: true }).click()

    const createdEntry = page.locator('[data-file-entry]').filter({ hasText: 'folder-30-created' })
    await expect(createdEntry).toHaveClass(/outline-sky-500/)
    const alignment = await createdEntry.evaluate(element => {
      const viewportElement = document.querySelector('[data-file-viewport]')!
      return element.getBoundingClientRect().top - viewportElement.getBoundingClientRect().top
    })
    expect(alignment).toBeGreaterThanOrEqual(0)
    expect(alignment).toBeLessThanOrEqual(10)

    await viewport.evaluate(element => element.scrollTo(0, 0))
    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(0)
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('dialog', { name: 'New folder' }).locator('input').fill('folder-top-created')
    await page.getByRole('button', { name: 'Create', exact: true }).click()

    await expect.poll(() => viewport.evaluate(element => element.scrollTop)).toBe(0)
  })
}

test('an unmapped file opens the preview fallback instead of the text editor', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'sample.bin', path: '/sample.bin', is_dir: false, size: 10, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  await expect(page.getByLabel('File preview')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'No preview available' })).toBeVisible()
  await expect(page.getByText("This type of file can't be previewed.", { exact: true })).toBeVisible()
  await expect(page.getByLabel('File preview').getByRole('button', { name: 'Download' })).toHaveCount(0)
  await expect(page.getByLabel('File preview')).toHaveAttribute('data-immersive', 'false')
  await expect(page.getByRole('button', { name: 'Back to folder' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enter fullscreen' })).toHaveCount(0)
})

test('extensionless text detected by content opens in the editor', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'LICENSE', path: '/LICENSE', is_dir: false, size: 20, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/LICENSE', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({ json: { path: '/LICENSE', content: 'Terms and conditions', modified: '2000-01-01T00:00:00Z', etag: 'etag' } }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  await expect(page.getByRole('button', { name: 'Close editor' })).toBeVisible()
  await expect(page.getByText('/LICENSE', { exact: true })).toBeVisible()
})

test('extensionless image detected by content opens in the image preview', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'picture', path: '/picture', is_dir: false, size: 20, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/picture', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="red"/></svg>',
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  await expect(page.getByLabel('File preview')).toBeVisible()
  await expect(page.getByRole('img', { name: 'picture' })).toBeVisible()
})

test('misleading jpg content opens as detected text rather than an image', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'notes.jpg', path: '/notes.jpg', is_dir: false, size: 20, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/notes.jpg', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({ json: { path: '/notes.jpg', content: 'not an image', modified: '2000-01-01T00:00:00Z', etag: 'etag' } }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  await expect(page.getByRole('button', { name: 'Close editor' })).toBeVisible()
  await expect(page.getByLabel('File preview')).toHaveCount(0)
})

test('the detected editor language can be searched and overridden', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'CONFIG', path: '/CONFIG', is_dir: false, size: 19, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/CONFIG', content_type: 'text/plain; charset=utf-8' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: { path: '/CONFIG', content: '# Heading\n\nContent', modified: '2000-01-01T00:00:00Z', etag: 'etag' },
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  const editor = page.getByLabel('Text editor')
  await expect(editor).toHaveAttribute('data-language', 'plaintext')
  await page.getByRole('button', { name: 'Select language mode, currently Plain Text' }).click()
  await expect(page.getByRole('region', { name: 'Select language mode' })).toBeVisible()

  await page.getByRole('textbox', { name: 'Search languages' }).fill('markdown')
  await expect(page.getByRole('option', { name: 'Markdown' })).toBeVisible()
  await page.getByRole('option', { name: 'Markdown' }).click()

  await expect(editor).toHaveAttribute('data-language', 'markdown')
  const languageButton = page.getByRole('button', { name: 'Select language mode, currently Markdown' })
  await expect(languageButton).toBeVisible()

  await languageButton.click()
  const languageList = page.getByRole('listbox', { name: 'Language modes' })
  const currentLanguage = page.getByRole('option', { name: 'Markdown' })
  await expect(currentLanguage).toBeInViewport()
  await expect.poll(() => languageList.evaluate(element => element.scrollTop)).toBeGreaterThan(0)

  await languageButton.click()
  await expect(page.getByRole('region', { name: 'Select language mode' })).toHaveCount(0)

  await page.getByRole('button', { name: 'Show rich toolbar' }).click()
  await expect(page.getByRole('button', { name: 'Show Markdown preview' })).toBeVisible()
})

test('the text editor can switch between compact and rich toolbars', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'sample.js', path: '/sample.js', is_dir: false, size: 24, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: { path: '/sample.js', content: 'const value = true\n', modified: '2000-01-01T00:00:00Z', etag: 'etag' },
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  const editor = page.getByLabel('Text editor')
  await expect(editor).toHaveAttribute('data-toolbar-mode', 'compact')
  await expect(page.getByRole('navigation', { name: 'Rich editor toolbar' })).toHaveCount(0)

  await page.getByRole('button', { name: 'Show rich toolbar' }).click()

  await expect(editor).toHaveAttribute('data-toolbar-mode', 'rich')
  await expect(page.getByRole('navigation', { name: 'Rich editor toolbar' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Show minimap' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Show whitespace' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Hide line numbers' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Format document' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Decrease font size' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Reset font size' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Increase font size' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enter Zen mode' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enter fullscreen' })).toBeVisible()

  await page.getByRole('button', { name: 'Show minimap' }).click()
  await expect(page.getByRole('button', { name: 'Hide minimap' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('.monaco-editor .minimap')).toBeVisible()

  await page.getByRole('button', { name: 'Increase font size' }).click()
  await expect(editor).toContainText('15px')
  await expect(page.getByRole('button', { name: 'Reset font size' })).toBeEnabled()

  await page.getByRole('button', { name: 'Hide line numbers' }).click()
  await expect(page.getByRole('button', { name: 'Show line numbers' })).toHaveAttribute('aria-pressed', 'false')

  await page.getByRole('button', { name: 'Enter Zen mode' }).click()
  await expect(editor).toHaveAttribute('data-zen', 'true')
  await expect(page.getByRole('button', { name: 'Close editor' })).toBeHidden()
  await expect(page.getByRole('button', { name: 'Exit Zen mode' })).toBeVisible()

  await page.getByRole('button', { name: 'Exit Zen mode' }).click()
  await expect(editor).toHaveAttribute('data-zen', 'false')
  await expect(page.getByRole('button', { name: 'Close editor' })).toBeVisible()
})

test('the find and replace widget overlays the editor without moving file contents', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'sample.txt', path: '/sample.txt', is_dir: false, size: 18, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: { path: '/sample.txt', content: 'first line\nsecond line\n', modified: '2000-01-01T00:00:00Z', etag: 'etag' },
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()

  const firstLine = page.locator('.monaco-editor .view-line').first()
  await expect(firstLine).toBeVisible()
  const initialTop = await firstLine.evaluate(element => element.getBoundingClientRect().top)

  await page.getByRole('button', { name: 'Replace' }).click()
  const findWidget = page.locator('.monaco-editor .find-widget')
  await expect(findWidget).toBeVisible()

  const findTop = await firstLine.evaluate(element => element.getBoundingClientRect().top)
  expect(Math.abs(findTop - initialTop)).toBeLessThanOrEqual(1)

  await findWidget.getByRole('textbox').first().focus()
  await page.keyboard.press('Escape')
  await expect(findWidget).toHaveAttribute('aria-hidden', 'true')
  await expect(page.getByRole('button', { name: 'Close editor' })).toBeVisible()

  await page.getByRole('textbox', { name: 'Editor content' }).focus()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Close editor' })).toBeHidden()
})

test('Markdown preview renders a sanitized live split view with relative images', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/docs',
      entries: [{ name: 'README.md', path: '/docs/README.md', is_dir: false, size: 90, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/file**', route => route.fulfill({
    json: {
      path: '/docs/README.md',
      content: '# Preview title\n\n![Example](assets/image.png)\n\n<script>window.markdownInjected = true</script>',
      modified: '2000-01-01T00:00:00Z',
      etag: 'etag',
    },
  }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="green"/></svg>',
  }))

  await page.goto('/#path=%2Fdocs')
  await page.locator('[data-file-entry]').dblclick()
  await page.getByRole('button', { name: 'Show rich toolbar' }).click()
  await page.getByRole('button', { name: 'Show Markdown preview' }).click()

  const editor = page.getByLabel('Text editor')
  const preview = page.getByRole('complementary', { name: 'Markdown preview' })
  await expect(editor).toHaveAttribute('data-markdown-preview', 'true')
  await expect(preview).toBeVisible()
  await expect(preview.getByRole('heading', { name: 'Preview title' })).toBeVisible()
  await expect(preview.locator('script')).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => (window as Window & { markdownInjected?: boolean }).markdownInjected)).toBeUndefined()

  const image = preview.getByRole('img', { name: 'Example' })
  await expect(image).toHaveAttribute('src', /\/api\/fs\/download\?path=%2Fdocs%2Fassets%2Fimage\.png&inline=true/)

  const splitWidths = await Promise.all([
    page.locator('.monaco-editor').evaluate(element => element.getBoundingClientRect().width),
    preview.evaluate(element => element.getBoundingClientRect().width),
  ])
  expect(Math.abs(splitWidths[0] - splitWidths[1])).toBeLessThanOrEqual(2)

  await page.locator('.monaco-editor').click()
  await page.keyboard.press('Control+A')
  await page.keyboard.type('# Updated live')
  await expect(preview.getByRole('heading', { name: 'Updated live' })).toBeVisible()
})

test('a cached folder size is shown without calculating it during listing', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'folder', path: '/folder', is_dir: true, size: 7, size_status: 'ready', modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }],
    },
  }))

  await page.goto('/')

  await expect(page.locator('[data-file-entry]').getByText('7 B', { exact: true })).toBeVisible()
})

test('details follows row focus and falls back to the current folder during navigation', async ({ page }) => {
  await page.route('**/api/fs/dir**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    const entries = path === '/'
      ? [{ name: 'root', path: '/root', is_dir: true, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x' }]
      : [
          { name: 'first.txt', path: '/root/first.txt', is_dir: false, size: 5, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
          { name: 'second.txt', path: '/root/second.txt', is_dir: false, size: 6, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        ]
    await route.fulfill({ json: { path, entries } })
  })
  await page.route('**/api/fs/details**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    await route.fulfill({ json: { path, name: path.split('/').pop() || 'Files', is_dir: path === '/' || path === '/root', is_symlink: false, size: 0, modified: '2000-01-01T00:00:00Z', mode: 'drwxr-xr-x', mode_octal: '0755', uid: 0, gid: 0, owner: 'root', group: 'root', size_status: 'ready' } })
  })

  await page.goto('/')
  await page.getByRole('button', { name: 'Open details' }).click()
  const detailsPane = page.locator('aside').filter({ has: page.getByRole('heading', { name: 'Details' }) })
  await expect(detailsPane.getByText('/', { exact: true })).toBeVisible()
  await detailsPane.getByRole('button', { name: 'Close details' }).click()

  await page.locator('[data-file-entry]').dblclick()
  const rows = page.locator('[data-file-entry]')
  await rows.nth(0).click()
  await page.getByRole('button', { name: 'Open details' }).click()
  await expect(detailsPane.getByText('/root/first.txt', { exact: true })).toBeVisible()

  await rows.nth(1).click()
  await expect(detailsPane.getByText('/root/second.txt', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Parent directory' }).click()
  await expect(detailsPane.getByText('/', { exact: true })).toBeVisible()
})

test('bulk checkbox selection does not replace row focus', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [
        { name: 'first.txt', path: '/first.txt', is_dir: false, size: 5, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        { name: 'second.txt', path: '/second.txt', is_dir: false, size: 6, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
      ],
    },
  }))

  await page.goto('/')
  const rows = page.locator('[data-file-entry]')
  await rows.nth(0).click()
  await showSelectionControls(page)
  await rows.nth(1).locator('[data-selection-control]').click()

  await expect(rows.nth(0)).toHaveClass(/outline-sky-500/)
  await expect(rows.nth(0)).toHaveClass(/webfile-focus-tint/)
  await expect(rows.nth(1)).toHaveClass(/outline-sky-500/)
  await expect(rows.nth(1)).toHaveClass(/webfile-selection-tint/)
  await expect(rows.nth(1)).not.toHaveClass(/webfile-focus-tint/)
  await expect(rows.nth(1).locator('[data-selection-control]')).toHaveAttribute('aria-pressed', 'true')

  await page.getByRole('button', { name: 'Clear selection' }).click()
  await expect(rows.nth(0)).toHaveClass(/outline-sky-500/)
  await expect(rows.nth(1).locator('[data-selection-control]')).toHaveAttribute('aria-pressed', 'false')
})

test('selection, row focus, and details state survive a browser refresh in the same tab', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [
        { name: 'first.txt', path: '/first.txt', is_dir: false, size: 5, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        { name: 'second.txt', path: '/second.txt', is_dir: false, size: 6, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
      ],
    },
  }))
  await page.route('**/api/fs/details**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? '/'
    await route.fulfill({ json: { path, name: path.split('/').pop() || 'Files', is_dir: false, is_symlink: false, size: 5, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--', mode_octal: '0644', uid: 0, gid: 0, owner: 'root', group: 'root', size_status: 'ready' } })
  })

  await page.goto('/')
  let rows = page.locator('[data-file-entry]')
  await rows.nth(0).click()
  await showSelectionControls(page)
  await rows.nth(1).locator('[data-selection-control]').click()
  await page.getByRole('button', { name: 'Open details' }).click()
  await expect(page.getByRole('heading', { name: 'Details' })).toBeVisible()

  await page.reload()

  rows = page.locator('[data-file-entry]')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toHaveClass(/outline-sky-500/)
  await expect(rows.nth(1).locator('[data-selection-control]')).toHaveAttribute('aria-pressed', 'true')
  const detailsPane = page.locator('aside').filter({ has: page.getByRole('heading', { name: 'Details' }) })
  await expect(detailsPane).toBeVisible()
  await expect(detailsPane.getByText('/first.txt', { exact: true })).toBeVisible()
})

test('the main pane uses generated thumbnails while full previews use the original image', async ({ page }) => {
  let thumbnailRequests = 0
  let originalRequests = 0
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [
        { name: 'photo.jpg', path: '/photo.jpg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        { name: 'guide.pdf', path: '/guide.pdf', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        { name: 'song.mp3', path: '/song.mp3', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
        { name: 'LICENSE', path: '/LICENSE', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
      ],
    },
  }))
  await page.route('**/api/fs/content-type**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    route.fulfill({ json: { path, content_type: path === '/photo.jpg' ? 'image/jpeg' : 'text/plain; charset=utf-8' } })
  })
  await page.route('**/api/fs/thumbnail?**', route => {
    thumbnailRequests++
    route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="1000"><rect width="100" height="1000" fill="red"/></svg>',
    })
  })
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: (() => {
      originalRequests++
      return '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="1000"><rect width="100" height="1000" fill="red"/></svg>'
    })(),
  }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Grid view' }).click()
  const rows = page.locator('[data-file-entry]')
  await expect(rows).toHaveCount(4)
  const thumbnail = rows.filter({ hasText: 'photo.jpg' }).locator('[data-file-thumbnail]')
  await expect(thumbnail).toBeVisible()
  const thumbnailBounds = await thumbnail.boundingBox()
  const tileBounds = await rows.filter({ hasText: 'photo.jpg' }).boundingBox()
  expect(thumbnailBounds).not.toBeNull()
  expect(tileBounds).not.toBeNull()
  expect(thumbnailBounds!.x).toBeGreaterThanOrEqual(tileBounds!.x)
  expect(thumbnailBounds!.y).toBeGreaterThanOrEqual(tileBounds!.y)
  expect(thumbnailBounds!.x + thumbnailBounds!.width).toBeLessThanOrEqual(tileBounds!.x + tileBounds!.width)
  expect(thumbnailBounds!.y + thumbnailBounds!.height).toBeLessThanOrEqual(tileBounds!.y + tileBounds!.height)
  expect(thumbnailBounds!.height).toBeGreaterThan(80)
  const pdfIcon = rows.filter({ hasText: 'guide.pdf' }).locator('.pi-file-pdf')
  await expect(pdfIcon).toBeVisible()
  expect(await pdfIcon.evaluate(element => Number.parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(60)
  await expect(rows.filter({ hasText: 'song.mp3' }).locator('.pi-volume-up')).toBeVisible()
  await expect(rows.filter({ hasText: 'LICENSE' }).locator('.pi-file-edit')).toBeVisible()
  expect(thumbnailRequests).toBe(1)
  expect(originalRequests).toBe(0)

  await rows.filter({ hasText: 'photo.jpg' }).dblclick()
  await expect(page.getByLabel('File preview')).toBeVisible()
  expect(originalRequests).toBeGreaterThan(0)
})

test('video tiles animate a cached sprite on hover without requesting the video', async ({ page }) => {
  let thumbnailRequests = 0
  let videoRequests = 0
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'clip.mp4', path: '/clip.mp4', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/thumbnail?**', route => {
    thumbnailRequests++
    route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="100"><rect width="1000" height="100" fill="#7c3aed"/></svg>',
    })
  })
  await page.route('**/api/fs/download**', route => {
    videoRequests++
    route.fulfill({ contentType: 'video/mp4', body: '' })
  })

  await page.goto('/')
  await page.getByRole('button', { name: 'Grid view' }).click()
  const sprite = page.locator('[data-video-thumbnail]')
  await expect(sprite).toBeVisible()
  await expect(sprite).toHaveAttribute('data-video-frame', '0')
  const frameWidth = await sprite.evaluate(element => element.clientWidth)
  expect(frameWidth).toBeGreaterThan(0)

  await sprite.hover()
  await expect(sprite).toHaveAttribute('data-video-frame', '1', { timeout: 750 })
  expect(await sprite.evaluate(element => getComputedStyle(element).backgroundPositionX)).toBe(`${-frameWidth}px`)
  await page.mouse.move(0, 0)
  await expect(sprite).toHaveAttribute('data-video-frame', '0')
  expect(await sprite.evaluate(element => getComputedStyle(element).backgroundPositionX)).toBe('0px')
  expect(thumbnailRequests).toBe(1)
  expect(videoRequests).toBe(0)
})

test('video thumbnails are batch queued in directory order before tile requests', async ({ page }) => {
  const orderedPaths = ['/first.mp4', '/second.mp4', '/third.mp4']
  let queuedPaths: string[] = []
  let batchComplete = false
  let tileRequestedBeforeBatch = false
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: orderedPaths.map((path, index) => ({
        name: path.slice(1),
        path,
        is_dir: false,
        size: index + 1,
        modified: '2000-01-01T00:00:00Z',
        mode: '-rw-r--r--',
      })),
    },
  }))
  await page.route('**/api/fs/thumbnails/queue', async route => {
    queuedPaths = (route.request().postDataJSON() as { paths: string[] }).paths
    await new Promise(resolve => setTimeout(resolve, 100))
    batchComplete = true
    await route.fulfill({ json: { jobs: [] } })
  })
  await page.route('**/api/fs/thumbnail?**', route => {
    if (!batchComplete) tileRequestedBeforeBatch = true
    return route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="100"><rect width="1000" height="100" fill="#7c3aed"/></svg>',
    })
  })

  await page.goto('/')
  await page.getByRole('button', { name: 'Grid view' }).click()
  await expect.poll(() => queuedPaths).toEqual(orderedPaths)
  await expect(page.locator('[data-video-thumbnail]')).toHaveCount(orderedPaths.length)
  expect(tileRequestedBeforeBatch).toBe(false)
})

test('directory invalidation keeps existing video thumbnails mounted while newly added videos are queued', async ({ page }) => {
  await page.addInitScript(() => {
    class MockWebSocket {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSING = 2
      static readonly CLOSED = 3
      readyState = MockWebSocket.OPEN
      onopen: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      onclose: ((event: CloseEvent) => void) | null = null

      constructor() {
        ;(window as typeof window & { __testSocket?: MockWebSocket }).__testSocket = this
        queueMicrotask(() => this.onopen?.(new Event('open')))
      }

      send() {}

      close() {
        this.readyState = MockWebSocket.CLOSED
      }
    }

    Object.defineProperty(window, 'WebSocket', { configurable: true, value: MockWebSocket })
  })

  const entry = (name: string) => ({
    name,
    path: `/${name}`,
    is_dir: false,
    size: 100,
    modified: '2000-01-01T00:00:00Z',
    mode: '-rw-r--r--',
  })
  let entries = [entry('first.mp4')]
  let batchCount = 0
  let releaseSecondBatch!: () => void
  const secondBatchReleased = new Promise<void>(resolve => { releaseSecondBatch = resolve })
  const thumbnailRequests = new Map<string, number>()

  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries } }))
  await page.route('**/api/fs/thumbnails/queue', async route => {
    batchCount++
    if (batchCount === 2) await secondBatchReleased
    await route.fulfill({ json: { jobs: [] } })
  })
  await page.route('**/api/fs/thumbnail?**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    thumbnailRequests.set(path, (thumbnailRequests.get(path) ?? 0) + 1)
    return route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="100"><rect width="1000" height="100" fill="#7c3aed"/></svg>',
    })
  })

  await page.goto('/')
  await page.getByRole('button', { name: 'Grid view' }).click()
  const firstThumbnail = page.locator('[data-file-entry]').filter({ hasText: 'first.mp4' }).locator('[data-video-thumbnail]')
  await expect(firstThumbnail).toBeVisible()
  await firstThumbnail.evaluate(element => { element.setAttribute('data-test-instance', 'original') })

  entries = [entry('first.mp4'), entry('second.mp4')]
  await page.evaluate(() => {
    const socket = (window as typeof window & {
      __testSocket?: { onmessage: ((event: MessageEvent) => void) | null }
    }).__testSocket
    socket?.onmessage?.(new MessageEvent('message', {
      data: JSON.stringify({ type: 'directory-invalidated', path: '/' }),
    }))
  })

  await expect.poll(() => batchCount).toBe(2)
  await expect(firstThumbnail).toHaveAttribute('data-test-instance', 'original')
  expect(thumbnailRequests.get('/first.mp4')).toBe(1)
  await expect(page.locator('[data-video-thumbnail]')).toHaveCount(1)

  releaseSecondBatch()
  await expect(page.locator('[data-video-thumbnail]')).toHaveCount(2)
  await expect(firstThumbnail).toHaveAttribute('data-test-instance', 'original')
  expect(thumbnailRequests.get('/first.mp4')).toBe(1)
})

test('a tall image is contained within both dimensions of the preview canvas', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'portrait.svg', path: '/portrait.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/portrait.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="1000"><rect width="100" height="1000" fill="red"/></svg>',
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const image = page.getByRole('img', { name: 'portrait.svg', exact: true })
  await expect(image).toBeVisible()

  const dimensions = await image.evaluate(element => {
    const imageElement = element as HTMLImageElement
    const parent = imageElement.parentElement!
    return {
      imageWidth: imageElement.clientWidth,
      imageHeight: imageElement.clientHeight,
      parentWidth: parent.clientWidth,
      parentHeight: parent.clientHeight,
      objectFit: getComputedStyle(imageElement).objectFit,
    }
  })
  expect(dimensions.objectFit).toBe('contain')
  expect(dimensions.imageWidth).toBeLessThanOrEqual(dimensions.parentWidth)
  expect(dimensions.imageHeight).toBeLessThanOrEqual(dimensions.parentHeight)
})

test('an image is constrained and hidden until its fitted size is ready', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'delayed.svg', path: '/delayed.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/delayed.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', async route => {
    await new Promise(resolve => setTimeout(resolve, 750))
    await route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="3000"><rect width="4000" height="3000" fill="red"/></svg>',
    })
  })

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const image = page.locator('img[alt="delayed.svg"]')
  await expect(image).toBeAttached()
  await expect(image).toHaveAttribute('data-image-loaded', 'false')
  await expect(page.getByLabel('Loading image')).toBeVisible()
  const initialStyle = await image.evaluate(element => ({
    maxWidth: (element as HTMLImageElement).style.maxWidth,
    maxHeight: (element as HTMLImageElement).style.maxHeight,
    visibility: (element as HTMLImageElement).style.visibility,
  }))
  expect(initialStyle).toEqual({ maxWidth: '100%', maxHeight: '100%', visibility: 'hidden' })

  await expect(image).toHaveAttribute('data-image-loaded', 'true')
  await expect(image).toBeVisible()
  await expect(page.getByLabel('Loading image')).toHaveCount(0)
})

test('image gallery reserves double tap for zoom and keeps immersive mode explicit', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'touch.svg', path: '/touch.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/touch.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="2400" height="1600"><rect width="2400" height="1600" fill="red"/></svg>',
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const preview = page.getByLabel('File preview')
  const viewport = page.locator('[data-image-viewport]')
  await expect(preview.getByRole('img', { name: 'touch.svg', exact: true })).toBeVisible()
  await expect(preview).toHaveAttribute('data-immersive', 'false')
  await expect.poll(() => viewport.evaluate(element => getComputedStyle(element).touchAction)).toBe('none')
  await expect.poll(() => preview.evaluate(element => getComputedStyle(element).touchAction)).toBe('pan-x pan-y')
  const thumbnailStrip = preview.locator('[data-thumbnail-strip] [role="listbox"]')
  await expect.poll(() => thumbnailStrip.evaluate(element => getComputedStyle(element).touchAction)).toBe('pan-x')
  await expect.poll(() => thumbnailStrip.evaluate(element => {
    const thumbnail = element.querySelector<HTMLElement>('[role="option"]')
    return thumbnail ? element.clientHeight >= thumbnail.offsetHeight : false
  })).toBe(true)

  await touchPoint(page, viewport)
  await touchPoint(page, viewport)
  await expect(page.getByLabel('Zoom level')).toHaveText('100%')
  await expect(preview).toHaveAttribute('data-immersive', 'false')

  await page.getByRole('button', { name: 'Enter fullscreen' }).click()
  await expect(preview).toHaveAttribute('data-immersive', 'true')
})

test('image gallery preloads neighbours and supports swipe, pinch, and explicit fullscreen controls', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Multi-touch gesture injection requires Chromium CDP')
  const requestedImages = new Set<string>()
  const entries = Array.from({ length: 12 }, (_, index) => `image-${String(index + 1).padStart(2, '0')}.svg`).map((name, index) => ({
    name,
    path: `/${name}`,
    is_dir: false,
    size: 100 + index,
    modified: '2000-01-01T00:00:00Z',
    mode: '-rw-r--r--',
  }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list', show_hidden: true, theme: 'system' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries } }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/image-06.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    requestedImages.add(path)
    return route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="1200"><rect width="1600" height="1200" fill="red"/></svg>',
    })
  })

  await page.goto('/')
  await page.locator('[data-file-entry]').filter({ hasText: 'image-06.svg' }).dblclick()
  const preview = page.getByLabel('File preview')
  await expect(preview.getByRole('img', { name: 'image-06.svg', exact: true })).toBeVisible()
  await expect(preview.getByRole('button', { name: 'Download' })).toHaveCount(0)
  await expect.poll(() => [...requestedImages].sort()).toEqual(Array.from({ length: 11 }, (_, index) => `/image-${String(index + 1).padStart(2, '0')}.svg`))

  const viewport = page.locator('[data-image-viewport]')
  const box = await viewport.boundingBox()
  if (!box) throw new Error('Image viewport is not visible')
  const centerX = box.x + box.width / 2
  const centerY = box.y + box.height / 2
  const session = await page.context().newCDPSession(page)
  const tap = async (x: number, y: number) => {
    await session.send('Input.dispatchTouchEvent', {
      type: 'touchStart',
      touchPoints: [{ x, y, radiusX: 2, radiusY: 2, force: 1 }],
    })
    await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  }
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x: centerX + 80, y: centerY, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchMove',
    touchPoints: [{ x: centerX - 20, y: centerY, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  const galleryTrack = page.locator('[data-gallery-track]')
  await expect(galleryTrack).toHaveAttribute('data-gallery-animating', 'true')
  await expect(preview.getByRole('img', { name: 'image-06.svg', exact: true })).toBeVisible()
  await galleryTrack.evaluate(element => {
    element.dispatchEvent(new TransitionEvent('transitionend', { propertyName: 'transform' }))
  })
  await expect(preview.getByRole('img', { name: 'image-07.svg', exact: true })).toBeVisible({ timeout: 100 })

  await expect(preview.getByRole('img', { name: 'image-07.svg', exact: true })).toBeVisible()
  await expect.poll(() => [...requestedImages].sort()).toEqual(Array.from({ length: 12 }, (_, index) => `/image-${String(index + 1).padStart(2, '0')}.svg`))

  const zoomBefore = Number.parseInt((await page.getByLabel('Zoom level').innerText()).replace('%', ''), 10)
  const focalX = box.x + box.width * 0.7
  await tap(focalX, centerY)
  await tap(focalX, centerY)
  await expect(page.getByLabel('Zoom level')).toHaveText('100%')
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)

  await tap(focalX, centerY)
  await tap(focalX, centerY)
  await expect(page.getByLabel('Zoom level')).toHaveText(`${zoomBefore}%`)
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBe(0)

  await session.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [
      { x: centerX - 30, y: centerY, radiusX: 2, radiusY: 2, force: 1 },
      { x: centerX + 30, y: centerY, radiusX: 2, radiusY: 2, force: 1 },
    ],
  })
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchMove',
    touchPoints: [
      { x: centerX - 90, y: centerY, radiusX: 2, radiusY: 2, force: 1 },
      { x: centerX + 90, y: centerY, radiusX: 2, radiusY: 2, force: 1 },
    ],
  })
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await expect.poll(async () => Number.parseInt((await page.getByLabel('Zoom level').innerText()).replace('%', ''), 10)).toBeGreaterThan(zoomBefore)

  await page.getByRole('button', { name: 'Enter fullscreen' }).click()
  await expect(preview).toHaveAttribute('data-immersive', 'true')
  await expect(page.getByRole('button', { name: 'Close preview' })).toBeHidden()
  await expect(page.getByRole('button', { name: 'Exit fullscreen' })).toBeVisible()
  const fullscreenControls = page.getByRole('navigation', { name: 'Fullscreen image controls' })
  await expect(fullscreenControls).toBeVisible()
  const controlsBox = await fullscreenControls.boundingBox()
  if (!controlsBox) throw new Error('Fullscreen image controls are not visible')
  const controlsX = controlsBox.x + controlsBox.width / 2
  const controlsStartY = controlsBox.y + controlsBox.height - 4
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x: controlsX, y: controlsStartY, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchMove',
    touchPoints: [{ x: controlsX, y: controlsStartY - 44, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await expect(fullscreenControls).toBeHidden()
  const showFullscreenControls = page.getByRole('button', { name: 'Show fullscreen image controls' })
  await expect(showFullscreenControls).toBeVisible()
  await showFullscreenControls.click()
  await expect(fullscreenControls).toBeVisible()
  await fullscreenControls.getByRole('button', { name: 'Fit to window' }).click()
  await expect.poll(() => viewport.evaluate(element => element.scrollHeight === element.clientHeight)).toBe(true)
  const fullscreenViewport = await viewport.boundingBox()
  if (!fullscreenViewport) throw new Error('Fullscreen image viewport is not visible')
  const fullscreenX = fullscreenViewport.x + fullscreenViewport.width / 2
  const fullscreenY = fullscreenViewport.y + fullscreenViewport.height / 2

  await page.mouse.move(fullscreenViewport.x + fullscreenViewport.width * 0.75, fullscreenY)
  await page.mouse.down()
  await page.mouse.move(fullscreenViewport.x + fullscreenViewport.width * 0.25, fullscreenY, { steps: 6 })
  await page.mouse.up()
  await expect(preview.getByRole('img', { name: 'image-08.svg', exact: true })).toBeVisible()

  await fullscreenControls.getByRole('button', { name: 'Actual size' }).click()
  await expect(fullscreenControls.getByLabel('Zoom level')).toHaveText('100%')
  await page.mouse.move(fullscreenX, fullscreenY)
  await page.mouse.down()
  await page.mouse.move(fullscreenX - 140, fullscreenY, { steps: 4 })
  await page.mouse.up()
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)

  await fullscreenControls.getByRole('button', { name: 'Fit to window' }).click()
  await expect.poll(() => viewport.evaluate(element => element.scrollLeft)).toBe(0)
  await tap(fullscreenX, fullscreenY)
  await expect(preview.locator('[data-thumbnail-strip]')).toBeHidden()
  await tap(fullscreenX, fullscreenY)
  await expect(preview.locator('[data-thumbnail-strip]')).toBeVisible()
  await page.getByRole('button', { name: 'Exit fullscreen' }).click()
  await expect(preview).toHaveAttribute('data-immersive', 'false')
  await expect(page.getByRole('button', { name: 'Back to folder' })).toBeVisible()
  await session.detach()
})

test('image gallery tracks a swipe and progressively loads an ordered thumbnail strip', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'Touch-drag gesture injection requires Chromium CDP')
  const entries = Array.from({ length: 40 }, (_, index) => `gallery-${String(index + 1).padStart(2, '0')}.svg`).map(name => ({
    name,
    path: `/${name}`,
    is_dir: false,
    size: 100,
    modified: '2000-01-01T00:00:00Z',
    mode: '-rw-r--r--',
  }))
  const thumbnailRequests: string[] = []
  let activeThumbnails = 0
  let maximumActiveThumbnails = 0
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list', show_hidden: true, theme: 'system' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries } }))
  await page.route('**/api/fs/content-type**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    return route.fulfill({ json: { path, content_type: 'image/png' } })
  })
  await page.route('**/api/fs/thumbnail?**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    thumbnailRequests.push(path)
    activeThumbnails++
    maximumActiveThumbnails = Math.max(maximumActiveThumbnails, activeThumbnails)
    await new Promise(resolve => setTimeout(resolve, 25))
    activeThumbnails--
    await route.fulfill({
      contentType: 'image/svg+xml',
      headers: { 'Cache-Control': 'public, max-age=3600' },
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="120"><rect width="160" height="120" fill="#10b981"/></svg>',
    })
  })
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    headers: { 'Cache-Control': 'public, max-age=3600' },
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="1200"><rect width="1600" height="1200" fill="#334155"/></svg>',
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').filter({ hasText: 'gallery-20.svg' }).dblclick()
  const preview = page.getByLabel('File preview')
  const viewport = preview.locator('[data-image-viewport]')
  const track = preview.locator('[data-gallery-track]')
  await expect(preview.getByRole('img', { name: 'gallery-20.svg', exact: true })).toBeVisible()
  await expect(preview.getByRole('option')).toHaveCount(40)
  const initialThumbnail = preview.getByRole('option', { name: 'Show gallery-20.svg' })
  await expect(initialThumbnail).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => initialThumbnail.evaluate(element => {
    const item = element.getBoundingClientRect()
    const strip = element.parentElement!.getBoundingClientRect()
    return Math.abs(item.left + item.width / 2 - (strip.left + strip.width / 2))
  })).toBeLessThanOrEqual(1)

  const thumbnailStrip = preview.getByRole('listbox', { name: 'Image thumbnails' })
  await expect.poll(() => thumbnailStrip.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true)
  await thumbnailStrip.evaluate(element => { element.scrollLeft = 0 })
  const thumbnailStripBox = await thumbnailStrip.boundingBox()
  if (!thumbnailStripBox) throw new Error('Thumbnail strip is not visible')
  await page.mouse.move(thumbnailStripBox.x + thumbnailStripBox.width / 2, thumbnailStripBox.y + thumbnailStripBox.height / 2)
  await page.mouse.wheel(0, 480)
  await expect.poll(() => thumbnailStrip.evaluate(element => element.scrollLeft)).toBeGreaterThan(0)

  const box = await viewport.boundingBox()
  if (!box) throw new Error('Image viewport is not visible')
  const initialTransform = await track.evaluate(element => getComputedStyle(element).transform)
  const session = await page.context().newCDPSession(page)
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x: box.x + box.width * 0.7, y: box.y + box.height / 2, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await session.send('Input.dispatchTouchEvent', {
    type: 'touchMove',
    touchPoints: [{ x: box.x + box.width * 0.45, y: box.y + box.height / 2, radiusX: 2, radiusY: 2, force: 1 }],
  })
  await expect.poll(() => track.evaluate(element => getComputedStyle(element).transform)).not.toBe(initialTransform)
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await expect.poll(async () => Number(await track.getAttribute('data-gallery-transition-ms'))).toBeGreaterThanOrEqual(300)
  await expect.poll(async () => Number.parseFloat(await track.evaluate(element => getComputedStyle(element).transitionDuration)) * 1000).toBeGreaterThanOrEqual(300)
  expect(await preview.getByRole('option', { name: 'Show gallery-21.svg' }).getAttribute('aria-selected')).toBe('true')
  await expect(preview.getByText('21 / 40')).toBeVisible()
  await expect(preview.getByRole('img', { name: 'gallery-21.svg', exact: true })).toBeVisible()
  await expect(preview.getByRole('option', { name: 'Show gallery-21.svg' })).toHaveAttribute('aria-selected', 'true')
  await session.detach()

  await expect.poll(() => new Set(thumbnailRequests).size, { timeout: 10_000 }).toBe(40)
  expect(thumbnailRequests[0]).toBe('/gallery-20.svg')
  expect([...new Set(thumbnailRequests)]).toEqual([
    '/gallery-20.svg',
    ...entries.filter(entry => entry.path !== '/gallery-20.svg').map(entry => entry.path),
  ])
  expect(maximumActiveThumbnails).toBeLessThanOrEqual(4)
  await expect(preview.getByText('40 / 40 thumbnails')).toBeVisible()

  await preview.getByRole('option', { name: 'Show gallery-35.svg' }).click()
  await expect(preview.getByRole('img', { name: 'gallery-35.svg', exact: true })).toBeVisible()
  const laterThumbnail = preview.getByRole('option', { name: 'Show gallery-35.svg' })
  await expect(laterThumbnail).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => laterThumbnail.evaluate(element => {
    const item = element.getBoundingClientRect()
    const strip = element.parentElement!.getBoundingClientRect()
    return Math.abs(item.left + item.width / 2 - (strip.left + strip.width / 2))
  })).toBeLessThanOrEqual(1)
  const preservedScrollLeft = await thumbnailStrip.evaluate(element => element.scrollLeft)
  await preview.getByRole('button', { name: 'Hide thumbnails' }).click()
  await expect(preview.locator('[data-thumbnail-strip]')).toBeHidden()
  await expect(preview.locator('[aria-label="Image thumbnails"]')).toHaveCount(1)
  await preview.getByRole('button', { name: 'Show thumbnails' }).click()
  await expect(preview.locator('[data-thumbnail-strip]')).toBeVisible()
  await expect.poll(() => thumbnailStrip.evaluate(element => element.scrollLeft)).toBe(preservedScrollLeft)
})

test('fit mode responds to gallery viewport growth', async ({ page }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'wide.svg', path: '/wide.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/wide.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/thumbnail?**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90"/>',
  }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="2400" height="1200"><rect width="2400" height="1200" fill="navy"/></svg>',
  }))

  await page.setViewportSize({ width: 800, height: 600 })
  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const image = page.getByRole('img', { name: 'wide.svg', exact: true })
  await expect(image).toBeVisible()
  const initialWidth = await image.evaluate(element => element.getBoundingClientRect().width)
  await page.setViewportSize({ width: 1200, height: 850 })
  await expect.poll(() => image.evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(initialWidth)
})

test('gallery accepts consecutive swipes and reversals before animations finish', async ({ page, browserName }) => {
  await page.route('**/api/preferences', route => route.fulfill({ json: { sort_field: 'name', sort_direction: 'asc' } }))
  const entries = Array.from({ length: 5 }, (_, index) => ({
    name: `rapid-${index + 1}.svg`, path: `/rapid-${index + 1}.svg`, is_dir: false,
    size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--',
  }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries } }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'image/png' } }))
  await page.route(/\/api\/fs\/(download|thumbnail)\?/, route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"><rect width="800" height="600" fill="teal"/></svg>',
  }))
  await page.goto('/')
  await page.locator('[data-file-entry]').filter({ hasText: 'rapid-2.svg' }).dblclick()
  const preview = page.getByLabel('File preview')
  const track = preview.locator('[data-gallery-track]')
  const surface = preview.locator('[data-gallery-gesture-surface]')
  await expect(preview.getByRole('img', { name: 'rapid-2.svg', exact: true })).toHaveAttribute('data-image-loaded', 'true')
  await expect(preview.locator('[data-gallery-adjacent="next"]')).toHaveCSS('visibility', 'visible')
  const box = await surface.boundingBox()
  if (!box) throw new Error('Gallery gesture surface is not visible')
  const x = box.x + box.width / 2
  const y = box.y + box.height / 2
  // Keep both CSS completion and the fallback timers from finishing between
  // gestures, so this exercises interruption rather than ordinary navigation.
  await page.addStyleTag({ content: '[data-gallery-track][data-gallery-animating="true"] { transition-duration: 10s !important; }' })
  await page.clock.install()
  await page.clock.pauseAt(new Date(Date.now() + 100))
  const session = browserName === 'chromium' ? await page.context().newCDPSession(page) : null
  const swipe = async (direction: -1 | 1) => {
    if (session) {
      await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
      await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x + direction * 180, y }] })
      await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    } else {
      await page.mouse.move(x, y)
      await page.mouse.down()
      await page.mouse.move(x + direction * 180, y, { steps: 3 })
      await page.mouse.up()
    }
    await page.clock.runFor(32)
  }
  await swipe(-1)
  await expect(track).toHaveAttribute('data-gallery-animating', 'true')
  await expect(preview.getByRole('option', { name: 'Show rapid-3.svg' })).toHaveAttribute('aria-selected', 'true')
  await swipe(-1)
  await expect(track).toHaveAttribute('data-gallery-animating', 'true')
  await expect(preview.getByRole('option', { name: 'Show rapid-4.svg' })).toHaveAttribute('aria-selected', 'true')
  await swipe(1)
  await expect(track).toHaveAttribute('data-gallery-animating', 'true')
  await expect(preview.getByRole('option', { name: 'Show rapid-3.svg' })).toHaveAttribute('aria-selected', 'true')
  await page.clock.runFor(1000)
  await expect(preview.getByRole('img', { name: 'rapid-3.svg', exact: true })).toBeVisible()
  await expect(track).toHaveAttribute('data-gallery-animating', 'false')
  // Interrupt an edge snap-back too: its old timeout must not stop the next slide.
  await preview.getByRole('option', { name: 'Show rapid-1.svg' }).click()
  await expect(preview.getByRole('img', { name: 'rapid-1.svg', exact: true })).toHaveAttribute('data-image-loaded', 'true')
  await swipe(1)
  await expect(track).toHaveAttribute('data-gallery-animating', 'true')
  await swipe(-1)
  await expect(preview.getByRole('option', { name: 'Show rapid-2.svg' })).toHaveAttribute('aria-selected', 'true')
  await page.clock.runFor(1000)
  await expect(preview.getByRole('img', { name: 'rapid-2.svg', exact: true })).toBeVisible()
  await session?.detach()
  let releaseInspection!: () => void
  const inspectionGate = new Promise<void>(resolve => { releaseInspection = resolve })
  await page.route('**/api/fs/content-type**', async route => {
    if (new URL(route.request().url()).searchParams.get('path') === '/rapid-3.svg') await inspectionGate
    await route.fulfill({ json: { content_type: 'image/png' } })
  })
  const pendingInspection = page.waitForRequest(request => request.url().includes('/api/fs/content-type') && new URL(request.url()).searchParams.get('path') === '/rapid-3.svg')
  await preview.getByRole('option', { name: 'Show rapid-3.svg' }).click()
  await page.clock.runFor(1000)
  await pendingInspection
  await preview.getByRole('option', { name: 'Show rapid-5.svg' }).click()
  await expect(preview.getByRole('img', { name: 'rapid-5.svg', exact: true })).toBeVisible()
  const staleResponse = page.waitForResponse(response => response.url().includes('/api/fs/content-type') && new URL(response.url()).searchParams.get('path') === '/rapid-3.svg')
  releaseInspection()
  await (await staleResponse).finished()
  await page.clock.runFor(32)
  await expect(preview.getByRole('img', { name: 'rapid-5.svg', exact: true })).toBeVisible()
  await expect(page).toHaveURL(/file=rapid-5\.svg/)
})

test('gallery navigation keeps the decoded target constrained throughout the transition', async ({ page }) => {
  await page.route('**/api/preferences', route => route.fulfill({ json: { sort_field: 'name', sort_direction: 'asc' } }))
  const entries = [
    { name: 'landscape.svg', path: '/landscape.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
    { name: 'portrait-next.svg', path: '/portrait-next.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries } }))
  await page.route('**/api/fs/content-type**', route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    return route.fulfill({ json: { path, content_type: 'image/png' } })
  })
  await page.route('**/api/fs/thumbnail?**', route => route.fulfill({
    contentType: 'image/svg+xml',
    headers: { 'Cache-Control': 'public, max-age=3600' },
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="120"/>',
  }))
  await page.route('**/api/fs/download**', async route => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    if (path === '/portrait-next.svg') await new Promise(resolve => setTimeout(resolve, 120))
    await route.fulfill({
      contentType: 'image/svg+xml',
      headers: { 'Cache-Control': 'public, max-age=3600' },
      body: path === '/portrait-next.svg'
        ? '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1600"><rect width="800" height="1600" fill="teal"/></svg>'
        : '<svg xmlns="http://www.w3.org/2000/svg" width="2000" height="1000"><rect width="2000" height="1000" fill="navy"/></svg>',
    })
  })

  await page.goto('/')
  await page.locator('[data-file-entry]').filter({ hasText: 'landscape.svg' }).dblclick()
  await expect(page.getByRole('img', { name: 'landscape.svg', exact: true })).toBeVisible()
  const incomingImage = page.locator('[data-gallery-adjacent="next"]')
  await expect(incomingImage).toHaveCSS('visibility', 'visible')
  await expect(incomingImage).toHaveCSS('max-width', 'none')
  const incomingBounds = await incomingImage.boundingBox()
  if (!incomingBounds) throw new Error('Incoming carousel image is not visible')
  await page.evaluate(() => {
    const frames: Array<{ loaded: string | null; visibility: string; width: number; height: number; viewportWidth: number; viewportHeight: number }> = []
    const sample = () => {
      const viewport = document.querySelector<HTMLElement>('[data-image-viewport]')
      const image = viewport?.querySelector<HTMLImageElement>('img[data-image-loaded]')
      if (!viewport || !image) return
      const bounds = image.getBoundingClientRect()
      frames.push({
        loaded: image.getAttribute('data-image-loaded'),
        visibility: getComputedStyle(image).visibility,
        width: bounds.width,
        height: bounds.height,
        viewportWidth: viewport.clientWidth,
        viewportHeight: viewport.clientHeight,
      })
    }
    const interval = window.setInterval(sample, 8)
    const image = document.querySelector<HTMLImageElement>('[data-image-viewport] img[data-image-loaded]')
    const observer = new MutationObserver(sample)
    if (image) observer.observe(image, { attributes: true, attributeFilter: ['src', 'style', 'data-image-loaded'] })
    ;(window as unknown as { galleryTransitionCapture: { frames: typeof frames; interval: number; observer: MutationObserver } }).galleryTransitionCapture = { frames, interval, observer }
    sample()
  })

  await page.getByRole('button', { name: 'Next image' }).click()
  const centeredImage = page.getByRole('img', { name: 'portrait-next.svg', exact: true })
  await expect(centeredImage).toBeVisible()
  const centeredBounds = await centeredImage.boundingBox()
  if (!centeredBounds) throw new Error('Centered carousel image is not visible')
  expect(Math.abs(centeredBounds.width - incomingBounds.width)).toBeLessThanOrEqual(1)
  expect(Math.abs(centeredBounds.height - incomingBounds.height)).toBeLessThanOrEqual(1)
  await page.waitForTimeout(300)
  const frames = await page.evaluate(() => {
    const capture = (window as unknown as { galleryTransitionCapture: { frames: Array<{ loaded: string | null; visibility: string; width: number; height: number; viewportWidth: number; viewportHeight: number }>; interval: number; observer: MutationObserver } }).galleryTransitionCapture
    clearInterval(capture.interval)
    capture.observer.disconnect()
    return capture.frames
  })
  expect(frames.length).toBeGreaterThan(5)
  expect(frames.every(frame => frame.loaded === 'true')).toBe(true)
  expect(frames.every(frame => frame.visibility === 'visible')).toBe(true)
  expect(frames.every(frame => frame.width > 0 && frame.width <= frame.viewportWidth)).toBe(true)
  expect(frames.every(frame => frame.height > 0 && frame.height <= frame.viewportHeight)).toBe(true)
})

test('a small image keeps its natural size until the user zooms it', async ({ page, browserName }) => {
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'small.svg', path: '/small.svg', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/small.svg', content_type: 'image/png' } }))
  await page.route('**/api/fs/download**', route => route.fulfill({
    contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30"><rect width="40" height="30" fill="red"/></svg>',
  }))

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const image = page.getByRole('img', { name: 'small.svg', exact: true })
  await expect(image).toBeVisible()
  await expect(page.getByLabel('Zoom level')).toHaveText('100%')
  await expect(image).toHaveCSS('width', '40px')
  await expect(image).toHaveCSS('height', '30px')

  await page.getByRole('button', { name: 'Zoom in' }).click()
  await expect(page.getByLabel('Zoom level')).toHaveText('125%')
  await expect(image).toHaveCSS('width', '50px')

  await page.getByRole('button', { name: 'Fit to window' }).click()
  await expect(page.getByLabel('Zoom level')).toHaveText('100%')
  await expect(image).toHaveCSS('width', '40px')

  if (browserName === 'chromium') {
    await image.hover()
    await page.mouse.wheel(0, -100)
    await expect(page.getByLabel('Zoom level')).toHaveText('110%')
    await expect(image).toHaveCSS('width', '44px')

    await page.mouse.wheel(0, 100)
    await expect(page.getByLabel('Zoom level')).toHaveText('100%')
    await expect(image).toHaveCSS('width', '40px')
  }
})

test('video preview uses a contained native player with controls', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(Element.prototype, 'requestFullscreen', {
      configurable: true,
      value: () => Promise.reject(new DOMException('Fullscreen unavailable', 'NotSupportedError')),
    })
  })
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{ name: 'movie.mp4', path: '/movie.mp4', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
    },
  }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { path: '/movie.mp4', content_type: 'video/mp4' } }))
  await page.route('**/api/fs/download**', async route => {
    await new Promise(resolve => setTimeout(resolve, 1500))
    await route.abort()
  })

  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
  const video = page.locator('video')
  await expect(video).toBeVisible()

  const player = await video.evaluate(element => {
    const videoElement = element as HTMLVideoElement
    const parent = videoElement.parentElement!
    return {
      controls: videoElement.controls,
      playsInline: videoElement.playsInline,
      preload: videoElement.preload,
      objectFit: getComputedStyle(videoElement).objectFit,
      width: videoElement.clientWidth,
      height: videoElement.clientHeight,
      parentWidth: parent.clientWidth,
      parentHeight: parent.clientHeight,
    }
  })
  expect(player.controls).toBe(true)
  expect(player.playsInline).toBe(true)
  expect(player.preload).toBe('metadata')
  expect(player.objectFit).toBe('contain')
  expect(player.width).toBeLessThanOrEqual(player.parentWidth)
  expect(player.height).toBeLessThanOrEqual(player.parentHeight)

  const preview = page.getByLabel('File preview')
  await page.getByRole('button', { name: 'Enter fullscreen' }).click()
  await expect(preview).toHaveAttribute('data-immersive', 'true')
  await expect(page.getByRole('button', { name: 'Exit fullscreen' })).toBeVisible()
  await video.dispatchEvent('error')
  await expect(page.getByRole('heading', { name: 'Preview unavailable' })).toBeVisible()
  await expect(preview).toHaveAttribute('data-immersive', 'false')
  await expect(page.getByRole('button', { name: 'Back to folder' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enter fullscreen' })).toHaveCount(0)
  await page.goBack()
  await expect(preview).toHaveCount(0)
  await page.goForward()
  await expect(preview).toHaveAttribute('data-immersive', 'false')
  await expect(page.getByRole('button', { name: 'Back to folder' })).toBeVisible()
})

test('repeated terminal operation updates announce completion only once', async ({ page }) => {
  await page.addInitScript(() => {
    class MockWebSocket {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSING = 2
      static readonly CLOSED = 3
      readyState = MockWebSocket.OPEN
      onopen: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      onclose: ((event: CloseEvent) => void) | null = null

      constructor() {
        ;(window as typeof window & { __testSocket?: MockWebSocket }).__testSocket = this
        queueMicrotask(() => this.onopen?.(new Event('open')))
      }

      send() {}

      close() {
        this.readyState = MockWebSocket.CLOSED
      }
    }

    Object.defineProperty(window, 'WebSocket', { configurable: true, value: MockWebSocket })
  })
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs**', route => route.fulfill({ json: { jobs: [] } }))

  await page.goto('/')
  await page.waitForFunction(() => Boolean((window as typeof window & { __testSocket?: unknown }).__testSocket))

  const operation = {
    id: 'delete-completed',
    kind: 'delete',
    sources: ['/folder'],
    state: 'completed',
    message: 'Deleted 1 item',
    progress_done: 1,
    progress_total: 1,
    progress_unit: 'items',
    bytes_done: 0,
    bytes_total: 0,
    items_done: 1,
    items_total: 1,
    cancellable: false,
    started_at: '2000-01-01T00:00:00Z',
    updated_at: '2000-01-01T00:00:01Z',
    finished_at: '2000-01-01T00:00:01Z',
  }
  await page.evaluate(completedOperation => {
    const socket = (window as typeof window & {
      __testSocket?: { onmessage: ((event: MessageEvent) => void) | null }
    }).__testSocket
    for (let index = 0; index < 100; index += 1) {
      socket?.onmessage?.(new MessageEvent('message', {
        data: JSON.stringify({ type: 'operation-updated', operation: completedOperation }),
      }))
    }
  }, operation)

  await expect(page.getByText('Delete complete', { exact: true })).toHaveCount(1)
})

test('task drawer separates active work from persistent history', async ({ page }) => {
  let clearHistoryCalled = false
  let removedHistoryID = ''
  const operations = [
    {
      id: 'copy-active',
      kind: 'copy',
      sources: ['/source/photos'],
      destination: '/target',
      state: 'running',
      phase: 'copying',
      message: 'Copying photos',
      progress_done: 262144,
      progress_total: 1048576,
      progress_unit: 'bytes',
      bytes_done: 262144,
      bytes_total: 1048576,
      bytes_per_second: 90492134,
      items_done: 1,
      items_total: 4,
      cancellable: true,
      cleanup_policy: 'Remove files created by this task',
      started_at: '2000-01-01T00:00:00Z',
      updated_at: '2000-01-01T00:00:01Z',
    },
    {
      id: 'extract-interrupted',
      kind: 'extract',
      sources: ['/source/archive.zip'],
      destination: '/target/archive',
      state: 'interrupted',
      message: 'Interrupted by restart',
      progress_done: 0,
      progress_total: 0,
      progress_unit: '',
      bytes_done: 0,
      bytes_total: 0,
      items_done: 0,
      items_total: 0,
      cancellable: false,
      cleanup_policy: 'Remove files created by this task',
      started_at: '2000-01-01T00:00:00Z',
      updated_at: '2000-01-01T00:00:02Z',
      finished_at: '2000-01-01T00:00:02Z',
    },
    {
      id: 'move-completed',
      kind: 'move',
      sources: ['/source/document.txt'],
      destination: '/target',
      state: 'completed',
      message: 'Moved',
      progress_done: 100,
      progress_total: 100,
      progress_unit: 'bytes',
      bytes_done: 100,
      bytes_total: 100,
      items_done: 1,
      items_total: 1,
      cancellable: false,
      started_at: '2000-01-01T00:00:00Z',
      updated_at: '2000-01-01T00:00:03Z',
      finished_at: '2000-01-01T00:00:03Z',
    },
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations/history', route => {
    clearHistoryCalled = true
    return route.fulfill({ json: { removed: 1 } })
  })
  await page.route('**/api/fs/operations/extract-interrupted', route => {
    removedHistoryID = 'extract-interrupted'
    return route.fulfill({ json: { removed: true } })
  })
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations } }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks' }).click()

  const drawer = page.getByRole('complementary')
  await expect(drawer.getByText('Active', { exact: true })).toBeVisible()
  await expect(drawer.getByText('History', { exact: true })).toBeVisible()
  await expect(drawer.getByRole('button', { name: 'Collapse Active (1)' })).toBeVisible()
  const historyToggle = drawer.getByRole('button', { name: 'Collapse History (2)' })
  await expect(historyToggle).toBeVisible()
  await historyToggle.click()
  await expect(drawer.getByText('Extract archive.zip')).toHaveCount(0)
  await drawer.getByRole('button', { name: 'Expand History (2)' }).click()
  await expect(drawer.getByText('Extract archive.zip')).toBeVisible()
  await expect(drawer.getByText('Copying photos')).toBeVisible()
  await expect(drawer.getByRole('progressbar', { name: 'Copy photos progress' })).toHaveAttribute('aria-valuenow', '25')
  await expect(drawer.getByText(/86\.3 MiB\/s/)).toBeVisible()
  const activeTask = drawer.getByRole('article').filter({ hasText: 'Copy photos' })
  await expect(activeTask.getByText('Remove files created by this task')).toHaveCount(0)
  await activeTask.getByRole('button', { name: 'Cancel' }).click()
  const cancelConfirmation = page.getByRole('alertdialog', { name: 'Cancel Copy photos?' })
  await expect(cancelConfirmation).toContainText('Remove files created by this task')
  await cancelConfirmation.getByRole('button', { name: 'Continue running' }).click()
  await expect(cancelConfirmation).toHaveCount(0)
  await expect(activeTask).toBeVisible()
  await expect(drawer.getByText('interrupted', { exact: true })).toBeVisible()

  await page.reload()
  await page.getByRole('button', { name: 'Open tasks' }).click()
  const reloadedDrawer = page.getByRole('complementary')
  await expect(reloadedDrawer.getByText('interrupted', { exact: true })).toBeVisible()
  const interruptedTask = reloadedDrawer.getByRole('article').filter({ hasText: 'Extract archive.zip' })
  await interruptedTask.getByText('Details', { exact: true }).click()
  await expect(interruptedTask.getByText('Interrupted by restart')).toBeVisible()
  await expect(interruptedTask.getByText('Remove files created by this task')).toHaveCount(0)

  await interruptedTask.getByRole('button', { name: 'Remove Extract archive.zip from history' }).click()
  const removeConfirmation = page.getByRole('alertdialog', { name: 'Remove task from history' })
  await expect(removeConfirmation).toContainText('does not affect any files')
  await removeConfirmation.getByRole('button', { name: 'Remove', exact: true }).click()

  await expect.poll(() => removedHistoryID).toBe('extract-interrupted')
  await expect(reloadedDrawer.getByText('Extract archive.zip')).toHaveCount(0)
  await expect(reloadedDrawer.getByText('Move document.txt')).toBeVisible()
  await expect(reloadedDrawer.getByText('Copying photos')).toBeVisible()

  await reloadedDrawer.getByRole('button', { name: 'Clear task history' }).click()
  const confirmation = page.getByRole('alertdialog', { name: 'Clear task history' })
  await expect(confirmation).toContainText('Active tasks will be kept')
  await confirmation.getByRole('button', { name: 'Clear history' }).click()

  await expect.poll(() => clearHistoryCalled).toBe(true)
  await expect(reloadedDrawer.getByText('History', { exact: true })).toHaveCount(0)
  await expect(reloadedDrawer.getByText('Copying photos')).toBeVisible()
})

test('folder download starts an archive task without opening the task drawer', async ({ page }) => {
  let operationRequest: Record<string, unknown> | undefined
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: {
      path: '/',
      entries: [{
        name: 'photos',
        path: '/Files/photos',
        is_dir: true,
        can_navigate: true,
        size: 0,
        modified: '2000-01-01T00:00:00Z',
        mode: 'drwxr-xr-x',
      }],
    },
  }))
  await page.route('**/api/fs/thumbnails/jobs**', route => route.fulfill({ json: { jobs: [] } }))
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'POST') {
      operationRequest = route.request().postDataJSON() as Record<string, unknown>
      await route.fulfill({
        status: 202,
        json: {
          id: 'archive-1', kind: 'archive', sources: ['/Files/photos'], state: 'queued',
          phase: 'queued', message: 'Queued', progress_done: 0, progress_total: 0,
          bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0,
          queue_position: 1, cancellable: true,
          started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z',
        },
      })
      return
    }
    await route.fulfill({ json: { operations: [] } })
  })

  await page.goto('/')
  const folder = page.locator('[data-file-entry]').filter({ hasText: 'photos', visible: true })
  await expect(folder).toBeVisible()
  await dispatchContextMenu(folder)
  await page.getByRole('menuitem', { name: 'Download' }).click()

  await expect.poll(() => operationRequest).toEqual({ kind: 'archive', sources: ['/Files/photos'] })
  await expect(page.getByRole('button', { name: 'Open tasks' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Close tasks' })).toHaveCount(0)
})

test('downloaded folder archive history shows that its artifact is no longer available', async ({ page }) => {
  await page.route('**/api/preferences', route => route.fulfill({ json: { theme: 'light' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/thumbnails/jobs**', route => route.fulfill({ json: { jobs: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [{
    id: 'archive-downloaded', kind: 'archive', sources: ['/Files/photos'], state: 'completed', phase: 'downloaded',
    message: 'Downloaded · temporary archive removed', progress_done: 10, progress_total: 10, progress_unit: 'bytes',
    bytes_done: 10, bytes_total: 10, items_done: 1, items_total: 1, cancellable: false,
    started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:02Z', finished_at: '2000-01-01T00:00:01Z',
  }, {
    id: 'archive-ready', kind: 'archive', sources: ['/Files/videos'], state: 'completed', phase: 'completed',
    message: 'Ready to download', progress_done: 20, progress_total: 20, progress_unit: 'bytes',
    bytes_done: 20, bytes_total: 20, items_done: 2, items_total: 2, cancellable: false,
    started_at: '2000-01-01T00:00:03Z', updated_at: '2000-01-01T00:00:05Z', finished_at: '2000-01-01T00:00:04Z',
  }] } }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks' }).click()
  const drawer = page.getByRole('complementary')
  await expect.poll(async () => {
    const box = await drawer.boundingBox()
    const viewportWidth = await page.evaluate(() => window.innerWidth)
    return box ? Math.abs(box.x + box.width - viewportWidth) : Number.MAX_SAFE_INTEGER
  }).toBeLessThanOrEqual(1)
  await expect(drawer.getByText('downloaded', { exact: true })).toBeVisible()
  await expect(drawer.getByRole('button', { name: 'Download Archive photos' })).toHaveCount(0)
  const downloadedCard = drawer.locator('article').filter({ hasText: 'Archive photos' })
  const readyCard = drawer.locator('article').filter({ hasText: 'Archive videos' })
  const downloadedStatus = await downloadedCard.locator('[data-task-status]').boundingBox()
  const downloadedClose = await downloadedCard.getByRole('button', { name: 'Remove Archive photos from history' }).boundingBox()
  const readyStatus = await readyCard.locator('[data-task-status]').boundingBox()
  const readyDownload = await readyCard.getByRole('button', { name: 'Download Archive videos' }).boundingBox()
  const readyClose = await readyCard.getByRole('button', { name: 'Remove Archive videos from history' }).boundingBox()
  expect(downloadedStatus).not.toBeNull()
  expect(downloadedClose).not.toBeNull()
  expect(readyStatus).not.toBeNull()
  expect(readyDownload).not.toBeNull()
  expect(readyClose).not.toBeNull()
  expect(Math.abs(downloadedStatus!.x + downloadedStatus!.width - downloadedClose!.x - downloadedClose!.width)).toBeLessThanOrEqual(1)
  expect(downloadedStatus!.y).toBeGreaterThan(downloadedClose!.y + downloadedClose!.height)
  expect(Math.abs(readyStatus!.x + readyStatus!.width - readyClose!.x - readyClose!.width)).toBeLessThanOrEqual(1)
  expect(readyStatus!.y).toBeGreaterThan(readyClose!.y + readyClose!.height)
  expect(Math.abs(readyDownload!.y - readyClose!.y)).toBeLessThanOrEqual(1)
  await expect(downloadedCard.locator('[data-task-status]')).toHaveCSS('background-color', 'rgb(209, 250, 229)')
  await expect(downloadedCard.locator('[data-task-status]')).toHaveCSS('color', 'rgb(6, 95, 70)')

  await downloadedCard.locator('summary', { hasText: 'Details' }).click()
  const expandedStatus = await downloadedCard.locator('[data-task-status]').boundingBox()
  const expandedClose = await downloadedCard.getByRole('button', { name: 'Remove Archive photos from history' }).boundingBox()
  expect(expandedStatus).not.toBeNull()
  expect(expandedClose).not.toBeNull()
  expect(Math.abs(expandedStatus!.y - downloadedStatus!.y)).toBeLessThanOrEqual(1)
  expect(Math.abs(expandedClose!.y - downloadedClose!.y)).toBeLessThanOrEqual(1)
})

test('task drawer surfaces the transient video thumbnail queue separately', async ({ page }) => {
  let cancelled = ''
  const queuedJobs = Array.from({ length: 11 }, (_, index) => ({
    id: `queued-${index + 1}`,
    path: `/media/queued-${index + 1}.mp4`,
    name: `queued-${index + 1}.mp4`,
    state: 'queued',
    queue_position: index + 1,
    cancellable: true,
    updated_at: `2000-01-01T00:00:${String(index).padStart(2, '0')}Z`,
  }))
  const jobs = [
    { id: 'settling-hidden', path: '/media/settling-hidden.mp4', name: 'settling-hidden.mp4', state: 'settling', cancellable: true, updated_at: '2000-01-01T00:00:00Z' },
    queuedJobs[1],
    { id: 'processing-2', path: '/media/processing-2.mp4', name: 'processing-2.mp4', state: 'generating', cancellable: true, started_at: '2000-01-01T00:00:01Z', updated_at: '2000-01-01T00:00:01Z' },
    queuedJobs[0],
    { id: 'processing-1', path: '/media/processing-1.mp4', name: 'processing-1.mp4', state: 'probing', cancellable: true, started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z' },
    ...queuedJobs.slice(2),
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs**', route => {
    if (route.request().method() === 'DELETE') {
      cancelled = route.request().url().split('/').pop() ?? ''
      return route.fulfill({ json: { ok: true } })
    }
    return route.fulfill({ json: { jobs } })
  })

  await page.goto('/')
  await expect(page.getByRole('button', { name: 'Open tasks' })).toHaveText('13')
  await page.getByRole('button', { name: 'Open tasks' }).click()

  const drawer = page.getByRole('complementary')
  await expect(drawer.getByText('Video thumbnails', { exact: true })).toBeVisible()
  await expect(drawer.getByText('settling-hidden.mp4', { exact: true })).toHaveCount(0)
  await expect(drawer.getByText('2 generating, 11 queued', { exact: true })).toBeVisible()
  await expect(drawer.getByRole('button', { name: 'Collapse Video thumbnails (13)' })).toBeVisible()
  const visibleJobIDs = await drawer.locator('[data-thumbnail-job]').evaluateAll(elements => elements.map(element => element.getAttribute('data-thumbnail-job')))
  expect(visibleJobIDs).toEqual([
    'processing-1', 'processing-2',
    'queued-1', 'queued-2', 'queued-3', 'queued-4', 'queued-5', 'queued-6', 'queued-7', 'queued-8',
  ])
  await expect(drawer.getByText('and 3 more', { exact: true })).toBeVisible()
  const processingThumbnail = drawer.locator('[data-thumbnail-job="processing-1"]')
  const queuedThumbnail = drawer.locator('[data-thumbnail-job="queued-1"]')
  await expect(processingThumbnail).toHaveAttribute('data-thumbnail-processing', 'true')
  await expect(processingThumbnail).toHaveClass(/webfile-task-processing/)
  await expect(queuedThumbnail).not.toHaveAttribute('data-thumbnail-processing', 'true')
  await expect(queuedThumbnail).toHaveClass(/webfile-task-queued/)
  const thumbnailSpinners = drawer.locator('[data-thumbnail-job] [data-task-spinner]')
  await expect(thumbnailSpinners).toHaveCount(10)
  await expect(drawer.getByRole('progressbar')).toHaveCount(0)
  await expect(drawer.getByRole('status', { name: 'processing-1.mp4 thumbnail task probing' })).toBeVisible()

  await drawer.getByRole('button', { name: 'Collapse Video thumbnails (13)' }).click()
  await expect(drawer.locator('[data-thumbnail-job]')).toHaveCount(0)
  await drawer.getByRole('button', { name: 'Expand Video thumbnails (13)' }).click()
  await drawer.locator('[data-thumbnail-job="queued-1"]').getByRole('button', { name: 'Cancel' }).click()
  await expect.poll(() => cancelled).toBe('queued-1')
})

test('failed video thumbnail tasks can be dismissed', async ({ page }) => {
  const failedJob = {
    id: 'failed-thumbnail',
    path: '/media/copying.mp4',
    name: 'copying.mp4',
    state: 'failed',
    error: 'video is incomplete',
    cancellable: false,
    updated_at: '2000-01-01T00:00:00Z',
  }
  let dismissed = false
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs**', route => {
    if (route.request().method() === 'DELETE') {
      dismissed = true
      return route.fulfill({ json: { ok: true } })
    }
    return route.fulfill({ json: { jobs: dismissed ? [] : [failedJob] } })
  })

  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks' }).click()

  const drawer = page.getByRole('complementary')
  const failedCard = drawer.locator('[data-thumbnail-job="failed-thumbnail"]')
  await expect(failedCard).toContainText('video is incomplete')
  await failedCard.getByRole('button', { name: 'Dismiss' }).click()
  await expect.poll(() => dismissed).toBe(true)
  await expect(failedCard).toHaveCount(0)
  await expect(drawer.getByText('No tasks yet.')).toBeVisible()
})

test('Extract here starts a single-pass task in the archive directory', async ({ page }) => {
  let operationRequest: Record<string, unknown> | null = null
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: { path: '/folder', entries: [{ name: 'archive.zip', path: '/folder/archive.zip', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }] },
  }))
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { operations: [] } })
      return
    }
    operationRequest = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({ json: { operations: [{ id: 'extract-1', kind: 'extract', sources: ['/folder/archive.zip'], destination: '/folder', state: 'queued', bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0, started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z' }] } })
  })

  await page.goto('/#path=%2Ffolder')
  await page.locator('[data-file-entry]').click({ button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'Extract…', exact: true })).toBeVisible()
  await page.getByRole('menuitem', { name: 'Extract here', exact: true }).click()

  await expect.poll(() => operationRequest).not.toBeNull()
  expect(operationRequest).toEqual({ kind: 'extract', sources: ['/folder/archive.zip'], destination: '/folder', extract_layout: 'here', existing_files: 'skip' })
  await expect(page.getByRole('button', { name: 'Open tasks' })).toHaveClass(/webfile-task-attention/)
  await expect(page.getByRole('complementary')).toHaveCount(0)
})

test('Extract defaults to an archive-named folder and prompts when the task requests a password', async ({ page }) => {
  let operationRequest: Record<string, unknown> | null = null
  let submittedPassword: Record<string, unknown> | null = null
  let releasePasswordRequest = () => {}
  const passwordRequestHold = new Promise<void>(resolve => { releasePasswordRequest = resolve })
  await page.route('**/api/fs/dir**', route => route.fulfill({
    json: { path: '/folder', entries: [{ name: 'secure.tar.gz', path: '/folder/secure.tar.gz', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }] },
  }))
  await page.route('**/api/fs/operations/extract-2/password', async route => {
    submittedPassword = route.request().postDataJSON() as Record<string, unknown>
    await passwordRequestHold
    await route.fulfill({ status: 202, json: { ok: true } })
  })
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: { operations: [] } })
      return
    }
    operationRequest = route.request().postDataJSON() as Record<string, unknown>
    await route.fulfill({ json: { operations: [{ id: 'extract-2', kind: 'extract', sources: ['/folder/secure.tar.gz'], destination: '/target/secure', state: 'awaiting_input', phase: 'awaiting_password', message: 'Password required', bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0, cancellable: true, started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z' }] } })
  })

  await page.goto('/#path=%2Ffolder')
  await page.locator('[data-file-entry]').click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Extract…', exact: true }).click()
  const destinationDialog = page.getByRole('dialog', { name: 'Extract secure.tar.gz' })
  await expect(destinationDialog.getByLabel('Password')).toHaveCount(0)
  await expect(destinationDialog.getByLabel('Destination folder')).toHaveValue('/folder')
  await expect(destinationDialog.getByRole('region', { name: 'Extraction folders' })).toContainText('/folder/secure')
  await destinationDialog.getByLabel('Destination folder').fill('/target')
  await expect(destinationDialog.getByRole('region', { name: 'Extraction folders' })).toContainText('/target/secure')
  await destinationDialog.getByRole('button', { name: 'Extract' }).click()

  const passwordDialog = page.getByRole('dialog', { name: 'Password required — secure.tar.gz' })
  await expect(passwordDialog).toBeVisible()
  const passwordInput = passwordDialog.getByLabel('Password')
  await expect(passwordDialog.locator('form')).toHaveAttribute('autocomplete', 'off')
  await expect(passwordInput).toHaveAttribute('autocomplete', 'off')
  await expect(passwordInput).toHaveAttribute('name', 'archive-passphrase')
  await expect(passwordInput).toHaveValue('')
  await passwordInput.fill('secret')
  await passwordDialog.getByRole('button', { name: 'Continue' }).click()
  await expect(passwordDialog).toBeHidden()

  await expect.poll(() => operationRequest).not.toBeNull()
  expect(operationRequest).toEqual({ kind: 'extract', sources: ['/folder/secure.tar.gz'], destination: '/target', extract_layout: 'separate_directories', existing_files: 'skip' })
  await expect.poll(() => submittedPassword).not.toBeNull()
  expect(submittedPassword).toEqual({ password: 'secret' })
  releasePasswordRequest()
})

for (const action of ['Extract…', 'Extract here']) {
  test(`${action} keeps the password prompt when the queued HTTP response trails realtime`, async ({ page }) => {
    let socket: WebSocketRoute | undefined
    await page.routeWebSocket('**/api/fs/ws', ws => { socket = ws })
    const queued = {
      id: 'password-race', kind: 'extract', sources: ['/folder/secure.7z'], destination: '/target',
      state: 'queued', phase: 'queued', message: '', cancellable: true,
      bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0,
      started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:00Z',
    }
    let current = { ...queued, state: 'awaiting_input', phase: 'awaiting_password', message: 'Password required', updated_at: '2000-01-01T00:00:01Z' }
    let started = false
    let responseSent = false
    let releaseResponse = () => {}
    const responseHold = new Promise<void>(resolve => { releaseResponse = resolve })
    await page.route('**/api/fs/dir**', route => route.fulfill({ json: {
      path: '/folder', entries: [{ name: 'secure.7z', path: '/folder/secure.7z', is_dir: false, size: 100, modified: queued.started_at, mode: '-rw-r--r--' }],
    } }))
    await page.route('**/api/fs/operations', async route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: { operations: started ? [current] : [] } })
      started = true
      socket!.send(JSON.stringify({ type: 'operation-updated', operation: current }))
      await responseHold
      await route.fulfill({ json: { operations: [queued] } })
      responseSent = true
    })
    await page.goto('/#path=%2Ffolder')
    await expect.poll(() => !!socket).toBe(true)
    await page.locator('[data-file-entry]').click({ button: 'right' })
    await page.getByRole('menuitem', { name: action, exact: true }).click()
    if (action === 'Extract…') {
      const destination = page.getByRole('dialog', { name: 'Extract secure.7z', exact: true })
      await destination.getByLabel('Destination folder').fill('/target')
      await destination.getByRole('button', { name: 'Extract', exact: true }).click()
    }
    const prompt = page.getByRole('dialog', { name: 'Password required — secure.7z', exact: true })
    await expect(prompt).toBeVisible()
    await prompt.getByLabel('Password', { exact: true }).fill('keep this input')
    releaseResponse()
    await expect.poll(() => responseSent).toBe(true)
    // A stale realtime event must also leave the accepted waiting state alone.
    socket!.send(JSON.stringify({ type: 'operation-updated', operation: queued }))
    await expect(prompt.getByLabel('Password', { exact: true })).toHaveValue('keep this input')
    await prompt.getByRole('button', { name: 'Later', exact: true }).click()
    await expect(prompt).toBeHidden()
    await page.getByRole('button', { name: 'Open tasks' }).click()
    await page.getByRole('button', { name: 'Enter password', exact: true }).click()
    await expect(prompt).toBeVisible()
    await expect(prompt.getByLabel('Password', { exact: true })).toHaveValue('')

    // Repeated identical wrong-password messages are distinct requests.
    let attempts = 0
    await page.route('**/api/fs/operations/password-race/password', async route => {
      attempts++
      current = { ...current, message: 'Incorrect password', updated_at: `2000-01-01T00:00:0${attempts + 1}Z` }
      socket!.send(JSON.stringify({ type: 'operation-updated', operation: current }))
      await route.fulfill({ status: 202, json: { ok: true } })
    })
    for (let attempt = 0; attempt < 2; attempt++) {
      await prompt.getByLabel('Password', { exact: true }).fill('wrong')
      await prompt.getByRole('button', { name: 'Continue', exact: true }).click()
      await expect(prompt.getByRole('alert')).toHaveText('Incorrect password')
      await expect(prompt.getByLabel('Password', { exact: true })).toHaveValue('')
    }
    current = { ...current, state: 'completed', phase: 'completed', message: '', updated_at: '2000-01-01T00:00:05Z' }
    socket!.send(JSON.stringify({ type: 'operation-updated', operation: current }))
    await expect(prompt).toBeHidden()
    await expect(page.getByRole('button', { name: 'Enter password', exact: true })).toHaveCount(0)
  })
}

test('recovered archive password prompts keep the current task in focus', async ({ page }) => {
  let socket: WebSocketRoute | undefined
  await page.routeWebSocket('**/api/fs/ws', ws => { socket = ws })
  const first = {
    id: 'first-password', kind: 'extract', sources: ['/first.7z'], destination: '/target',
    state: 'awaiting_input', phase: 'awaiting_password', message: 'Password required', cancellable: true,
    bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0,
    started_at: '2000-01-01T00:00:00Z', updated_at: '2000-01-01T00:00:01Z',
  }
  const second = { ...first, id: 'second-password', sources: ['/second.7z'] }
  let waiting = [first]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: waiting } }))
  await page.goto('/')
  const prompt = page.getByRole('dialog', { name: 'Password required — first.7z', exact: true })
  await expect(prompt).toBeVisible()
  await prompt.getByLabel('Password', { exact: true }).fill('retain me')
  await expect.poll(() => !!socket).toBe(true)
  waiting = [second, first]
  socket!.send(JSON.stringify({ type: 'operation-updated', operation: second }))
  await expect(prompt.getByLabel('Password', { exact: true })).toHaveValue('retain me')
  await expect(page.getByRole('dialog', { name: 'Password required — second.7z', exact: true })).toHaveCount(0)
  await prompt.getByRole('button', { name: 'Later', exact: true }).click()
  await expect(prompt).toBeHidden()
  await page.getByRole('button', { name: 'Open tasks' }).click()
  // Refreshing history recovers the other request without a realtime event.
  const secondPrompt = page.getByRole('dialog', { name: 'Password required — second.7z', exact: true })
  await expect(secondPrompt).toBeVisible()
  await secondPrompt.getByRole('button', { name: 'Later', exact: true }).click()
  await expect(secondPrompt).toBeHidden()
  await page.getByRole('complementary').locator('article').filter({ hasText: 'Extract first.7z' }).getByRole('button', { name: 'Enter password' }).click()
  await expect(prompt).toBeVisible()
})

test('multiple selected archives can be queued as independent extraction tasks', async ({ page }) => {
  let operationRequest: Record<string, unknown> | null = null
  const archiveEntries = [
    { name: 'first.zip', path: '/folder/first.zip', is_dir: false, size: 100, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
    { name: 'second.tar.gz', path: '/folder/second.tar.gz', is_dir: false, size: 200, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' },
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/folder', entries: archiveEntries } }))
  await page.route('**/api/fs/operations', async route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: { operations: [] } })
    operationRequest = route.request().postDataJSON() as Record<string, unknown>
    return route.fulfill({ json: { operations: archiveEntries.map((entry, index) => ({
      id: `extract-${index}`,
      kind: 'extract',
      sources: [entry.path],
      destination: `/folder/${index ? 'second' : 'first'}`,
      state: 'queued',
      queue_position: index + 1,
      progress_done: 0,
      progress_total: 0,
      bytes_done: 0,
      bytes_total: 0,
      items_done: 0,
      items_total: 0,
      cancellable: true,
      started_at: `2000-01-01T00:00:0${index}Z`,
      updated_at: `2000-01-01T00:00:0${index}Z`,
    })) } })
  })

  await page.goto('/#path=%2Ffolder')
  const entries = page.locator('[data-file-entry]')
  await entries.nth(0).click()
  await entries.nth(1).click({ modifiers: ['Control'] })
  await page.getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Extract 2 archives…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Extract 2 archives' })
  await expect(dialog.getByLabel('Destination folder')).toHaveValue('/folder')
  await expect(dialog.getByRole('region', { name: 'Extraction folders' }).getByRole('listitem')).toHaveText(['/folder/first', '/folder/second'])
  await dialog.getByRole('button', { name: 'Extract', exact: true }).click()

  await expect.poll(() => operationRequest).not.toBeNull()
  expect(operationRequest).toEqual({
    kind: 'extract',
    sources: ['/folder/first.zip', '/folder/second.tar.gz'],
    destination: '/folder',
    extract_layout: 'separate_directories',
    existing_files: 'skip',
  })
})

test('queued filesystem tasks expose persistent up and down controls', async ({ page }) => {
  let reorderRequest: Record<string, unknown> | null = null
  let reordered = false
  const queuedOperations = () => [
    {
      id: 'copy-queued', kind: 'copy', sources: ['/first'], destination: '/target', state: 'queued', queue_position: reordered ? 2 : 1,
      progress_done: 0, progress_total: 0, bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0, cancellable: true,
      started_at: '2000-01-01T00:00:01Z', updated_at: '2000-01-01T00:00:01Z',
    },
    {
      id: 'extract-queued', kind: 'extract', sources: ['/second.zip'], destination: '/target', state: 'queued', queue_position: reordered ? 1 : 2,
      progress_done: 0, progress_total: 0, bytes_done: 0, bytes_total: 0, items_done: 0, items_total: 0, cancellable: true,
      started_at: '2000-01-01T00:00:02Z', updated_at: '2000-01-01T00:00:02Z',
    },
  ]
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/', entries: [] } }))
  await page.route('**/api/fs/operations/**', async route => {
    reorderRequest = route.request().postDataJSON() as Record<string, unknown>
    reordered = true
    return route.fulfill({ json: queuedOperations()[0] })
  })
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: queuedOperations() } }))

  await page.goto('/')
  await page.getByRole('button', { name: 'Open tasks' }).click()
  const drawer = page.getByRole('complementary')
  await expect(drawer.getByText('Queue position 1', { exact: true })).toBeVisible()
  await expect(drawer.getByRole('button', { name: 'Move Copy first up' })).toBeDisabled()
  await drawer.getByRole('button', { name: 'Move Extract second.zip up' }).click()
  await expect.poll(() => reorderRequest).toEqual({ direction: 'up' })
  await expect(drawer.getByRole('button', { name: 'Move Extract second.zip up' })).toBeDisabled()
})
