import { expect, test } from '@playwright/test'
import { featureFixture } from './feature-fixture'

test('opening a form prioritises editable invalid fields', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/#path=/)
  // Supply a native invalid field before the opening transition finishes.
  await page.evaluate(() => {
    const observer = new MutationObserver(() => {
      const form = document.querySelector('[role="dialog"] form')
      if (!form) return
      observer.disconnect()
      const disabled = document.createElement('input')
      disabled.disabled = true
      disabled.setAttribute('aria-invalid', 'true')
      form.append(disabled)
      const invalid = document.createElement('input')
      invalid.required = true
      invalid.setAttribute('aria-label', 'Invalid field')
      form.append(invalid)
    })
    observer.observe(document.body, { childList: true, subtree: true })
  })
  await page.getByRole('button', { name: 'New folder', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Invalid field' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toBeHidden()
})

test('forms focus fields, validate Enter, ignore repeats/composition, and recover after failure', async ({ page }) => {
  const fixture = await featureFixture(page, 'dialogs')
  await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
  const opener = page.getByRole('button', { name: 'New folder', exact: true })
  await opener.click()
  const dialog = page.getByRole('dialog', { name: 'New folder', exact: true })
  const input = dialog.locator('input')
  await expect(input).toBeFocused()
  await input.press('Enter')
  await expect(dialog).toBeVisible()
  await input.fill('retry-folder')
  await input.evaluate(element => element.setCustomValidity('Choose another name'))
  await input.press('Enter')
  await expect(dialog).toBeVisible()
  await expect(input).toBeFocused()
  await input.evaluate(element => element.setCustomValidity(''))
  let requests = 0
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/mkdir', async route => {
    requests++
    if (requests === 1) {
      await pending
      await route.fulfill({ status: 500, json: { error: 'Try again' } })
    } else await route.continue()
  })
  for (const options of [{ repeat: true }, { isComposing: true }]) {
    await input.dispatchEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, ...options })
  }
  expect(requests).toBe(0)
  await input.press('Enter')
  await expect.poll(() => requests).toBe(1)
  await expect(dialog).toHaveAttribute('aria-busy', 'true')
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Create', exact: true })).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeVisible()
  release()
  await expect(input).toBeEnabled()
  await expect(input).toHaveValue('retry-folder')
  await input.press('Enter')
  await expect(dialog).toBeHidden()
  expect(requests).toBe(2)
  await expect(opener).toBeFocused()
})

test('dropdowns consume the first Escape and preserve Enter; closing restores focus', async ({ page }) => {
  await page.goto('/')
  const opener = page.getByRole('button', { name: 'Settings', exact: true })
  await opener.click()
  const dialog = page.getByRole('dialog', { name: 'Settings', exact: true })
  const theme = dialog.getByRole('combobox', { name: 'Theme', exact: true })
  await expect(theme).toBeFocused()
  await theme.press('Enter')
  await expect(theme).toHaveAttribute('aria-expanded', 'true')
  await theme.press('Escape')
  await expect(theme).toHaveAttribute('aria-expanded', 'false')
  await expect(dialog).toBeVisible()
  await theme.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(opener).toBeFocused()
})

test('checkboxes, buttons, and multiline fields keep their own keyboard behaviour', async ({ page }) => {
  const fixture = await featureFixture(page, 'dialog-controls')
  await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
  await page.getByRole('button', { name: 'Search beneath this folder', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Search beneath this folder', exact: true })
  const input = dialog.getByRole('textbox', { name: 'Search filenames' })
  await expect(input).toBeFocused()
  await input.fill('notes')
  const checkbox = dialog.getByRole('checkbox', { name: 'Wildcards (* and ?)' })
  await checkbox.press('Space')
  await expect(checkbox).toBeChecked()
  await checkbox.press('Enter')
  await expect(dialog).toBeVisible()
  // Exercise the shared dialog handler with a native multiline field.
  await dialog.locator('form').evaluate(form => {
    const textarea = document.createElement('textarea')
    textarea.setAttribute('aria-label', 'Multiline notes')
    form.append(textarea)
  })
  const multiline = dialog.getByRole('textbox', { name: 'Multiline notes' })
  await multiline.fill('first')
  await multiline.press('End')
  await multiline.press('Enter')
  await multiline.pressSequentially('second')
  await expect(multiline).toHaveValue('first\nsecond')
  await expect(dialog).toBeVisible()
  await dialog.locator('[data-dialog-cancel]').press('Enter')
  await expect(dialog).toBeHidden()
})

test('confirmation defaults to its action, Escape cancels, and failed actions allow retry', async ({ page }) => {
  const fixture = await featureFixture(page, 'dialog-confirm')
  await fixture.write(`${fixture.path}/delete.txt`, 'confirmation fixture')
  await page.goto(`/#path=${encodeURIComponent(fixture.path)}`)
  await page.locator('[data-file-entry]').filter({ hasText: 'delete.txt' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('alertdialog', { name: 'Delete', exact: true })
  const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true })
  const accept = dialog.getByRole('button', { name: 'Delete permanently' })
  await expect(accept).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  expect(await fixture.exists(`${fixture.path}/delete.txt`)).toBe(true)
  await page.locator('[data-file-entry]').filter({ hasText: 'delete.txt' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Delete', exact: true }).click()
  await expect(accept).toBeFocused()
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/fs/operations', async route => {
    await pending
    await route.fulfill({ status: 500, json: { error: 'Try again' } })
  })
  await page.keyboard.press('Enter')
  await expect(cancel).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeVisible()
  release()
  await expect(cancel).toBeEnabled()
  await cancel.press('Enter')
  await expect(dialog).toBeHidden()
  expect(await fixture.exists(`${fixture.path}/delete.txt`)).toBe(true)
})
