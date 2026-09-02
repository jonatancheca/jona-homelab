import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { DeviceStore } from '../../server/core/database.ts'
import { shutdownDevice, wakeDevice } from '../../server/core/service.ts'

const input = { name: 'Server', mac: 'AA:BB:CC:DD:EE:FF', address: '192.168.1.25', sshUser: 'jona-homelab-remote' }

function databaseVersion(database: DatabaseSync): number {
  return database.prepare('PRAGMA user_version').get()!.user_version as number
}

function createVersionOneDatabase(path: string): void {
  const database = new DatabaseSync(path)
  database.exec(`
    CREATE TABLE devices (
      id TEXT PRIMARY KEY,
      name TEXT NOT NULL,
      mac TEXT NOT NULL UNIQUE,
      createdAt TEXT NOT NULL,
      updatedAt TEXT NOT NULL,
      lastSentAt TEXT,
      lastAttemptMs INTEGER
    );
    INSERT INTO devices VALUES (
      'legacy', 'Legacy PC', 'AA:BB:CC:DD:EE:01',
      '2026-01-01T00:00:00.000Z', '2026-01-02T00:00:00.000Z',
      '2026-01-03T00:00:00.000Z', 1000
    );
    PRAGMA user_version = 1;
  `)
  database.close()
}

function createVersionTwoDatabase(path: string): void {
  createVersionOneDatabase(path)
  const database = new DatabaseSync(path)
  database.exec(`
    CREATE INDEX idx_devices_name_nocase ON devices(name COLLATE NOCASE, id);
    PRAGMA user_version = 2;
  `)
  database.close()
}

test('CRUD, SQL parameterization, uniqueness and missing devices', () => {
  const store = new DeviceStore(':memory:')
  try {
    assert.deepEqual(store.list(), [])
    const device = store.create(input)
    assert.equal(device.lastSentAt, null)
    assert.throws(() => store.create(input), { statusCode: 409 })
    const second = store.create({ ...input, name: 'PC', mac: 'AA:BB:CC:DD:EE:00', address: '192.168.1.26' })
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
  finally { first?.close(); second?.close(); rmSync(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 }) }
})

test('creates the current schema for a new database', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-new-version-test-'))
  const path = join(directory, 'new.sqlite')
  try {
    const store = new DeviceStore(path)
    store.close()
    const database = new DatabaseSync(path)
    assert.equal(databaseVersion(database), 4)
    assert.equal(
      database.prepare("SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?")
        .get('idx_devices_name_nocase')!.name,
      'idx_devices_name_nocase',
    )
    database.close()
  }
  finally { rmSync(directory, { recursive: true, force: true }) }
})

test('migrates version 1 to 3 without losing devices and is idempotent', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-migration-test-'))
  const path = join(directory, 'legacy.sqlite')
  let first: DeviceStore | undefined
  let second: DeviceStore | undefined
  try {
    createVersionOneDatabase(path)
    first = new DeviceStore(path)
    assert.deepEqual(first.list(), [{
      id: 'legacy',
      name: 'Legacy PC',
      mac: 'AA:BB:CC:DD:EE:01',
      address: null,
      sshUser: null,
      remoteMethod: 'ssh',
      companionConfigured: false,
      createdAt: '2026-01-01T00:00:00.000Z',
      updatedAt: '2026-01-02T00:00:00.000Z',
      lastSentAt: '2026-01-03T00:00:00.000Z',
    }])
    assert.throws(() => first!.claimWake('legacy', 1001), { statusCode: 429, retryAfter: 5 })
    first.close()
    first = undefined

    second = new DeviceStore(path)
    assert.equal(second.list().length, 1)
    second.close()
    second = undefined

    const database = new DatabaseSync(path)
    assert.equal(databaseVersion(database), 4)
    assert.equal(
      database.prepare("SELECT COUNT(*) AS count FROM sqlite_master WHERE type = 'index' AND name = ?")
        .get('idx_devices_name_nocase')!.count,
      1,
    )
    assert.equal(database.prepare('SELECT lastAttemptMs FROM devices WHERE id = ?').get('legacy')!.lastAttemptMs, 1000)
    const remote = database.prepare('SELECT address, sshUser, lastShutdownAttemptMs FROM devices WHERE id = ?').get('legacy')!
    assert.equal(remote.address, null)
    assert.equal(remote.sshUser, null)
    assert.equal(remote.lastShutdownAttemptMs, null)
    database.close()
  }
  finally { first?.close(); second?.close(); rmSync(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 }) }
})

