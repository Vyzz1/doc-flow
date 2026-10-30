import { expect, test } from '@playwright/test'

test('demo upload completes and results tabs work without external requests', async ({ page }) => {
  const errors: string[] = []
  const external: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/*', route => {
    if (new URL(route.request().url()).hostname !== '127.0.0.1') { external.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  const response = await page.goto('/')
  expect(response?.status()).toBe(200)
  await expect(page.getByRole('heading', { name: 'Welcome back.' })).toBeVisible()
  await page.getByRole('button', { name: 'Explore demo workspace' }).click()
  await expect(page.getByRole('heading', { name: 'From document to data.' })).toBeVisible()
  await page.screenshot({ path: 'test-results/workspace-desktop.png', fullPage: true })
  await page.getByLabel('Choose your file').setInputFiles({ name: 'invoice.png', mimeType: 'image/png', buffer: Buffer.from('local demo file') })
  await page.getByRole('button', { name: 'Upload & process' }).click()
  await expect(page.getByText('Processing', { exact: true })).toBeVisible()
  await expect(page.getByText('Completed', { exact: true })).toBeVisible({ timeout: 15000 })
  await expect(page.getByRole('cell', { name: 'INV-2026-042', exact: true })).toBeVisible()
  await page.getByRole('tab', { name: 'Recognized text' }).click()
  await expect(page.getByText('NORTHSTAR STUDIO', { exact: false })).toBeVisible()
  await page.screenshot({ path: 'test-results/results-desktop.png', fullPage: true })
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('heading', { name: 'Welcome back.' })).toBeVisible()
  expect(errors).toEqual([])
  expect(external).toEqual([])
})

test('mobile workspace and sample result fit the viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await page.getByRole('button', { name: 'Explore demo workspace' }).click()
  await expect(page.getByRole('heading', { name: 'From document to data.' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: 'test-results/workspace-mobile.png', fullPage: true })
  await page.getByRole('button', { name: 'Toggle navigation' }).click()
  await expect(page.getByRole('navigation', { name: 'Main navigation' })).toBeVisible()
  await page.getByRole('link', { name: /New document/ }).click()
  await page.getByRole('button', { name: 'View a sample result' }).click()
  await expect(page.getByText('Completed', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: 'test-results/results-mobile.png', fullPage: true })
})
