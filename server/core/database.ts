import { randomUUID } from 'node:crypto'
import { mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import type { Device, DeviceInput } from '../../shared/types/device.ts'
import { AppError } from './errors.ts'

export const WAKE_COOLDOWN_MS = 5000

interface DeviceRow extends Device {
  lastAttemptMs: number | null
}

function publicDevice(row: DeviceRow): Device {
  return { id: row.id, name: row.name, mac: row.mac, createdAt: row.createdAt, updatedAt: row.updatedAt, lastSentAt: row.lastSentAt }
}

export class DeviceStore {
  private database: DatabaseSync

  constructor(path: string) {
    if (path !== ':memory:') mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
    this.database = new DatabaseSync(path, { timeout: 5000 })
    try {
      this.database.exec('PRAGMA journal_mode = WAL;')
      const version = this.database.prepare('PRAGMA user_version').get()!.user_version
      if (version === 0) {
        this.database.exec(`
          BEGIN IMMEDIATE;
          CREATE TABLE devices (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            mac TEXT NOT NULL UNIQUE,
            createdAt TEXT NOT NULL,
            updatedAt TEXT NOT NULL,
            lastSentAt TEXT,
            lastAttemptMs INTEGER
          );
          PRAGMA user_version = 1;
          COMMIT;
        `)
      }
      else if (version !== 1) throw new Error('Unsupported database version. The schema was not modified.')
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
    try {
      this.database.prepare('INSERT INTO devices (id, name, mac, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?)')
        .run(id, input.name, input.mac, timestamp, timestamp)
    }
    catch (error) { this.handleWriteError(error) }
    return this.get(id)
  }

  update(id: string, input: DeviceInput, now = Date.now()): Device {
    this.get(id)
    try {
      this.database.prepare(`UPDATE devices SET name = ?,
        lastSentAt = CASE WHEN mac = ? THEN lastSentAt ELSE NULL END,
        mac = ?, updatedAt = ? WHERE id = ?`)
        .run(input.name, input.mac, input.mac, new Date(now).toISOString(), id)
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

  close(): void { this.database.close() }

  private handleWriteError(error: unknown): never {
    if (error instanceof Error && error.message.includes('UNIQUE constraint failed: devices.mac')) {
      throw new AppError(409, 'A device with that MAC is already registered.')
    }
    throw error
  }
}
