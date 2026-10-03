import { test } from 'node:test'
import assert from 'node:assert/strict'
import type { Device, DeviceStatus } from '../../shared/types/device.ts'
import { createReleaseLookup, updateRemoteCompanion, withCompanionRelease } from '../../server/core/companion-updates.ts'
import { checkDeviceStatus, companionResponseSignature, readCompanionStatus } from '../../server/core/remote.ts'

const version = 'main-111111111111'
const oldVersion = 'main-000000000000'
const archive = 'jona-homelab-companion-win-x64.zip'
const device: Device = { id: 'updates-test', name: 'PC', mac: 'AA:BB:CC:DD:EE:FF', address: '192.168.1.2', sshUser: null, remoteMethod: 'companion', companionConfigured: true, createdAt: '', updatedAt: '', lastSentAt: null }
const secret = Buffer.alloc(32, 7).toString('base64url')
const release = { tag_name: version, assets: [archive, `${archive}.sha256`].map(name => ({ name, browser_download_url: `https://github.com/jonatancheca/jona-homelab/releases/download/${version}/${name}` })) }
const status: DeviceStatus = { deviceId: device.id, networkReachable: true, remoteReady: true, remoteMethod: 'companion', checkedAt: '', companion: { version: oldVersion, latestVersion: null, remoteUpdate: true, state: 'unknown' } }

function signedFetcher(handler: (path: string, init?: RequestInit) => Record<string, unknown> | Promise<Record<string, unknown>>): typeof fetch {
  return (async (url, init) => {
    const payload = await handler(new URL(String(url)).pathname, init)
    const body = JSON.stringify(payload)
    const nonce = new Headers(init?.headers).get('X-Jona-Nonce')!
    return new Response(body, { headers: { 'x-jona-response-signature': companionResponseSignature(secret, 200, nonce, body) } })
  }) as typeof fetch
}

test('release lookup coalesces requests, caches and rejects incomplete packages', async () => {
  let calls = 0
  let now = 0
  const lookup = createReleaseLookup((async () => { calls++; return Response.json(calls === 1 ? release : { ...release, assets: [] }) }) as typeof fetch, () => now)
  const results = await Promise.all([lookup(), lookup(), lookup()])
  assert.equal(calls, 1)
  assert.ok(results.every(result => result.version === version))
  await lookup()
  assert.equal(calls, 1)
  now = 300_001
  assert.equal((await lookup()).version, null)
  assert.equal(calls, 2)
  await lookup()
  assert.equal(calls, 2)
})

test('release errors do not change connectivity or claim latest version', () => {
  const result = withCompanionRelease(status, { version: null, checkedAt: '', error: 'unavailable' })
  assert.equal(result.remoteReady, true)
  assert.equal(result.companion?.state, 'unknown')
  assert.equal(result.companion?.releaseError, 'unavailable')
  assert.equal(withCompanionRelease(status, { version, checkedAt: '' }).companion?.state, 'available')
  assert.equal(withCompanionRelease({ ...status, companion: { ...status.companion!, version } }, { version, checkedAt: '' }).companion?.state, 'current')
  assert.equal(withCompanionRelease({ ...status, companion: { ...status.companion!, version: 'local-repair' } }, { version, checkedAt: '' }).companion?.state, 'local')
  assert.equal(withCompanionRelease({ ...status, remoteReady: false }, { version, checkedAt: '' }).companion?.state, 'unknown')
  for (const phase of ['scheduled', 'restarting', 'failed', 'rolled-back'] as const) {
    assert.equal(withCompanionRelease({ ...status, companion: { ...status.companion!, operation: { phase } } }, { version, checkedAt: '' }).companion?.state, ['scheduled', 'restarting'].includes(phase) ? 'updating' : 'failed')
  }
})

