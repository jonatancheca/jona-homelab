import { test, expect } from '@playwright/test'

const oldVersion = 'main-000000000000'
const latestVersion = 'main-111111111111'
const device = { id: 'version-fixture', name: 'Companion de prueba', mac: 'AA:BB:CC:DD:EE:02', address: '192.168.1.2', remoteMethod: 'companion', companionConfigured: true, createdAt: '', updatedAt: '', lastSentAt: null }

test('confirms the new version after restart, keeps pending across disconnect and fits mobile', async ({ page }) => {
  let phase: 'available' | 'scheduled' | 'offline' | 'current' = 'available'
  let requests = 0
  await page.route('**/api/devices', route => route.fulfill({ json: [device] }))
  await page.route('**/api/devices/status', route => route.fulfill({ json: [{
    deviceId: device.id, networkReachable: phase !== 'offline', remoteReady: phase !== 'offline', remoteMethod: 'companion', checkedAt: '',
    companion: { version: phase === 'current' ? latestVersion : phase === 'offline' ? null : oldVersion, latestVersion, remoteUpdate: phase !== 'offline',
      state: phase === 'available' ? 'available' : phase === 'current' ? 'current' : phase === 'offline' ? 'unknown' : 'updating',
      operation: phase === 'scheduled' ? { phase: 'scheduled', targetVersion: latestVersion } : undefined },
  }] }))
  await page.route(`**/api/devices/${device.id}/update`, (route) => { requests++; phase = 'scheduled'; return route.fulfill({ json: { scheduled: true, targetVersion: latestVersion } }) })
  await page.goto('/')
  const section = page.getByRole('region', { name: 'Versión de Companion' })
  await expect(section.getByText('Actualización disponible', { exact: true })).toBeVisible()
  await expect(section.getByText(oldVersion, { exact: true })).toBeVisible()
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `artifacts/companion-update-${width}.png`, fullPage: true })
  }
  await section.getByRole('button', { name: 'Actualizar Companion', exact: true }).click()
  await expect(section.getByRole('button', { name: 'Actualizando…', exact: true })).toBeDisabled()
  await expect(section.getByText('Actualización confirmada:', { exact: false })).toHaveCount(0)
  phase = 'offline'
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect(section.getByText('Esperando a Companion', { exact: true })).toBeVisible()
  phase = 'current'
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect(section.getByText('Actualización confirmada:', { exact: false })).toBeVisible()
  await expect(section.getByText('Actualizado', { exact: true })).toBeVisible()
  expect(requests).toBe(1)
})

test('shows local, legacy, release failure and rollback without claiming success', async ({ page }) => {
  let mode = 'legacy'
  await page.route('**/api/devices', route => route.fulfill({ json: [device] }))
  await page.route('**/api/devices/status', route => route.fulfill({ json: [{ deviceId: device.id, remoteReady: true, networkReachable: true, remoteMethod: 'companion',
    companion: { version: mode === 'local' ? 'local-repair' : oldVersion, latestVersion: mode === 'unknown' ? null : latestVersion,
      remoteUpdate: mode !== 'legacy', state: mode === 'legacy' ? 'available' : mode === 'rollback' ? 'failed' : mode,
      releaseError: mode === 'unknown' ? 'No se pudo comprobar la última versión publicada.' : undefined,
      operation: mode === 'rollback' ? { phase: 'rolled-back', error: 'Checksum incorrecto; versión anterior restaurada.' } : undefined },
  }] }))
  await page.goto('/')
  const section = page.getByRole('region', { name: 'Versión de Companion' })
  await expect(section.getByText('Necesita instalación manual inicial', { exact: false })).toBeVisible()
  await expect(section.getByRole('button')).toHaveCount(0)
  mode = 'local'
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect(section.getByText('Versión local', { exact: true })).toBeVisible()
  await expect(section.getByRole('button')).toHaveCount(0)
  mode = 'unknown'
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect(section.getByRole('button', { name: 'Actualizar Companion' })).toBeDisabled()
  await expect(page.getByText('Companion ready', { exact: true })).toBeVisible()
  mode = 'rollback'
  await page.getByRole('button', { name: 'Refresh status' }).click()
  await expect(section.getByRole('alert')).toContainText('versión anterior restaurada')
  await expect(section.getByText('Actualizado', { exact: true })).toHaveCount(0)
})
