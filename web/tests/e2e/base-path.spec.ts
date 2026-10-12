import { expect, test } from '@playwright/test'

test('browser traffic remains inside the configured application path', async ({ page }) => {
  const configured = (process.env.WEBFILE_E2E_PREFIX ?? '').replace(/\/$/, '')
  const prefix = configured === '/' ? '' : configured
  const requests: string[] = []
  let websocketPath = ''

  page.on('request', request => requests.push(new URL(request.url()).pathname))
  page.on('websocket', socket => { websocketPath = new URL(socket.url()).pathname })

  await page.goto(`${prefix}/#path=%2F`)

  await expect(page.getByText('live', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Files', exact: true })).toBeVisible()
  await expect.poll(() => requests).toContain(`${prefix}/api/session`)
  await expect.poll(() => requests).toContain(`${prefix}/api/fs/dir`)
  await expect.poll(() => requests.some(path => path.startsWith(`${prefix}/assets/`))).toBe(true)
  await expect.poll(() => websocketPath).toBe(`${prefix}/api/fs/ws`)
})
