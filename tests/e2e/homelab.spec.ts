import { test, expect } from '@playwright/test'

test('shows focused device workspace without promotional navigation', async ({ page, request }) => {
  const headers = { 'content-type': 'application/json' }
  const name = 'Issue four server'
  const created = await request.post('/api/devices', { headers, data: { name, mac: 'AA:BB:CC:DD:EE:05', address: '192.168.255.5', sshUser: 'jona-homelab-remote' } })
  const device = await created.json()

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'My devices', exact: true })).toBeVisible()
  await expect(page.locator('.sidebar')).toHaveCount(0)
  await expect(page.getByText('No agents. No installation on the target device.', { exact: true })).toHaveCount(0)
  await expect(page.getByText('MY SPACE', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Devices', exact: true })).toHaveCount(0)
  await expect(page.getByText('My space', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Registered devices', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Last packet sent', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Power method', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Small panel.', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Made for your homelab.', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Local inside. Accessible from anywhere.', { exact: true })).toHaveCount(0)
  await expect(page.getByText('A small packet to get everything moving.', { exact: true })).toHaveCount(0)

  const card = page.getByRole('article', { name, exact: true })
  await expect(card.locator('.device-name')).toHaveText(name)
  await expect(card.locator('.device-tag')).toHaveCount(0)
  await expect(card.locator('h3')).toHaveCount(0)

  await request.delete(`/api/devices/${device.id}`, { headers, data: {} })
})

