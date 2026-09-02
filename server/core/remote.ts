import { execFile } from 'node:child_process'
import type { Device, DeviceStatus } from '../../shared/types/device.ts'
import type { Settings } from './config.ts'
import { AppError } from './errors.ts'

const PING_TIMEOUT_MS = 2000
const SSH_TIMEOUT_MS = 6000
const MAX_OUTPUT_BYTES = 64 * 1024

export type CommandRunner = (command: string, args: string[], timeoutMs: number) => Promise<boolean>

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

export async function checkDeviceStatus(
  device: Device,
  ssh: Settings['ssh'],
  runner: CommandRunner = runCommand,
  platform: NodeJS.Platform = process.platform,
): Promise<DeviceStatus> {
  const network = device.address
    ? runner('ping', pingArguments(device.address, platform), PING_TIMEOUT_MS + 1000)
    : Promise.resolve(false)
  const remote = ssh && device.address && device.sshUser
    ? runner('ssh', sshArguments(device, ssh, 'status'), SSH_TIMEOUT_MS)
    : Promise.resolve(false)
  const [networkReachable, sshReady] = await Promise.all([network, remote])
  return { deviceId: device.id, networkReachable, sshReady, checkedAt: new Date().toISOString() }
}

export async function checkDevicesStatus(devices: Device[], ssh: Settings['ssh'], concurrency = 4): Promise<DeviceStatus[]> {
  const statuses = new Array<DeviceStatus>(devices.length)
  let next = 0
  const worker = async () => {
    while (next < devices.length) {
      const index = next++
      statuses[index] = await checkDeviceStatus(devices[index]!, ssh)
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, devices.length) }, worker))
  return statuses
}

export async function sendShutdownCommand(
  device: Device,
  force: boolean,
  ssh: Settings['ssh'],
  runner: CommandRunner = runCommand,
): Promise<void> {
  if (!ssh) throw new AppError(503, 'SSH shutdown is not configured on the homelab server.')
  const accepted = await runner('ssh', sshArguments(device, ssh, force ? 'shutdown-force' : 'shutdown-safe'), SSH_TIMEOUT_MS)
  if (!accepted) throw new Error('SSH command failed')
}
