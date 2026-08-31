import { createSocket, type Socket } from 'node:dgram'
import { normalizeMac } from './validation.ts'
import type { Settings } from './config.ts'

export function createMagicPacket(mac: string): Buffer {
  const address = Buffer.from(normalizeMac(mac).replaceAll(':', ''), 'hex')
  return Buffer.concat([Buffer.alloc(6, 0xff), ...Array.from({ length: 16 }, () => address)])
}

export function sendMagicPacket(
  mac: string,
  settings: Settings['wol'],
  socketFactory: () => Socket = () => createSocket('udp4'),
  timeoutMs = 2000,
): Promise<void> {
  const packet = createMagicPacket(mac)
  return new Promise((resolve, reject) => {
    const socket = socketFactory()
    let settled = false
    const finish = (error?: Error | null) => {
      if (settled) return
      settled = true
      clearTimeout(timeout)
      try { socket.close() }
      catch { /* An unbound socket can already be closed after a bind failure. */ }
      if (error) reject(error)
      else resolve()
    }
    const timeout = setTimeout(() => finish(new Error('UDP send timed out.')), timeoutMs)
    socket.on('error', finish)
    try {
      socket.bind(0, settings.sourceIp || '0.0.0.0', () => {
        if (settled) return
        try {
          socket.setBroadcast(true)
          socket.send(packet, settings.port, settings.broadcast, error => finish(error))
        }
        catch (error) { finish(error instanceof Error ? error : new Error('UDP error.')) }
      })
    }
    catch (error) { finish(error instanceof Error ? error : new Error('UDP error.')) }
  })
}
