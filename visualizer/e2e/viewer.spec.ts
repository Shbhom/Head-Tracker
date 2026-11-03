import { test, expect, type WebSocketRoute } from '@playwright/test'

test('renders a quaternion, marks stale data, and disconnects cleanly', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let socket: WebSocketRoute | undefined
  await page.routeWebSocket('ws://127.0.0.1:8080/ws/orientation', ws => {
    socket = ws
    ws.send(JSON.stringify({ type: 'status', status: 'connected', message: 'Waiting for the first quaternion' }))
  })
  await page.goto('/')
  await expect(page.getByText('Not connected', { exact: true })).toBeVisible()
  await expect(page.locator('canvas')).toBeVisible()
  await page.getByRole('button', { name: 'Connect stream' }).click()
  await expect(page.getByText('Waiting for sensor', { exact: true })).toBeVisible()
  socket!.send(JSON.stringify({ type: 'orientation', timestamp: new Date().toISOString(), sequence: 42,
    quaternion: { x: 0.7324, y: -0.4373, z: 0.5217, w: 0.0001 } }))
  await expect(page.getByText('Live orientation', { exact: true })).toBeVisible()
  await expect(page.getByText('0.7324', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Set current pose as forward' }).click()
  await expect(page.getByText(/Relative to your saved pose/)).toBeVisible()
  await page.getByRole('button', { name: 'Reset camera' }).click()
  await page.screenshot({ path: 'test-results/viewer-live.png' })
  socket!.send('invalid JSON')
  await expect(page.locator('dl > div').filter({ hasText: 'Rejected messages' }).locator('dd')).toHaveText('1')
  await expect(page.getByText('Data stale', { exact: true })).toBeVisible({ timeout: 5000 })
  await expect(page.getByRole('button', { name: 'Set current pose as forward' })).toBeDisabled()
  await page.screenshot({ path: 'test-results/viewer-stale.png' })
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click()
  await expect(page.getByText('Not connected', { exact: true })).toBeVisible()
  expect(errors).toEqual([])
})

test('reconnects after server closure but not after manual disconnect', async ({ page }) => {
  const sockets: WebSocketRoute[] = []
  await page.routeWebSocket('ws://127.0.0.1:8080/ws/orientation', ws => {
    sockets.push(ws)
    ws.send(JSON.stringify({ type: 'status', status: 'connected' }))
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Connect stream' }).click()
  await expect(page.getByText('Waiting for sensor', { exact: true })).toBeVisible()
  sockets[0].send(JSON.stringify({ type: 'orientation', timestamp: new Date().toISOString(), sequence: 1,
    quaternion: { x: 0, y: 0, z: 0, w: 1 } }))
  await expect(page.getByText('Live orientation', { exact: true })).toBeVisible()
  sockets[0].close({ code: 1011, reason: 'test interruption' })
  await expect(page.getByText('Reconnecting', { exact: true })).toBeVisible()
  await expect.poll(() => sockets.length).toBe(2)
  await expect(page.getByText('Waiting for sensor', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click()
  await page.waitForTimeout(1200)
  expect(sockets.length).toBe(2)
})

test('supports a narrow screen and does not auto-connect or persist secrets', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await page.getByLabel('API token').fill('a'.repeat(64))
  await page.reload()
  await expect(page.getByLabel('API token')).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Connect stream' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
