import { expect, test, type Page } from '@playwright/test'

// One second of PCM silence: playable without codec-specific browser support.
function wavFile() {
  const samples = 8000
  const data = Buffer.alloc(44 + samples * 2)
  data.write('RIFF', 0)
  data.writeUInt32LE(data.length - 8, 4)
  data.write('WAVEfmt ', 8)
  data.writeUInt32LE(16, 16)
  data.writeUInt16LE(1, 20)
  data.writeUInt16LE(1, 22)
  data.writeUInt32LE(samples, 24)
  data.writeUInt32LE(samples * 2, 28)
  data.writeUInt16LE(2, 32)
  data.writeUInt16LE(16, 34)
  data.write('data', 36)
  data.writeUInt32LE(samples * 2, 40)
  return data
}

async function audioFolder(page: Page, name: string, contentType: string | null) {
  await page.route('**/api/fs/dir**', route => route.fulfill({ json: {
    path: '/',
    entries: [{ name, path: `/${name}`, is_dir: false, size: 16044, modified: '2000-01-01T00:00:00Z', mode: '-rw-r--r--' }],
  } }))
  await page.route('**/api/fs/content-type**', route => contentType === null
    ? route.abort()
    : route.fulfill({ json: { path: `/${name}`, content_type: contentType } }))
  await page.route('**/api/fs/download**', route => route.fulfill({ contentType: 'audio/wav', body: wavFile() }))
  await page.goto('/')
  await page.locator('[data-file-entry]').dblclick()
}

test('audio preview plays detected extensionless audio and stops when closed', async ({ page }) => {
  await audioFolder(page, 'recording', 'audio/wave')
  const audio = page.locator('audio')
  await expect(audio).toBeVisible()
  await expect(page.getByRole('button', { name: 'Enter fullscreen' })).toHaveCount(0)
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.readyState)).toBeGreaterThanOrEqual(2)
  expect(await audio.evaluate((element: HTMLAudioElement) => ({
    controls: element.controls, autoplay: element.autoplay, paused: element.paused, preload: element.preload,
  }))).toEqual({ controls: true, autoplay: false, paused: true, preload: 'metadata' })
  // Retain the old element to verify explicit cleanup, even if detached.
  const player = await audio.elementHandle()
  await audio.evaluate((element: HTMLAudioElement) => element.play())
  await expect.poll(() => audio.evaluate((element: HTMLAudioElement) => element.currentTime)).toBeGreaterThan(0)
  await page.getByRole('button', { name: 'Back to folder' }).click()
  await expect(audio).toHaveCount(0)
  expect(await player!.evaluate((element: HTMLAudioElement) => element.paused)).toBe(true)
  await page.locator('[data-file-entry]').dblclick()
  await expect(audio).toBeVisible()
  expect(await audio.evaluate((element: HTMLAudioElement) => element.paused)).toBe(true)
})

for (const extension of ['aac', 'flac', 'm4a', 'mp3', 'oga', 'ogg', 'opus', 'wav']) {
  test(`audio preview falls back to the ${extension} extension when inspection fails`, async ({ page }) => {
    await audioFolder(page, `recording.${extension.toUpperCase()}`, null)
    await expect(page.locator('audio')).toBeVisible()
    await expect(page.locator('audio')).toHaveAttribute('src', /inline=true/)
  })
}

test('audio preview reports playback errors and can reopen the same file', async ({ page }) => {
  await audioFolder(page, 'recording.wav', 'audio/wav')
  await page.locator('audio').dispatchEvent('error')
  await expect(page.getByRole('heading', { name: 'Preview unavailable' })).toBeVisible()
  await expect(page.getByText('The browser could not play this audio file.')).toBeVisible()
  await page.getByRole('button', { name: 'Back to folder' }).click()
  await page.locator('[data-file-entry]').dblclick()
  await expect(page.locator('audio')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Preview unavailable' })).toHaveCount(0)
})
