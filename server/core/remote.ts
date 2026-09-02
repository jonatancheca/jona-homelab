import { execFile } from 'node:child_process'
import { createHash, createHmac, randomBytes, timingSafeEqual } from 'node:crypto'
import type { Device, DeviceStatus } from '../../shared/types/device.ts'
import type { Settings } from './config.ts'
import { AppError } from './errors.ts'

const PING_TIMEOUT_MS = 2000
const SSH_TIMEOUT_MS = 6000
export const COMPANION_PORT = 47654
const COMPANION_TIMEOUT_MS = 6000
const MAX_OUTPUT_BYTES = 64 * 1024

export type CommandRunner = (command: string, args: string[], timeoutMs: number) => Promise<boolean>
export type CompanionCommand = 'status' | 'shutdown-safe' | 'shutdown-force'
export type CompanionRunner = (device: Device, secret: string, command: CompanionCommand) => Promise<boolean>

export const runCommand: CommandRunner = (command, args, timeoutMs) => new Promise((resolve) => {
  execFile(command, args, {
    timeout: timeoutMs,
    maxBuffer: MAX_OUTPUT_BYTES,
    windowsHide: true,
    shell: false,
  }, error => resolve(!error))
})

export function pingArguments(address: string, platform: NodeJS.Platform = process.platform): string[] {
  return platform === 'win32'
    ? ['-n', '1', '-w', String(PING_TIMEOUT_MS), address]
    : ['-c', '1', '-W', String(Math.ceil(PING_TIMEOUT_MS / 1000)), address]
}

export function sshArguments(
  device: Pick<Device, 'address' | 'sshUser'>,
  ssh: NonNullable<Settings['ssh']>,
  command: 'status' | 'shutdown-safe' | 'shutdown-force',
): string[] {
  if (!device.address || !device.sshUser) throw new AppError(409, 'Configure the device IPv4 address and SSH user first.')
  return [
    '-T',
    '-o', 'BatchMode=yes',
    '-o', 'PasswordAuthentication=no',
    '-o', 'ConnectionAttempts=1',
    '-o', 'ConnectTimeout=5',
    '-o', 'StrictHostKeyChecking=yes',
    '-o', `UserKnownHostsFile=${ssh.knownHostsFile}`,
    '-o', 'IdentitiesOnly=yes',
    '-o', 'LogLevel=ERROR',
    '-i', ssh.identityFile,
    '-p', String(ssh.port),
    `${device.sshUser}@${device.address}`,
    command,
  ]
}

function decodeCompanionSecret(secret: string): Buffer {
  const decoded = Buffer.from(secret, 'base64url')
  if (decoded.length !== 32) throw new AppError(500, 'Stored Companion secret is invalid.')
  return decoded
}

function bodyHash(body: string): string { return createHash('sha256').update(body).digest('hex') }

export function companionRequestSignature(secret: string, method: string, path: string, timestamp: number, nonce: string, body: string): string {
  const canonical = `${method.toUpperCase()}\n${path}\n${timestamp}\n${nonce}\n${bodyHash(body)}`
  return createHmac('sha256', decodeCompanionSecret(secret)).update(canonical).digest('hex')
}

export function companionResponseSignature(secret: string, status: number, nonce: string, body: string): string {
  return createHmac('sha256', decodeCompanionSecret(secret)).update(`${status}\n${nonce}\n${bodyHash(body)}`).digest('hex')
}

function equalSignature(actual: string | null, expected: string): boolean {
  if (!actual || !/^[\da-f]{64}$/i.test(actual)) return false
  return timingSafeEqual(Buffer.from(actual.toLowerCase(), 'ascii'), Buffer.from(expected, 'ascii'))
}

