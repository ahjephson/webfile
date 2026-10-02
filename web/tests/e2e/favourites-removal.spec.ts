import { expect, test } from '@playwright/test'

test('does not expose the removed favourites interface', async ({ page }) => {
  await page.goto('/')

  await expect(page.getByRole('button', { name: /favourites/i })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Favourites' })).toHaveCount(0)
})
