import { isIPv4 } from 'node:net'
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
  if (Object.keys(input).some(key => !['name', 'mac', 'address', 'sshUser'].includes(key))) {
    throw new AppError(400, 'Only name, MAC, IPv4 address and SSH user are allowed.')
  }
  if (typeof input.name !== 'string' || !input.name.trim() || input.name.trim().length > 80) {
    throw new AppError(400, 'The name must be between 1 and 80 characters.')
  }
  // eslint-disable-next-line no-control-regex -- Reject control characters in user-supplied names.
  if (/[\u0000-\u001f\u007f]/.test(input.name)) {
    throw new AppError(400, 'The name contains invalid characters.')
  }
  if (typeof input.address !== 'string' || !isPrivateIPv4(input.address.trim())) {
    throw new AppError(400, 'Enter a private IPv4 address such as 192.168.1.25.')
  }
  if (typeof input.sshUser !== 'string' || !/^[a-z0-9][a-z0-9._-]{0,31}$/i.test(input.sshUser.trim())) {
    throw new AppError(400, 'The SSH user must contain 1 to 32 letters, numbers, dots, dashes or underscores.')
  }
  return {
    name: input.name.trim(),
    mac: normalizeMac(input.mac),
    address: input.address.trim(),
    sshUser: input.sshUser.trim(),
  }
}

function isPrivateIPv4(value: string): boolean {
  if (!isIPv4(value)) return false
  const [first, second] = value.split('.').map(Number)
  return first === 10 || (first === 172 && second! >= 16 && second! <= 31) || (first === 192 && second === 168)
}

export function parseShutdownInput(value: unknown): { force: boolean } {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new AppError(400, 'Invalid shutdown data.')
  const input = value as Record<string, unknown>
  if (Object.keys(input).some(key => key !== 'force') || typeof input.force !== 'boolean') {
    throw new AppError(400, 'Shutdown requires a boolean force value.')
  }
  return { force: input.force }
}

export function parseDeviceId(value: string | undefined): string {
  if (!value || !/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/i.test(value)) {
    throw new AppError(404, 'Device not found.')
  }
  return value
}