export async function requestCompanion(device: Device, secret: string, command: CompanionCommand, fetcher: typeof fetch = fetch, now = Date.now): Promise<boolean> {
  if (!device.address) throw new AppError(409, 'Configure the device private IPv4 address first.')
  const path = command === 'status' ? '/v1/status' : '/v1/shutdown'
  const method = command === 'status' ? 'GET' : 'POST'
  const body = command === 'status' ? '' : JSON.stringify({ force: command === 'shutdown-force' })
  const timestamp = Math.floor(now() / 1000)
  const nonce = randomBytes(16).toString('base64url')
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), COMPANION_TIMEOUT_MS)
  try {
    const signature = companionRequestSignature(secret, method, path, timestamp, nonce, body)
    const response = await fetcher(`http://${device.address}:${COMPANION_PORT}${path}`, {
      method,
      headers: {
        Accept: 'application/json',
        ...(body ? { 'Content-Type': 'application/json' } : {}),
        'X-Jona-Timestamp': String(timestamp),
        'X-Jona-Nonce': nonce,
        'X-Jona-Signature': signature,
      },
      body: body || undefined,
      signal: controller.signal,
    })
    const responseBody = await response.text()
    const responseSignature = response.headers.get('x-jona-response-signature')
    if (!equalSignature(responseSignature, companionResponseSignature(secret, response.status, nonce, responseBody))) return false
    if (!response.ok) return false
    const payload = JSON.parse(responseBody) as { ready?: unknown, accepted?: unknown }
    return command === 'status' ? payload.ready === true : payload.accepted === true
  }
  catch { return false }
  finally { clearTimeout(timeout) }
}

export async function checkDeviceStatus(
  device: Device,
  ssh: Settings['ssh'],
  runner: CommandRunner = runCommand,
  platform: NodeJS.Platform = process.platform,
  companionSecret?: string,
  companionRunner: CompanionRunner = requestCompanion,
): Promise<DeviceStatus> {
  const remoteMethod = device.remoteMethod || 'ssh'
  const network = device.address
    ? runner('ping', pingArguments(device.address, platform), PING_TIMEOUT_MS + 1000)
    : Promise.resolve(false)
  const remote = remoteMethod === 'companion' && device.address && companionSecret
    ? companionRunner(device, companionSecret, 'status')
    : remoteMethod === 'ssh' && ssh && device.address && device.sshUser
    ? runner('ssh', sshArguments(device, ssh, 'status'), SSH_TIMEOUT_MS)
    : Promise.resolve(false)
  const [networkReachable, remoteReady] = await Promise.all([network, remote])
  return { deviceId: device.id, networkReachable, remoteReady, remoteMethod, checkedAt: new Date().toISOString() }
}

export async function checkDevicesStatus(
  devices: Device[],
  ssh: Settings['ssh'],
  companionSecretOrConcurrency: ((device: Device) => string | null) | number = () => null,
  concurrency = 4,
): Promise<DeviceStatus[]> {
  const companionSecret = typeof companionSecretOrConcurrency === 'function' ? companionSecretOrConcurrency : () => null
  const workerCount = typeof companionSecretOrConcurrency === 'number' ? companionSecretOrConcurrency : concurrency
  const statuses = new Array<DeviceStatus>(devices.length)
  let next = 0
  const worker = async () => {
    while (next < devices.length) {
      const index = next++
      const device = devices[index]!
      statuses[index] = await checkDeviceStatus(device, ssh, runCommand, process.platform, companionSecret(device) ?? undefined)
    }
  }
  await Promise.all(Array.from({ length: Math.min(workerCount, devices.length) }, worker))
  return statuses
}

export async function sendShutdownCommand(
  device: Device,
  force: boolean,
  ssh: Settings['ssh'],
  runner: CommandRunner = runCommand,
  companionSecret?: string,
  companionRunner: CompanionRunner = requestCompanion,
): Promise<void> {
  if ((device.remoteMethod || 'ssh') === 'companion') {
    if (!companionSecret) throw new AppError(503, 'Companion shutdown is not configured on the homelab server.')
    const accepted = await companionRunner(device, companionSecret, force ? 'shutdown-force' : 'shutdown-safe')
    if (!accepted) throw new Error('Companion command failed')
    return
  }
  if (!ssh) throw new AppError(503, 'SSH shutdown is not configured on the homelab server.')
  const accepted = await runner('ssh', sshArguments(device, ssh, force ? 'shutdown-force' : 'shutdown-safe'), SSH_TIMEOUT_MS)
  if (!accepted) throw new Error('SSH command failed')
}
