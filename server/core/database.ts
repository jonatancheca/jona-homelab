import { randomUUID } from 'node:crypto'
import { mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import type { Device, DeviceInput, RemoteMethod } from '../../shared/types/device.ts'
import { companionSecretFromCode } from './validation.ts'
import { AppError } from './errors.ts'

export const WAKE_COOLDOWN_MS = 5000
export const SHUTDOWN_COOLDOWN_MS = 10000

const DATABASE_MIGRATIONS = [
  {
    version: 1,
    sql: `
      CREATE TABLE devices (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        mac TEXT NOT NULL UNIQUE,
        createdAt TEXT NOT NULL,
        updatedAt TEXT NOT NULL,
        lastSentAt TEXT,
        lastAttemptMs INTEGER
      );
    `,
  },
  {
    version: 2,
    sql: 'CREATE INDEX idx_devices_name_nocase ON devices(name COLLATE NOCASE, id);',
  },
  {
    version: 3,
    sql: `
      ALTER TABLE devices ADD COLUMN address TEXT;
      ALTER TABLE devices ADD COLUMN sshUser TEXT;
      ALTER TABLE devices ADD COLUMN lastShutdownAttemptMs INTEGER;
    `,
  },
  {
    version: 4,
    sql: `
      ALTER TABLE devices ADD COLUMN remoteMethod TEXT NOT NULL DEFAULT 'ssh';
      ALTER TABLE devices ADD COLUMN companionSecret TEXT;
    `,
  },
] as const

const CURRENT_DATABASE_VERSION = DATABASE_MIGRATIONS.length

interface DeviceRow {
  id: string
  name: string
  mac: string
  address: string | null
  sshUser: string | null
  remoteMethod: RemoteMethod
  companionSecret: string | null
  createdAt: string
  updatedAt: string
  lastSentAt: string | null
  lastAttemptMs: number | null
  lastShutdownAttemptMs: number | null
}

function publicDevice(row: DeviceRow): Device {
  return {
    id: row.id,
    name: row.name,
    mac: row.mac,
    address: row.address,
    sshUser: row.sshUser,
    remoteMethod: row.remoteMethod === 'companion' ? 'companion' : 'ssh',
    companionConfigured: Boolean(row.companionSecret),
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
    lastSentAt: row.lastSentAt,
  }
}

function readDatabaseVersion(database: DatabaseSync): number {
  const row = database.prepare('PRAGMA user_version').get() as { user_version?: unknown }
  if (typeof row.user_version !== 'number' || !Number.isInteger(row.user_version) || row.user_version < 0) {
    throw new Error('Invalid database version. The schema was not modified.')
  }
  return row.user_version
}

function migrateDatabase(database: DatabaseSync): void {
  let version = readDatabaseVersion(database)
  if (version > CURRENT_DATABASE_VERSION) {
    throw new Error('Unsupported database version. The schema was not modified.')
  }

  database.exec('PRAGMA journal_mode = WAL;')
  while (version < CURRENT_DATABASE_VERSION) {
    const migration = DATABASE_MIGRATIONS[version]
    if (!migration || migration.version !== version + 1) {
      throw new Error(`Missing database migration from version ${version}.`)
    }

    database.exec('BEGIN IMMEDIATE;')
    try {
      database.exec(migration.sql)
      database.exec(`PRAGMA user_version = ${migration.version};`)
      database.exec('COMMIT;')
    }
    catch (error) {
      try { database.exec('ROLLBACK;') }
      catch { /* Preserve the migration error. */ }
      throw error
    }
    version = migration.version
  }
}

export class DeviceStore {
  private database: DatabaseSync

  constructor(path: string) {
    if (path !== ':memory:') mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
    this.database = new DatabaseSync(path, { timeout: 5000 })
    try {
      migrateDatabase(this.database)
    }
    catch (error) {
      this.database.close()
      throw error
    }
  }

  list(): Device[] {
    return (this.database.prepare('SELECT * FROM devices ORDER BY name COLLATE NOCASE, id').all() as unknown as DeviceRow[]).map(publicDevice)
  }

  get(id: string): Device {
    return publicDevice(this.row(id))
  }

  private row(id: string): DeviceRow {
    const row = this.database.prepare('SELECT * FROM devices WHERE id = ?').get(id) as unknown as DeviceRow | undefined
    if (!row) throw new AppError(404, 'Device not found.')
    return row
  }

  create(input: DeviceInput, now = Date.now()): Device {
    const id = randomUUID()
    const timestamp = new Date(now).toISOString()
    const remoteMethod = input.remoteMethod || 'ssh'
    const companionSecret = remoteMethod === 'companion'
      ? companionSecretFromCode(input.companionCode)
      : null
    try {
      this.database.prepare('INSERT INTO devices (id, name, mac, address, sshUser, remoteMethod, companionSecret, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)')
        .run(id, input.name, input.mac, input.address, remoteMethod === 'ssh' ? input.sshUser : null, remoteMethod, companionSecret, timestamp, timestamp)
    }
    catch (error) { this.handleWriteError(error) }
    return this.get(id)
  }

  update(id: string, input: DeviceInput, now = Date.now()): Device {
    const current = this.row(id)
    const remoteMethod = input.remoteMethod || 'ssh'
    const companionSecret = remoteMethod === 'companion'
      ? (input.companionCode?.trim() ? companionSecretFromCode(input.companionCode) : current.remoteMethod === 'companion' ? current.companionSecret : null)
      : null
    if (remoteMethod === 'companion' && !companionSecret) {
      throw new AppError(400, 'Enter the Companion pairing code.')
    }
    try {
      this.database.prepare(`UPDATE devices SET name = ?,
        lastSentAt = CASE WHEN mac = ? THEN lastSentAt ELSE NULL END,
        lastShutdownAttemptMs = CASE WHEN address = ? AND remoteMethod = ? AND sshUser IS ? AND companionSecret IS ? THEN lastShutdownAttemptMs ELSE NULL END,
        mac = ?, address = ?, sshUser = ?, remoteMethod = ?, companionSecret = ?, updatedAt = ? WHERE id = ?`)
        .run(input.name, input.mac, input.address, remoteMethod, remoteMethod === 'ssh' ? input.sshUser : null,
          remoteMethod === 'companion' ? companionSecret : null,
          input.mac, input.address, remoteMethod === 'ssh' ? input.sshUser : null, remoteMethod, companionSecret,
          new Date(now).toISOString(), id)
    }
    catch (error) { this.handleWriteError(error) }
    return this.get(id)
  }

  delete(id: string): void {
    if (!this.database.prepare('DELETE FROM devices WHERE id = ?').run(id).changes) {
      throw new AppError(404, 'Device not found.')
    }
  }

  claimWake(id: string, now = Date.now()): Device {
    // The conditional write also serializes requests from separate processes.
    const result = this.database.prepare(`UPDATE devices SET lastAttemptMs = ?
      WHERE id = ? AND (lastAttemptMs IS NULL OR lastAttemptMs <= ?)`)
      .run(now, id, now - WAKE_COOLDOWN_MS)
    const row = this.row(id)
    if (!result.changes) {
      const retryAfter = Math.max(1, Math.ceil(((row.lastAttemptMs ?? now) + WAKE_COOLDOWN_MS - now) / 1000))
      throw new AppError(429, `Wait ${retryAfter} seconds before sending again.`, retryAfter)
    }
    return publicDevice(row)
  }

  markSent(id: string, mac: string, now = Date.now()): Device {
    if (!this.database.prepare('UPDATE devices SET lastSentAt = ? WHERE id = ? AND mac = ?')
      .run(new Date(now).toISOString(), id, mac).changes) {
      throw new AppError(409, 'Packet sent, but the device changed during sending. Refresh the list.')
    }
    return this.get(id)
  }

  claimShutdown(id: string, now = Date.now()): Device {
    const row = this.row(id)
    if (!row.address) throw new AppError(409, 'Configure the device private IPv4 address first.')
    if (row.remoteMethod === 'ssh' && !row.sshUser) throw new AppError(409, 'Configure the device SSH user first.')
    if (row.remoteMethod === 'companion' && !row.companionSecret) throw new AppError(409, 'Configure the Companion pairing code first.')
    const result = this.database.prepare(`UPDATE devices SET lastShutdownAttemptMs = ?
      WHERE id = ? AND (lastShutdownAttemptMs IS NULL OR lastShutdownAttemptMs <= ?)`)
      .run(now, id, now - SHUTDOWN_COOLDOWN_MS)
    if (!result.changes) {
      const retryAfter = Math.max(1, Math.ceil(((row.lastShutdownAttemptMs ?? now) + SHUTDOWN_COOLDOWN_MS - now) / 1000))
      throw new AppError(429, `Wait ${retryAfter} seconds before trying shutdown again.`, retryAfter)
    }
    return publicDevice(row)
  }

  companionSecret(id: string): string {
    const row = this.row(id)
    if (row.remoteMethod !== 'companion' || !row.companionSecret) {
      throw new AppError(409, 'Configure the Companion pairing code first.')
    }
    return row.companionSecret
  }

  companionSecretOrNull(id: string): string | null {
    const row = this.row(id)
    return row.remoteMethod === 'companion' ? row.companionSecret : null
  }

  close(): void { this.database.close() }

  private handleWriteError(error: unknown): never {
    if (error instanceof Error && error.message.includes('UNIQUE constraint failed: devices.mac')) {
      throw new AppError(409, 'A device with that MAC is already registered.')
    }
    throw error
  }
}
