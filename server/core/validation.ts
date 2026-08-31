import type { DeviceInput } from '../../shared/types/device.ts'
import { AppError } from './errors.ts'

export function normalizeMac(value: unknown): string {
  if (typeof value !== 'string') throw new AppError(400, 'Enter a valid MAC address.')
  const input = value.trim()
  if (!/^(?:[\da-f]{12}|(?:[\da-f]{2}:){5}[\da-f]{2}|(?:[\da-f]{2}-){5}[\da-f]{2})$/i.test(input)) {
    throw new AppError(400, 'Invalid MAC address. Use AA:BB:CC:DD:EE:FF.')
  }
  const hex = input.replace(/[:-]/g, '').toUpperCase()
  if (hex === '000000000000' || (parseInt(hex.slice(0, 2), 16) & 1) !== 0) {
    throw new AppError(400, 'Use the Ethernet adapter unicast MAC, not a group address.')
  }
  return hex.match(/.{2}/g)!.join(':')
}

export function parseDeviceInput(value: unknown): DeviceInput {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new AppError(400, 'Invalid device data.')
  }
  const input = value as Record<string, unknown>
  if (Object.keys(input).some(key => key !== 'name' && key !== 'mac')) {
    throw new AppError(400, 'Only name and MAC are allowed.')
  }
  if (typeof input.name !== 'string' || !input.name.trim() || input.name.trim().length > 80) {
    throw new AppError(400, 'The name must be between 1 and 80 characters.')
  }
  // eslint-disable-next-line no-control-regex -- Reject control characters in user-supplied names.
  if (/[\u0000-\u001f\u007f]/.test(input.name)) {
    throw new AppError(400, 'The name contains invalid characters.')
  }
  return { name: input.name.trim(), mac: normalizeMac(input.mac) }
}

export function parseDeviceId(value: string | undefined): string {
  if (!value || !/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/i.test(value)) {
    throw new AppError(404, 'Device not found.')
  }
  return value
}
