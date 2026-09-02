import { test } from 'node:test'
import assert from 'node:assert/strict'
import type { Device } from '../../shared/types/device.ts'
import { checkDeviceStatus, pingArguments, sendShutdownCommand, sshArguments, type CommandRunner } from '../../server/core/remote.ts'

const device: Device = {
  id: 'device-id',
  name: 'PC',
  mac: 'AA:BB:CC:DD:EE:FF',
  address: '192.168.1.25',
  sshUser: 'jona-homelab-remote',
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-01-01T00:00:00.000Z',
  lastSentAt: null,
}
const ssh = { identityFile: '/etc/jona-homelab/ssh/id_ed25519', knownHostsFile: '/etc/jona-homelab/ssh/known_hosts', port: 22 }

test('builds platform ping arguments without a shell', () => {
  assert.deepEqual(pingArguments(device.address!, 'win32'), ['-n', '1', '-w', '2000', device.address])
  assert.deepEqual(pingArguments(device.address!, 'linux'), ['-c', '1', '-W', '2', device.address])
})

test('builds strict SSH arguments from validated saved fields', () => {
  const args = sshArguments(device, ssh, 'status')
  assert.equal(args.at(-2), `${device.sshUser}@${device.address}`)
  assert.equal(args.at(-1), 'status')
  assert.ok(args.includes('BatchMode=yes'))
  assert.ok(args.includes('PasswordAuthentication=no'))
  assert.ok(args.includes('StrictHostKeyChecking=yes'))
  assert.ok(args.includes(`UserKnownHostsFile=${ssh.knownHostsFile}`))
  assert.ok(args.includes('IdentitiesOnly=yes'))
  assert.equal(args.some(value => value.includes(';')), false)
})

test('status checks ping and authenticated SSH independently', async () => {
  const calls: Array<{ command: string, args: string[], timeout: number }> = []
  const runner: CommandRunner = async (command, args, timeout) => {
    calls.push({ command, args, timeout })
    return command === 'ssh'
  }
  const status = await checkDeviceStatus(device, ssh, runner, 'linux')
  assert.equal(status.networkReachable, false)
  assert.equal(status.sshReady, true)
  assert.deepEqual(calls.map(call => call.command).sort(), ['ping', 'ssh'])
  assert.equal(calls.find(call => call.command === 'ping')?.timeout, 3000)
  assert.equal(calls.find(call => call.command === 'ssh')?.timeout, 6000)
})

test('unconfigured legacy device is reported without executing processes', async () => {
  const runner: CommandRunner = async () => { throw new Error('must not run') }
  const status = await checkDeviceStatus({ ...device, address: null, sshUser: null }, ssh, runner)
  assert.equal(status.networkReachable, false)
  assert.equal(status.sshReady, false)
})

test('shutdown maps safe and forced choices to fixed remote commands', async () => {
  const commands: string[] = []
  const runner: CommandRunner = async (command, args) => {
    assert.equal(command, 'ssh')
    commands.push(args.at(-1)!)
    return true
  }
  await sendShutdownCommand(device, false, ssh, runner)
  await sendShutdownCommand(device, true, ssh, runner)
  assert.deepEqual(commands, ['shutdown-safe', 'shutdown-force'])
})

test('shutdown fails closed without configuration or when SSH rejects command', async () => {
  await assert.rejects(sendShutdownCommand(device, false, null), { statusCode: 503 })
  await assert.rejects(sendShutdownCommand(device, false, ssh, async () => false), /SSH command failed/)
})
