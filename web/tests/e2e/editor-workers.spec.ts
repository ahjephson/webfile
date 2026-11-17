import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

for (const sample of [
  { name: 'example.js', content: 'const value={answer:42};', worker: 'ts.worker-' },
  { name: 'example.ts', content: 'const value:number=42;', worker: 'ts.worker-' },
  { name: 'example.json', content: '{"answer":42}', worker: 'json.worker-' },
  { name: 'example.css', content: 'body{color:red;}', worker: 'css.worker-' },
  { name: 'example.html', content: '<div>\n<p>content</p>\n</div>', worker: 'html.worker-' },
]) {
  test(`${sample.name} uses its language worker and formats without runtime errors`, async ({ page }) => {
    const errors: string[] = []
    const workers: string[] = []
    page.on('worker', worker => workers.push(new URL(worker.url()).pathname))
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => {
      if (message.type() === 'error') errors.push(message.text())
    })
    const fixture = await featureFixture(page, 'editor-workers')
    await fixture.write(`${fixture.path}/${sample.name}`, sample.content)
    await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
    await page.locator('[data-file-entry]').filter({ hasText: sample.name }).dblclick()
    await expect.poll(() => workers.some(path => path.includes(sample.worker))).toBe(true)
    await page.getByRole('button', { name: 'Show rich toolbar', exact: true }).click()
    await page.getByRole('button', { name: 'Format document', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    await expect.poll(() => fixture.read(`${fixture.path}/${sample.name}`)).not.toBe(sample.content)
    expect(errors).toEqual([])
  })
}
