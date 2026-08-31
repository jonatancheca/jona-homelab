import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { DeviceStore } from '../../server/core/database.ts'
import { wakeDevice } from '../../server/core/service.ts'

const input = { name: 'Server', mac: 'AA:BB:CC:DD:EE:FF' }

test('CRUD, SQL parameterization, uniqueness and missing devices', () => {
  const store = new DeviceStore(':memory:')
  try {
    assert.deepEqual(store.list(), [])
    const device = store.create(input)
    assert.equal(device.lastSentAt, null)
    assert.throws(() => store.create(input), { statusCode: 409 })
    const second = store.create({ name: 'PC', mac: 'AA:BB:CC:DD:EE:00' })
    assert.throws(() => store.update(second.id, input), { statusCode: 409 })
    assert.equal(store.update(device.id, { ...input, name: "'; DROP TABLE devices; --" }).name, "'; DROP TABLE devices; --")
    assert.equal(store.list().length, 2)
    store.delete(device.id)
    assert.throws(() => store.get(device.id), { statusCode: 404 })
    assert.throws(() => store.update(device.id, input), { statusCode: 404 })
    assert.throws(() => store.delete(device.id), { statusCode: 404 })
    assert.throws(() => store.claimWake(device.id), { statusCode: 404 })
  }
  finally { store.close() }
})

test('persists data and cooldown across connections and restarts', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-db-test-'))
  const path = join(directory, 'data.sqlite')
  let first: DeviceStore | undefined
  let second: DeviceStore | undefined
  try {
    first = new DeviceStore(path)
    const device = first.create(input)
    first.claimWake(device.id, 10000)
    second = new DeviceStore(path)
    assert.throws(() => second!.claimWake(device.id, 10001), { statusCode: 429, retryAfter: 5 })
    first.close()
    first = undefined
    second.close()
    second = new DeviceStore(path)
    assert.equal(second.get(device.id).mac, input.mac)
    assert.throws(() => second!.claimWake(device.id, 14999), { statusCode: 429, retryAfter: 1 })
    assert.equal(second.claimWake(device.id, 15000).id, device.id)
  }
  finally { first?.close(); second?.close(); rmSync(directory, { recursive: true, force: true }) }
})

test('does not downgrade a database from a newer release', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-version-test-'))
  try {
    const path = join(directory, 'newer.sqlite')
    const database = new DatabaseSync(path)
    database.exec('PRAGMA user_version = 2')
    database.close()
    assert.throws(() => new DeviceStore(path), /Unsupported database version/)
  }
  finally { rmSync(directory, { recursive: true, force: true }) }
})

test('wake only uses saved MAC, records success and serializes concurrent calls', async () => {
  const store = new DeviceStore(':memory:')
  try {
    const device = store.create(input)
    let sentMac = ''
    let release!: () => void
    const send = async (mac: string) => { sentMac = mac; await new Promise<void>((resolve) => { release = resolve }) }
    const first = wakeDevice(store, device.id, send)
    await assert.rejects(wakeDevice(store, device.id, send), { statusCode: 429 })
    assert.equal(store.get(device.id).lastSentAt, null)
    release()
    const result = await first
    assert.equal(sentMac, input.mac)
    assert.equal(result.message, 'Packet sent')
    assert.ok(result.device.lastSentAt)
  }
  finally { store.close() }
})

test('UDP failure does not claim success or update sent timestamp', async () => {
  const store = new DeviceStore(':memory:')
  try {
    const device = store.create(input)
    await assert.rejects(wakeDevice(store, device.id, async () => { throw new Error('EACCES') }), { statusCode: 502 })
    assert.equal(store.get(device.id).lastSentAt, null)
    await assert.rejects(wakeDevice(store, device.id, async () => {}), { statusCode: 429 })
  }
  finally { store.close() }
})

test('a MAC changed during send is not marked as sent', () => {
  const store = new DeviceStore(':memory:')
  try {
    const device = store.create(input)
    store.markSent(device.id, input.mac)
    store.update(device.id, { ...input, mac: 'AA:BB:CC:DD:EE:00' })
    assert.throws(() => store.markSent(device.id, input.mac), { statusCode: 409 })
    assert.equal(store.get(device.id).lastSentAt, null)
  }
  finally { store.close() }
})
