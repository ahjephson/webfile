import { expect, test, type Page } from '@playwright/test'

const modified = '2000-01-01T00:00:00Z'
const file = (name: string, folder = '/docs') => ({ name, path: `${folder}/${name}`, is_dir: false, size: 12, modified, mode: '-rw-r--r--' })
const folder = (name: string, parent = '/docs') => ({ ...file(name, parent), is_dir: true, mode: 'drwxr-xr-x' })

const task = (id: string, state = 'running') => ({
  id, kind: 'copy', sources: [`/docs/${id}.txt`], state, phase: state,
  message: `task ${id}`, bytes_done: 1, bytes_total: 10, items_done: 0, items_total: 1,
  progress_done: 1, progress_total: 10, progress_unit: 'bytes', cancellable: state === 'running',
  started_at: modified, updated_at: modified,
})

async function mockFolder(page: Page, entries = [file('notes.txt')]) {
  await page.route('**/api/session', route => route.fulfill({ json: { authenticated: true, mode: 'none', user: { username: 'audit' } } }))
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list', theme: 'dark' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: new URL(route.request().url()).searchParams.get('path'), entries } }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'text/plain' } }))
  await page.route('**/api/fs/file**', route => route.fulfill({ json: { content: 'original', etag: 'original' } }))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: [] } }))
  await page.route('**/api/fs/thumbnails/jobs', route => route.fulfill({ json: { jobs: [] } }))
  await page.routeWebSocket('**/api/fs/ws', () => {})
}