test('register, reject duplicate, edit, wake, search, reload and delete', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Your first device starts here' })).toBeVisible()
  await page.getByRole('button', { name: 'Add device', exact: true }).click()
  const form = page.getByRole('dialog', { name: 'Add device' })
  await form.getByRole('textbox', { name: 'Device name' }).fill('Test server')
  await form.getByRole('textbox', { name: 'MAC address' }).fill('aa-bb-cc-dd-ee-ff')
  await form.getByRole('textbox', { name: 'Private IPv4 address' }).fill('192.168.255.10')
  await form.getByRole('textbox', { name: 'SSH user' }).fill('jona-homelab-remote')
  await form.getByRole('button', { name: 'Add device', exact: true }).click()
  await expect(form).not.toBeVisible()
  const card = page.getByRole('article', { name: 'Test server', exact: true })
  await expect(card.getByText('AA:BB:CC:DD:EE:FF')).toBeVisible()
  await page.getByRole('button', { name: 'Add device', exact: true }).click()
  await form.getByRole('textbox', { name: 'Device name' }).fill('Duplicate')
  await form.getByRole('textbox', { name: 'MAC address' }).fill('aabbccddeeff')
  await form.getByRole('textbox', { name: 'Private IPv4 address' }).fill('192.168.255.11')
  await form.getByRole('textbox', { name: 'SSH user' }).fill('jona-homelab-remote')
  await form.getByRole('button', { name: 'Add device', exact: true }).click()
  await expect(form.getByRole('alert')).toContainText('already registered')
  await form.getByRole('textbox', { name: 'MAC address' }).press('Escape')
  await expect(form).not.toBeVisible()
  await card.getByRole('button', { name: 'Edit Test server' }).click()
  const edit = page.getByRole('dialog', { name: 'Edit device' })
  await edit.getByRole('textbox', { name: 'Device name' }).fill('Updated server')
  await edit.getByRole('button', { name: 'Save changes' }).click()
  const updated = page.getByRole('article', { name: 'Updated server', exact: true })
  await updated.getByRole('button', { name: 'Wake', exact: true }).click()
  await expect(updated.getByRole('status')).toHaveText('Packet sent')
  await expect(updated.getByRole('button', { name: /Wait/ })).toBeDisabled()
  await page.reload()
  await expect(updated).toContainText('Last sent:')
  await page.getByRole('searchbox').fill('no existe')
  await expect(page.getByRole('heading', { name: 'No matches' })).toBeVisible()
  await page.getByRole('button', { name: 'Clear search' }).click()
  await updated.getByRole('button', { name: 'Delete Updated server' }).click()
  await page.getByRole('dialog', { name: 'Delete device?' }).getByRole('button', { name: 'Cancel' }).click()
  await expect(updated).toBeVisible()
  await updated.getByRole('button', { name: 'Delete Updated server' }).click()
  await page.getByRole('dialog', { name: 'Delete device?' }).getByRole('button', { name: 'Delete device', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Your first device starts here' })).toBeVisible()
})

test('API enforces JSON, payload size, IDs, registered MAC and cooldown', async ({ request }) => {
  const headers = { 'content-type': 'application/json' }
  const noOrigin = await request.post('/api/devices', { data: { name: 'No origin', mac: 'aabbccddee01', address: '192.168.255.11', sshUser: 'jona-homelab-remote' } })
  expect(noOrigin.status()).toBe(201)
  const noOriginDevice = await noOrigin.json()
  expect((await request.delete(`/api/devices/${noOriginDevice.id}`, { headers, data: {} })).status()).toBe(204)
  expect((await request.post('/api/devices', { headers: { ...headers, 'content-type': 'text/plain' }, data: '{}' })).status()).toBe(415)
  expect((await request.post('/api/devices', { headers, data: '{' })).status()).toBe(400)
  expect((await request.post('/api/devices', { headers, data: 'x'.repeat(5000) })).status()).toBe(413)
  expect((await request.post('/api/devices', { headers, data: { name: 'Bad', mac: '01:00:5E:00:00:01', address: '192.168.255.12', sshUser: 'jona-homelab-remote' } })).status()).toBe(400)
  expect((await request.post('/api/devices/missing/wake', { headers, data: {} })).status()).toBe(404)
  const created = await request.post('/api/devices', { headers, data: { name: 'API test', mac: 'AA:BB:CC:DD:EE:02', address: '192.168.255.12', sshUser: 'jona-homelab-remote' } })
  expect(created.status()).toBe(201)
  const device = await created.json()
  expect((await request.post(`/api/devices/${device.id}/wake`, { headers, data: { mac: '00:11:22:33:44:55' } })).status()).toBe(200)
  const limited = await request.post(`/api/devices/${device.id}/wake`, { headers, data: {} })
  expect(limited.status()).toBe(429)
  expect(Number(limited.headers()['retry-after'])).toBeGreaterThan(0)
  expect((await request.post(`/api/devices/${device.id}/shutdown`, { headers, data: { force: 'yes' } })).status()).toBe(400)
  expect((await request.delete(`/api/devices/${device.id}`, { headers, data: {} })).status()).toBe(204)
})

test('cards and dialogs fit 320, 390 and 1280px, including long names', async ({ page, request }, testInfo) => {
  const headers = { 'content-type': 'application/json' }
  const name = 'Server-' + 'X'.repeat(70)
  const created = await request.post('/api/devices', { headers, data: { name, mac: 'AA:BB:CC:DD:EE:03', address: '192.168.255.13', sshUser: 'jona-homelab-remote' } })
  const device = await created.json()
  await page.goto('/')
  await expect(page.getByRole('article', { name, exact: true })).toBeVisible()
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`panel-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Add device', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Add device' })
    await expect(dialog).toBeVisible()
    expect(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`dialog-${width}.png`), fullPage: true })
    await dialog.getByRole('button', { name: 'Cancel' }).click()
  }
  await request.delete(`/api/devices/${device.id}`, { headers, data: {} })
})

test('network errors are visible and can be retried', async ({ page }) => {
  await page.route('**/api/devices', route => route.abort())
  await page.goto('/')
  await expect(page.getByRole('heading', { name: "We couldn't load your devices" })).toBeVisible()
  await page.unroute('**/api/devices')
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('heading', { name: 'Your first device starts here' })).toBeVisible()
})

test('failed wake is visible and does not invent a sent timestamp', async ({ page, request }) => {
  const headers = { 'content-type': 'application/json' }
  const created = await request.post('/api/devices', { headers, data: { name: 'Send failure', mac: 'AA:BB:CC:DD:EE:04', address: '192.168.255.14', sshUser: 'jona-homelab-remote' } })
  const device = await created.json()
  await page.route(`**/api/devices/${device.id}/wake`, route => route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ data: { message: 'The packet could not be sent.', retryAfter: 5 } }) }))
  await page.goto('/')
  const card = page.getByRole('article', { name: 'Send failure' })
  await card.getByRole('button', { name: 'Wake', exact: true }).click()
  await expect(card.getByRole('alert')).toContainText('could not be sent')
  await expect(card).toContainText('No packets sent')
  await request.delete(`/api/devices/${device.id}`, { headers, data: {} })
})

test('shows network and SSH status, refreshes manually, and confirms safe or forced shutdown', async ({ page, request }) => {
  const headers = { 'content-type': 'application/json' }
  const created = await request.post('/api/devices', { headers, data: { name: 'Remote Windows', mac: 'AA:BB:CC:DD:EE:06', address: '192.168.255.16', sshUser: 'jona-homelab-remote' } })
  const device = await created.json()
  let statusRequests = 0
  const shutdownModes: boolean[] = []
  await page.route('**/api/devices/status', route => {
    statusRequests++
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{ deviceId: device.id, networkReachable: false, remoteReady: true, remoteMethod: 'ssh', checkedAt: new Date().toISOString() }]),
    })
  })
  await page.route(`**/api/devices/${device.id}/shutdown`, async (route) => {
    shutdownModes.push((route.request().postDataJSON() as { force: boolean }).force)
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'Shutdown command accepted', retryAfter: 10 }) })
  })

  await page.goto('/')
  const card = page.getByRole('article', { name: 'Remote Windows' })
  await expect(card.getByText('Online', { exact: true })).toBeVisible()
  await expect(card.getByText('SSH ready', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect.poll(() => statusRequests).toBeGreaterThan(1)

  await card.getByRole('button', { name: 'Shut down', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Shut down Remote Windows?' })
  await expect(dialog.getByText('Safe shutdown')).toBeVisible()
  await dialog.getByRole('button', { name: 'Shut down safely' }).click()
  await expect(page.getByRole('status')).toContainText('Shutdown command accepted')

  await card.getByRole('button', { name: 'Shut down', exact: true }).click()
  await dialog.getByText('Force shutdown').click()
  await expect(dialog.getByText('Forced shutdown can permanently lose unsaved work.')).toBeVisible()
  await dialog.getByRole('button', { name: 'Force shut down' }).click()
  expect(shutdownModes).toEqual([false, true])
  await request.delete(`/api/devices/${device.id}`, { headers, data: {} })
})

test('registers Companion devices without exposing pairing code and dispatches shutdown', async ({ page, request }) => {
  const headers = { 'content-type': 'application/json' }
  const pairingCode = 'jhcp1_' + 'A'.repeat(43)
  const created = await request.post('/api/devices', { headers, data: { name: 'Companion Windows', mac: 'AA:BB:CC:DD:EE:07', address: '192.168.255.17', remoteMethod: 'companion', companionCode: pairingCode } })
  expect(created.status()).toBe(201)
  const device = await created.json()
  expect(device.remoteMethod).toBe('companion')
  expect(device.companionConfigured).toBe(true)
  expect(device.companionSecret).toBeUndefined()
  await page.route('**/api/devices/status', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([{ deviceId: device.id, networkReachable: true, remoteReady: true, remoteMethod: 'companion', checkedAt: new Date().toISOString() }]) }))
  await page.route(`**/api/devices/${device.id}/shutdown`, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ message: 'Shutdown command accepted', retryAfter: 10 }) }))
  await page.goto('/')
  const card = page.getByRole('article', { name: 'Companion Windows' })
  await expect(card.getByText('Companion ready', { exact: true })).toBeVisible()
  await expect(card.getByText('Companion · 192.168.255.17', { exact: true })).toBeVisible()
  await card.getByRole('button', { name: 'Edit Companion Windows' }).click()
  const form = page.getByRole('dialog', { name: 'Edit device' })
  await expect(form.getByText('Already paired. Leave blank to keep the current code.')).toBeVisible()
  expect(await form.locator('input').evaluateAll((elements, code) => elements.some(element => (element as HTMLInputElement).value === code), pairingCode)).toBe(false)
  await form.getByRole('button', { name: 'Cancel' }).click()
  await card.getByRole('button', { name: 'Shut down', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Shut down Companion Windows?' })).toBeVisible()
  await page.getByRole('dialog', { name: 'Shut down Companion Windows?' }).getByRole('button', { name: 'Shut down safely' }).click()
  await expect(page.getByRole('status')).toContainText('Shutdown command accepted')
  await request.delete(`/api/devices/${device.id}`, { headers, data: {} })
})
