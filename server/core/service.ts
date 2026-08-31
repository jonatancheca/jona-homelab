import type { WakeResult } from '../../shared/types/device.ts'
import type { DeviceStore } from './database.ts'
import { AppError } from './errors.ts'

export async function wakeDevice(store: DeviceStore, id: string, send: (mac: string) => Promise<void>): Promise<WakeResult> {
  const target = store.claimWake(id)
  try { await send(target.mac) }
  catch {
    throw new AppError(502, 'The packet could not be sent. Check the network interface and broadcast settings.', 5)
  }
  try {
    const device = store.markSent(id, target.mac)
    return { message: 'Packet sent', device, retryAfter: 5 }
  }
  catch (error) {
    if (error instanceof AppError) throw error
    throw new AppError(500, 'Packet sent, but the timestamp could not be saved. Check storage.')
  }
}
