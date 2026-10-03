import type { Device, PowerInput, ShutdownResult, WakeResult } from '../../shared/types/device.ts'
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

export async function shutdownDevice(
  store: DeviceStore,
  id: string,
  force: boolean,
  send: (device: Device, force: boolean) => Promise<void>,
): Promise<ShutdownResult> {
  const target = store.claimShutdown(id)
  try { await send(target, force) }
  catch (error) {
    if (error instanceof AppError) throw error
    throw new AppError(502, 'The shutdown command was not accepted. Check device status and remote configuration.', 10)
  }
  return { message: 'Shutdown command accepted', retryAfter: 10 }
}

export async function powerDevice(store: DeviceStore, id: string, input: PowerInput, send: (device: Device, input: PowerInput) => Promise<void>): Promise<ShutdownResult> {
  if (input.action !== 'shutdown' && store.get(id).remoteMethod !== 'companion') {
    throw new AppError(409, 'Sleep and hibernate require Companion.')
  }
  // All power actions share the persisted shutdown cooldown, including the legacy endpoint.
  const target = store.claimShutdown(id)
  try { await send(target, input) }
  catch (error) {
    if (error instanceof AppError) throw error
    throw new AppError(502, 'Power command was not accepted. Check Companion diagnostics and update the Companion if needed.', 10)
  }
  return { message: input.action === 'shutdown' ? 'Shutdown command accepted' : input.action === 'sleep' ? 'Sleep command scheduled' : 'Hibernate command scheduled', retryAfter: 10 }
}
