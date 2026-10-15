import { expect, test, type Page } from '@playwright/test'

async function mockFolder(page: Page) {
  await page.route('**/api/preferences', route => route.fulfill({ json: { view_mode: 'list' } }))
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: { path: '/docs', entries: [{
    name: 'notes.txt', path: '/docs/notes.txt', is_dir: false, size: 12,
    modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--',
  }] } }))
  await page.route('**/api/fs/content-type**', route => route.fulfill({ json: { content_type: 'text/plain' } }))
}

for (const mode of ['existing', 'new', 'recreated'] as const) {
  test(`edits made during ${mode} file save remain unsaved until the next save`, async ({ page }) => {
    await mockFolder(page)
    let release!: () => void
    const released = new Promise<void>(resolve => { release = resolve })
    const submissions: string[] = []
    let persisted = 'original'
    await page.route('**/api/fs/file**', async route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: { content: persisted, etag: 'one' } })
      const content = route.request().postData() ?? ''
      submissions.push(content)
      await released
      if (mode === 'recreated' && submissions.length === 1) {
        await route.fulfill({ status: 404, json: { error: 'removed' } })
        return
      }
      persisted = content
      await route.fulfill({ json: { content, etag: 'two' } })
    })
    if (mode === 'new') {
      await page.goto('/#path=%2Fdocs')
      await expect(page).toHaveURL(/#path=%2Fdocs$/)
      await page.getByRole('button', { name: 'New file', exact: true }).click()
      await page.getByLabel('Filename', { exact: true }).fill('new.txt')
      await page.getByRole('button', { name: 'Open editor', exact: true }).click()
    } else {
      await page.goto('/#path=%2Fdocs&file=notes.txt')
    }
    const editor = page.getByRole('textbox', { name: 'Editor content' })
    const save = page.getByRole('button', { name: 'Save', exact: true })
    await editor.focus()
    await page.keyboard.press('ControlOrMeta+End')
    await page.keyboard.type(' first')
    await save.click()
    await expect.poll(() => submissions.length).toBe(1)
    await editor.focus()
    await page.keyboard.type(' later')
    release()
    await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible()
    await expect(save).toBeEnabled()
    await expect(page.locator('.view-lines')).toContainText('later')
    expect(persisted).not.toContain('later')
    if (mode === 'recreated') {
      await expect.poll(() => submissions.length).toBe(2)
      expect(submissions[1]).toBe(submissions[0])
    }
    await save.click()
    await expect(page.getByText('Saved', { exact: true })).toBeVisible()
    await expect(save).toBeDisabled()
    expect(persisted).toContain('later')
  })
}

test('cancelling browser Back preserves dirty text and accepting it preserves Forward navigation', async ({ page }) => {
  await mockFolder(page)
  await page.route('**/api/fs/file**', route => route.fulfill({ json: { content: 'original', etag: 'one' } }))
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await page.locator('[data-file-entry]').dblclick()
  const editor = page.getByRole('textbox', { name: 'Editor content' })
  await editor.focus()
  await page.keyboard.type('unsaved')
  let prompts = 0
  page.once('dialog', async dialog => { prompts++; await dialog.dismiss() })
  await page.goBack()
  await expect.poll(() => prompts).toBe(1)
  await expect(page).toHaveURL(/&file=notes\.txt$/)
  await expect(page.getByLabel('Text editor', { exact: true })).toBeVisible()
  await expect(page.locator('.view-lines')).toContainText('unsaved')
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
  page.once('dialog', async dialog => { prompts++; await dialog.accept() })
  await page.goBack()
  await expect(page.getByLabel('Text editor', { exact: true })).toHaveCount(0)
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  expect(prompts).toBe(2)
  await page.goForward()
  await expect(page.getByLabel('Text editor', { exact: true })).toBeVisible()
  await expect(page.locator('.view-lines')).toContainText('original')
  await expect(page.locator('.view-lines')).not.toContainText('unsaved')
})

test('Close editor asks only once before discarding changes', async ({ page }) => {
  await mockFolder(page)
  await page.route('**/api/fs/file**', route => route.fulfill({ json: { content: 'original', etag: 'one' } }))
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await page.locator('[data-file-entry]').dblclick()
  await page.getByRole('textbox', { name: 'Editor content' }).focus()
  await page.keyboard.type('unsaved')
  let prompts = 0
  page.on('dialog', async dialog => { prompts++; await dialog.accept() })
  await page.getByRole('button', { name: 'Close editor', exact: true }).click()
  await expect(page.getByLabel('Text editor', { exact: true })).toHaveCount(0)
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  expect(prompts).toBe(1)
})

for (const fail of [false, true]) {
  test(`late Details ${fail ? 'errors' : 'responses'} cannot replace the current selection`, async ({ page }) => {
    await mockFolder(page)
    let release!: () => void
    let held = false
    const released = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/fs/details**', async route => {
      const path = new URL(route.request().url()).searchParams.get('path')!
      if (path === '/docs') {
        held = true
        await released
        if (fail) return route.fulfill({ status: 500, json: { error: 'stale details error' } })
      }
      await route.fulfill({ json: { path, name: path === '/docs' ? 'docs' : 'notes.txt',
        is_dir: path === '/docs', size: 12, size_status: 'ready',
        modified: '2000-01-01T00:00:00Z', mode_octal: '0644', owner: 'test', group: 'test', uid: 1000, gid: 1000 } })
    })
    await page.goto('/#path=%2Fdocs')
    await expect(page).toHaveURL(/#path=%2Fdocs$/)
    await page.getByRole('button', { name: 'Open details', exact: true }).click()
    await expect.poll(() => held).toBe(true)
    await page.locator('[data-file-entry]').click()
    const details = page.locator('aside')
    await expect(details.getByText('/docs/notes.txt', { exact: true })).toBeVisible()
    const response = page.waitForResponse(response => response.url().includes('/api/fs/details?') && new URL(response.url()).searchParams.get('path') === '/docs')
    release()
    await response
    await expect(details.getByText('/docs/notes.txt', { exact: true })).toBeVisible()
    await expect(page.getByText('stale details error', { exact: true })).toHaveCount(0)
  })
}

test('closing Details while a request is pending does not restart folder-size polling', async ({ page }) => {
  await mockFolder(page)
  let release!: () => void
  const released = new Promise<void>(resolve => { release = resolve })
  let reads = 0
  await page.route('**/api/fs/details**', async route => {
    reads++
    await released
    await route.fulfill({ json: { path: '/docs', name: 'docs', is_dir: true, size_status: 'calculating' } })
  })
  await page.goto('/#path=%2Fdocs')
  await expect(page).toHaveURL(/#path=%2Fdocs$/)
  await page.getByRole('button', { name: 'Open details', exact: true }).click()
  await expect.poll(() => reads).toBe(1)
  await page.locator('aside').getByRole('button', { name: 'Close details', exact: true }).click()
  const response = page.waitForResponse(response => response.url().includes('/api/fs/details?'))
  release()
  await response
  // Cross the polling interval to catch a late response scheduling new work.
  await page.waitForTimeout(650)
  expect(reads).toBe(1)
})