test('rolls back a failed migration and keeps its previous version', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-failed-migration-test-'))
  const path = join(directory, 'invalid-v1.sqlite')
  try {
    const database = new DatabaseSync(path)
    database.exec('PRAGMA user_version = 1')
    database.close()

    assert.throws(() => new DeviceStore(path), /no such table/)
    const unchanged = new DatabaseSync(path)
    assert.equal(databaseVersion(unchanged), 1)
    assert.equal(
      unchanged.prepare("SELECT COUNT(*) AS count FROM sqlite_master WHERE type = 'index' AND name = ?")
        .get('idx_devices_name_nocase')!.count,
      0,
    )
    unchanged.close()
  }
  finally { rmSync(directory, { recursive: true, force: true }) }
})

test('does not modify a database from a newer release', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-version-test-'))
  try {
    const path = join(directory, 'newer.sqlite')
    const database = new DatabaseSync(path)
    database.exec(`
      CREATE TABLE sentinel (value TEXT NOT NULL);
      INSERT INTO sentinel VALUES ('preserved');
      PRAGMA user_version = 5;
    `)
    database.close()
    assert.throws(() => new DeviceStore(path), /Unsupported database version/)
    const unchanged = new DatabaseSync(path)
    assert.equal(databaseVersion(unchanged), 5)
    assert.equal(unchanged.prepare('SELECT value FROM sentinel').get()!.value, 'preserved')
    assert.equal(unchanged.prepare('PRAGMA journal_mode').get()!.journal_mode, 'delete')
    unchanged.close()
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

test('migrates version 2 to 3 with nullable remote fields', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-v2-migration-test-'))
  const path = join(directory, 'v2.sqlite')
  let store: DeviceStore | undefined
  try {
    createVersionTwoDatabase(path)
    store = new DeviceStore(path)
    assert.equal(store.get('legacy').address, null)
    assert.equal(store.get('legacy').sshUser, null)
    store.close()
    store = undefined
    const database = new DatabaseSync(path)
    assert.equal(databaseVersion(database), 4)
    assert.equal(database.prepare('SELECT lastShutdownAttemptMs FROM devices WHERE id = ?').get('legacy')!.lastShutdownAttemptMs, null)
    database.close()
  }
  finally { store?.close(); rmSync(directory, { recursive: true, force: true }) }
})

test('shutdown uses saved target, maps safe and forced modes, and persists cooldown', async () => {
  const store = new DeviceStore(':memory:')
  try {
    const device = store.create(input)
    const calls: Array<{ address: string | null, sshUser: string | null, force: boolean }> = []
    const send = async (target: typeof device, force: boolean) => { calls.push({ address: target.address, sshUser: target.sshUser, force }) }
    assert.deepEqual(await shutdownDevice(store, device.id, false, send), { message: 'Shutdown command accepted', retryAfter: 10 })
    assert.deepEqual(calls, [{ address: input.address, sshUser: input.sshUser, force: false }])
    await assert.rejects(shutdownDevice(store, device.id, true, send), { statusCode: 429, retryAfter: 10 })
  }
  finally { store.close() }
})

test('stores Companion secret privately, preserves it on blank edit and clears it when switching to SSH', () => {
  const store = new DeviceStore(':memory:')
  try {
    const code = 'jhcp1_' + 'B'.repeat(43)
    const companion = store.create({ name: 'Companion', mac: 'AA:BB:CC:DD:EE:11', address: '192.168.1.30', remoteMethod: 'companion', sshUser: null, companionCode: code })
    assert.equal(companion.remoteMethod, 'companion')
    assert.equal(companion.companionConfigured, true)
    assert.equal('companionSecret' in companion, false)
    assert.equal(store.companionSecret(companion.id), 'B'.repeat(43))
    const preserved = store.update(companion.id, { name: companion.name, mac: companion.mac, address: companion.address!, remoteMethod: 'companion', sshUser: null })
    assert.equal(preserved.companionConfigured, true)
    store.update(companion.id, { ...input, remoteMethod: 'ssh' })
    assert.equal(store.get(companion.id).remoteMethod, 'ssh')
    assert.equal(store.get(companion.id).companionConfigured, false)
    assert.throws(() => store.companionSecret(companion.id), { statusCode: 409 })
  }
  finally { store.close() }
})

test('legacy devices require remote configuration before shutdown', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-legacy-shutdown-test-'))
  const path = join(directory, 'legacy.sqlite')
  let store: DeviceStore | undefined
  try {
    createVersionOneDatabase(path)
    store = new DeviceStore(path)
    await assert.rejects(shutdownDevice(store, 'legacy', false, async () => {}), { statusCode: 409 })
  }
  finally { store?.close(); rmSync(directory, { recursive: true, force: true }) }
})
