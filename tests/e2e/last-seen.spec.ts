import { test, expect } from '@playwright/test'
import type { Device, DeviceStatus } from '../../shared/types/device'

test('shows the last confirmed online time only when a checked device has no response', async ({ page }) => {
  const device: Device = {
    id: 'last-seen-fixture', name: 'Equipo de prueba', mac: 'AA:BB:CC:DD:EE:08',
    address: 'TEST-PC', sshUser: null, remoteMethod: 'none', companionConfigured: false,
    createdAt: '', updatedAt: '', lastSentAt: '2026-09-30T07:00:00.000Z', lastSeenAt: null,
  }
  let status: DeviceStatus = {
    deviceId: device.id, networkReachable: false, remoteReady: false, remoteMethod: 'none',
    checkedAt: '2026-10-01T09:00:00.000Z', lastSeenAt: null,
  }
  let finishInitialCheck!: () => void
  const initialCheck = new Promise<void>(resolve => { finishInitialCheck = resolve })
  await page.route('**/api/devices', route => route.fulfill({ json: [device] }))
  await page.route('**/api/devices/status', async (route) => {
    await initialCheck
    await route.fulfill({ json: [status] })
  })
  await page.goto('/')
  const card = page.getByRole('article', { name: device.name })
  const lastSeen = card.locator('.last-seen')
  await expect(card.getByText('Checking…', { exact: true })).toBeVisible()
  await expect(lastSeen).toHaveCount(0)
  finishInitialCheck()
  await expect(lastSeen).toHaveText('Última vez visto encendido: Sin registros')
  await expect(card.getByText('No response', { exact: true })).toBeVisible()

  const refresh = page.getByRole('button', { name: 'Refresh status' })
  const seen = '2026-10-01T08:30:00.000Z'
  status = { ...status, networkReachable: true, lastSeenAt: seen }
  await refresh.click()
  await expect(card.getByText('Online', { exact: true })).toBeVisible()
  await expect(lastSeen).toHaveCount(0)

  status = { ...status, networkReachable: false }
  await refresh.click()
  await expect(lastSeen.locator('time')).toHaveAttribute('datetime', seen)
  await expect(lastSeen).toContainText('01/10/2026')
  await expect(card.locator('.last-sent')).toContainText('Last sent:')
  const savedLabel = await lastSeen.innerText()
  await page.reload()
  await expect(lastSeen).toHaveText(savedLabel)
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await expect(lastSeen).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `artifacts/issue-8-${width}.png`, fullPage: true })
  }

  status = { ...status, remoteMethod: 'ssh', remoteReady: true }
  await refresh.click()
  await expect(card.getByText('Online', { exact: true })).toBeVisible()
  await expect(lastSeen).toHaveCount(0)
  status = { ...status, remoteReady: false, lastSeenAt: '2026-10-02T08:30:00.000Z' }
  await refresh.click()
  await expect(lastSeen.locator('time')).toHaveAttribute('datetime', status.lastSeenAt!)
  device.address = null
  await page.reload()
  await expect(card).toBeVisible()
  await expect(lastSeen).toHaveCount(0)
})

test('status API persists a real loopback observation in the device list', async ({ request }) => {
  const headers = { 'content-type': 'application/json' }
  const created = await request.post('/api/devices', {
    headers, data: { name: 'Last seen loopback', mac: 'AA:BB:CC:DD:EE:18', address: 'localhost', remoteMethod: 'none' },
  })
  expect(created.status()).toBe(201)
  const device: Device = await created.json()
  try {
    expect(device.lastSeenAt).toBeNull()
    const response = await request.get('/api/devices/status')
    expect(response.status()).toBe(200)
    const statuses: DeviceStatus[] = await response.json()
    const status = statuses.find(item => item.deviceId === device.id)!
    expect(status.networkReachable).toBe(true)
    expect(status.remoteReady).toBe(false)
    expect(status.lastSeenAt).toBe(status.checkedAt)
    const devices: Device[] = await (await request.get('/api/devices')).json()
    const persisted = devices.find(item => item.id === device.id)!
    expect(persisted.lastSeenAt).toBe(status.checkedAt)
    expect(persisted.lastSentAt).toBeNull()
    expect(persisted.updatedAt).toBe(device.updatedAt)
  }
  finally { await request.delete(`/api/devices/${device.id}`, { headers, data: {} }) }
})