test('numbered release metadata preserves the updater identifier and supports old releases', async () => {
  for (const displayVersion of ['1.00', '1.09', '1.100', undefined]) {
    const lookup = createReleaseLookup((async () => Response.json({ ...release, name: displayVersion ? `Jona Homelab ${version} (Companion ${displayVersion})` : `Jona Homelab ${version}` })) as typeof fetch)
    const latest = await lookup()
    assert.equal(latest.version, version)
    assert.equal(latest.displayVersion, displayVersion)
    const result = withCompanionRelease(status, latest)
    assert.equal(result.companion?.latestDisplayVersion, displayVersion)
    assert.equal(result.companion?.state, 'available')
  }
})

test('signed status retains installed version and strips unknown capabilities and fields', async () => {
  const fetcher = signedFetcher(() => ({ ready: true, version: oldVersion, displayVersion: '1.00', remoteUpdate: true, update: { phase: 'failed', error: 'checksum mismatch', pid: 123 } }))
  const reply = await readCompanionStatus(device, secret, fetcher)
  assert.equal(reply.version, oldVersion)
  assert.equal(reply.displayVersion, '1.00')
  assert.equal(reply.remoteUpdate, true)
  assert.equal(reply.operation?.error, 'checksum mismatch')
  assert.equal('pid' in reply.operation!, false)
  const result = await checkDeviceStatus(device, undefined, async () => false, 'win32', secret, async () => reply)
  assert.equal(result.remoteReady, true)
  assert.equal(result.companion?.version, oldVersion)
  assert.equal(result.companion?.displayVersion, '1.00')
  const legacy = await readCompanionStatus(device, secret, signedFetcher(() => ({ ready: true, version: oldVersion })))
  assert.equal(legacy.remoteUpdate, false)
  assert.equal(legacy.version, oldVersion)
  assert.equal(legacy.displayVersion, undefined)
  for (const displayVersion of ['1.0', '<b>1.00</b>', 1, 'main-111111111111']) {
    const invalid = await readCompanionStatus(device, secret, signedFetcher(() => ({ ready: true, version: oldVersion, displayVersion })))
    assert.equal(invalid.displayVersion, undefined)
    assert.equal(invalid.version, oldVersion)
  }
})

test('remote updates reject legacy/local companions and accept only a signed result', async () => {
  for (const payload of [{ ready: true, version: oldVersion }, { ready: true, version: 'local-repair', remoteUpdate: true }]) {
    await assert.rejects(updateRemoteCompanion(device, secret, signedFetcher((path) => { assert.equal(path, '/v1/status'); return payload })))
  }
  const calls: string[] = []
  const result = await updateRemoteCompanion(device, secret, signedFetcher((path, init) => {
    calls.push(path)
    if (path === '/v1/status') return { ready: true, version: oldVersion, remoteUpdate: true }
    assert.equal(init?.method, 'POST')
    assert.equal(init?.body, '{}')
    return { scheduled: true, targetVersion: version }
  }))
  assert.deepEqual(calls, ['/v1/status', '/v1/update'])
  assert.equal(result.scheduled, true)
  assert.equal(result.targetVersion, version)
  await assert.rejects(updateRemoteCompanion(device, secret, (async () => Response.json({ ready: true, remoteUpdate: true, version: oldVersion })) as typeof fetch), /firma/)
})

test('one in-flight remote update per device; failure releases the lock', async () => {
  let releaseRequest!: () => void
  let entered!: () => void
  const pending = new Promise<void>((resolve) => { releaseRequest = resolve })
  const started = new Promise<void>((resolve) => { entered = resolve })
  const fetcher = signedFetcher(async (path) => {
    if (path === '/v1/status') return { ready: true, version: oldVersion, remoteUpdate: true }
    entered(); await pending
    throw new Error('network failed')
  })
  const first = updateRemoteCompanion(device, secret, fetcher)
  await started
  await assert.rejects(updateRemoteCompanion(device, secret, fetcher), /en curso/)
  releaseRequest()
  await assert.rejects(first, /confirmar/)
  const retried = await updateRemoteCompanion(device, secret, signedFetcher(path => path === '/v1/status' ? { ready: true, version: oldVersion, remoteUpdate: true } : { scheduled: false }))
  assert.equal(retried.scheduled, false)
})