test('repeated elevation changes keep the folder and exactly one realtime connection', async ({ page }) => {
  await mockFolder(page)
  let elevated = false, sockets = 0, closed = 0
  const session = () => ({ authenticated: true, mode: 'pam', user: { username: 'audit', uid: 1000 },
    elevation_available: true, elevated, elevation_expires_at: new Date(Date.now() + 60_000).toISOString() })
  await page.route('**/api/session', route => route.fulfill({ json: session() }))
  await page.route('**/api/elevation', route => {
    elevated = route.request().method() === 'POST'
    return elevated ? route.fulfill({ json: session() }) : route.fulfill({ status: 204 })
  })
  await page.routeWebSocket('**/api/fs/ws', socket => {
    sockets++
    socket.onClose(() => { closed++ })
  })
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  for (let round = 0; round < 3; round++) {
    await page.getByRole('button', { name: 'Elevate access', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Elevate access' })
    const password = dialog.locator('input[type="password"]')
    await password.click()
    await expect(password).toBeFocused()
    await password.fill('test password')
    await expect(password).toHaveValue('test password')
    await dialog.getByRole('button', { name: 'Elevate access', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Drop elevated access', exact: true })).toBeVisible()
    await expect.poll(() => sockets).toBe(round * 2 + 2)
    await page.getByRole('button', { name: 'Drop elevated access', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Elevate access', exact: true })).toBeVisible()
    await expect.poll(() => sockets).toBe(round * 2 + 3)
    await expect(page).toHaveURL(/#path=%2Fdocs$/)
  }
  await page.waitForLoadState('networkidle')
  expect(sockets - closed).toBe(1)
  expect(sockets).toBe(7)
})

test('elevation expiry preserves dirty editor text and reconnects with ordinary access', async ({ page }) => {
  await mockFolder(page)
  let sessionReads = 0, fileReads = 0, sockets = 0
  const expires = new Date(Date.now() + 5_000).toISOString()
  await page.route('**/api/session', route => route.fulfill({ json: {
    authenticated: true, mode: 'pam', user: { username: 'audit', uid: 1000, gid: 1000 }, csrf_token: 'audit',
    elevation_available: true, elevated: ++sessionReads === 1, elevation_expires_at: expires,
  } }))
  await page.route('**/api/fs/file**', route => {
    fileReads++
    return route.fulfill({ json: { content: 'original', etag: 'original' } })
  })
  await page.routeWebSocket('**/api/fs/ws', () => { sockets++ })
  await page.goto('/#path=%2Fdocs&file=notes.txt')
  await page.getByRole('textbox', { name: 'Editor content' }).focus()
  await page.keyboard.type('UNSAVED_EXPIRY')
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible()
  await expect.poll(() => sessionReads, { timeout: 10_000 }).toBe(2)
  await page.waitForLoadState('networkidle')
  await expect(page.locator('.view-lines')).toContainText('UNSAVED_EXPIRY')
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
  await expect.poll(() => sockets).toBe(2)
  expect(fileReads).toBe(1)
  await expect(page).toHaveURL(/#path=%2Fdocs&file=notes\.txt$/)
})

test('a late file-open response cannot replace a newer dirty document', async ({ page }) => {
  await mockFolder(page, [file('first.txt'), file('second.txt')])
  let release!: () => void
  let held = false
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/content-type**', async route => {
    if (new URL(route.request().url()).searchParams.get('path') === '/docs/first.txt') {
      held = true
      await pending
    }
    return route.fulfill({ json: { content_type: 'text/plain' } })
  })
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await page.locator('[data-file-entry]').filter({ hasText: 'first.txt' }).dblclick()
  await expect.poll(() => held).toBe(true)
  await page.locator('[data-file-entry]').filter({ hasText: 'second.txt' }).dblclick()
  await page.getByRole('textbox', { name: 'Editor content' }).focus()
  await page.keyboard.type('UNSAVED_SECOND')
  const response = page.waitForResponse(response => response.url().includes('/api/fs/content-type?') && new URL(response.url()).searchParams.get('path') === '/docs/first.txt')
  release()
  await response
  await page.waitForLoadState('networkidle')
  await expect(page.locator('.view-lines')).toContainText('UNSAVED_SECOND')
  await expect(page).toHaveURL(/file=second\.txt$/)
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible()
})

for (const action of ['navigate', 'new document']) {
  test(`a pending file open cannot undo ${action}`, async ({ page }) => {
    await mockFolder(page)
    let release!: () => void
    let held = false
    const pending = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/fs/content-type**', async route => {
      held = true
      await pending
      return route.fulfill({ json: { content_type: 'text/plain' } })
    })
    await page.goto('/#path=%2Fdocs')
    await expect(page).toHaveURL(/#path=%2Fdocs$/)
    await page.locator('[data-file-entry]').dblclick()
    await expect.poll(() => held).toBe(true)
    if (action === 'navigate') {
      await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('button', { name: 'Files', exact: true }).click()
      await expect(page).toHaveURL(/#path=%2F$/)
    } else {
      await page.getByRole('button', { name: 'New file', exact: true }).click()
      await page.getByLabel('Filename', { exact: true }).fill('new.txt')
      await page.getByRole('button', { name: 'Open editor', exact: true }).click()
      await page.getByRole('textbox', { name: 'Editor content' }).focus()
      await page.keyboard.type('UNSAVED_NEW')
    }
    release()
    await page.waitForLoadState('networkidle')
    if (action === 'navigate') {
      await expect(page.getByLabel('Text editor', { exact: true })).toHaveCount(0)
      await expect(page).toHaveURL(/#path=%2F$/)
    } else {
      await expect(page.locator('.view-lines')).toContainText('UNSAVED_NEW')
      await expect(page.getByText('New file — save to create', { exact: true })).toBeVisible()
    }
  })
}

for (const fail of [false, true]) {
  test(`destination browser ignores late ${fail ? 'errors' : 'listings'} after navigating and reopening`, async ({ page }) => {
    await mockFolder(page)
    let hold = false, held = false
    let release!: () => void
    const pending = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/fs/dir**', async route => {
      const path = new URL(route.request().url()).searchParams.get('path')!
      const delayed = hold && path === '/docs'
      if (delayed) { held = true; await pending }
      if (delayed && fail) return route.fulfill({ status: 500, json: { error: 'stale destination error' } })
      return route.fulfill({ json: { path, entries: path === '/' ? [folder('current-root', '')] : [file('notes.txt'), folder('stale-folder')] } })
    })
    await page.goto('/#path=%2Fdocs')
    await expect(page).toHaveURL(/#path=%2Fdocs$/)
    hold = true
    await page.locator('[data-file-entry]').filter({ hasText: 'notes.txt' }).click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Copy to…', exact: true }).click()
    await expect.poll(() => held).toBe(true)
    const dialog = page.getByRole('dialog', { name: 'Copy to…' })
    await dialog.getByRole('button', { name: 'Roots', exact: true }).click()
    await expect(dialog.getByRole('button', { name: 'current-root', exact: true })).toBeVisible()
    const response = page.waitForResponse(response => response.url().includes('/api/fs/dir?') && new URL(response.url()).searchParams.get('path') === '/docs')
    release()
    await response
    await expect(dialog.getByRole('button', { name: 'current-root', exact: true })).toBeVisible()
    await expect(dialog.getByText('stale destination error', { exact: true })).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    hold = false
    await page.locator('[data-file-entry]').filter({ hasText: 'notes.txt' }).click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Copy to…', exact: true }).click()
    await expect(dialog.getByRole('button', { name: 'stale-folder', exact: true })).toBeVisible()
  })
}

test('task history limits retain every active task on load and realtime updates', async ({ page }) => {
  await mockFolder(page)
  const active = Array.from({ length: 55 }, (_, index) => task(`active-${index}`))
  await page.route('**/api/fs/operations', route => route.fulfill({ json: { operations: active } }))
  let send = (_message: unknown) => {}
  await page.routeWebSocket('**/api/fs/ws', socket => { send = message => socket.send(JSON.stringify(message)) })
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await page.getByRole('button', { name: 'Open tasks' }).click()
  const tasks = page.getByRole('complementary')
  await expect(tasks.getByRole('button', { name: 'Collapse Active (55)' })).toBeVisible()
  for (let index = 0; index < 60; index++) send({ type: 'operation-updated', operation: task(`history-${index}`, 'completed') })
  await expect(tasks.getByRole('button', { name: 'Collapse History (50)' })).toBeVisible()
  await expect(tasks.getByRole('button', { name: 'Collapse Active (55)' })).toBeVisible()
  await expect(tasks.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(55)
})

test('Save As into another folder survives reload and closes into that folder', async ({ page }) => {
  await mockFolder(page)
  let saved = ''
  const opened: string[] = []
  await page.route('**/api/fs/file**', route => {
    const path = new URL(route.request().url()).searchParams.get('path')!
    if (route.request().method() === 'GET') {
      opened.push(path)
      return route.fulfill({ json: { content: path === saved ? 'edited' : 'original', etag: 'original' } })
    }
    if (path === '/docs/notes.txt') return route.fulfill({ status: 412, json: { error: 'changed' } })
    saved = path
    return route.fulfill({ json: { content: 'edited', etag: 'new' } })
  })
  await page.goto('/#path=%2Fdocs&file=notes.txt')
  await page.getByRole('textbox', { name: 'Editor content' }).fill('edited')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  const conflict = page.getByRole('dialog', { name: 'The target changed' })
  await conflict.getByRole('textbox').fill('/elsewhere/saved.txt')
  await conflict.getByRole('button', { name: 'Save as', exact: true }).click()
  await expect(page.getByText('Saved', { exact: true })).toBeVisible()
  await expect(page).toHaveURL(/#path=%2Felsewhere&file=saved\.txt$/)
  await page.reload()
  await expect(page.locator('.view-lines')).toContainText('edited')
  expect(opened.at(-1)).toBe('/elsewhere/saved.txt')
  await page.getByRole('button', { name: 'Close editor', exact: true }).click()
  await expect(page).toHaveURL(/#path=%2Felsewhere$/)
})

for (const action of ['rename', 'folder', 'document'] as const) {
  for (const name of [' spaced.txt ', '   ']) {
    test(`${action} preserves the exact filename ${JSON.stringify(name)}`, async ({ page }) => {
      await mockFolder(page)
      let target = ''
      await page.route('**/api/fs/rename', route => {
        target = route.request().postDataJSON().to
        return route.fulfill({ json: { ok: true } })
      })
      await page.route('**/api/fs/mkdir', route => {
        target = route.request().postDataJSON().path
        return route.fulfill({ json: { ok: true } })
      })
      await page.route('**/api/fs/file**', route => {
        target = new URL(route.request().url()).searchParams.get('path')!
        return route.fulfill({ json: { content: '', etag: 'new' } })
      })
      await page.goto('/#path=%2Fdocs')
      await expect(page).toHaveURL(/#path=%2Fdocs$/)
      if (action === 'rename') {
        await page.locator('[data-file-entry]').click({ button: 'right' })
        await page.getByRole('menuitem', { name: 'Rename…', exact: true }).click()
      } else {
        await page.getByRole('button', { name: action === 'folder' ? 'New folder' : 'New file', exact: true }).click()
      }
      const dialog = page.getByRole('dialog')
      const submit = dialog.getByRole('button', { name: action === 'rename' ? 'Rename' : action === 'folder' ? 'Create' : 'Open editor', exact: true })
      await dialog.getByRole('textbox').fill('')
      await expect(submit).toBeDisabled()
      await dialog.getByRole('textbox').fill(name)
      await expect(submit).toBeEnabled()
      await submit.click()
      if (action === 'document') await page.getByRole('button', { name: 'Save', exact: true }).click()
      await expect.poll(() => target).toBe(`/docs/${name}`)
    })
  }
}
