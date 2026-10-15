import { expect, test, type Page } from '@playwright/test'

const modified = '2000-01-01T00:00:00Z'
const file = (name: string, folder = '/docs') => ({ name, path: `${folder}/${name}`, is_dir: false, size: 12, modified, mode: '-rw-r--r--' })

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
