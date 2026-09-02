import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { Device } from '../../shared/types/device.ts'
import { checkDeviceStatus, companionRequestSignature, companionResponseSignature, pingArguments, requestCompanion, sendShutdownCommand, sshArguments, type CommandRunner } from '../../server/core/remote.ts'

const device: Device = {
  id: 'device-id',
  name: 'PC',
  mac: 'AA:BB:CC:DD:EE:FF',
  address: '192.168.1.25',
  sshUser: 'jona-homelab-remote',
  remoteMethod: 'ssh',
  companionConfigured: false,
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-01-01T00:00:00.000Z',
  lastSentAt: null,
}
const ssh = { identityFile: '/etc/jona-homelab/ssh/id_ed25519', knownHostsFile: '/etc/jona-homelab/ssh/known_hosts', port: 22 }

test('shared HMAC vector stays compatible with companion', () => {
  const vector = JSON.parse(readFileSync(resolve('tests/fixtures/companion-protocol.json'), 'utf8')) as { secret: string, method: string, path: string, timestamp: number, nonce: string, body: string, requestSignature: string, responseStatus: number, responseBody: string, responseSignature: string }
  assert.equal(companionRequestSignature(vector.secret, vector.method, vector.path, vector.timestamp, vector.nonce, vector.body), vector.requestSignature)
  assert.equal(companionResponseSignature(vector.secret, vector.responseStatus, vector.nonce, vector.responseBody), vector.responseSignature)
})

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
  assert.equal(status.remoteReady, true)
  assert.equal(status.remoteMethod, 'ssh')
  assert.deepEqual(calls.map(call => call.command).sort(), ['ping', 'ssh'])
  assert.equal(calls.find(call => call.command === 'ping')?.timeout, 3000)
  assert.equal(calls.find(call => call.command === 'ssh')?.timeout, 6000)
})

test('unconfigured legacy device is reported without executing processes', async () => {
  const runner: CommandRunner = async () => { throw new Error('must not run') }
  const status = await checkDeviceStatus({ ...device, address: null, sshUser: null }, ssh, runner)
  assert.equal(status.networkReachable, false)
  assert.equal(status.remoteReady, false)
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

test('Companion client signs request, verifies response and rejects altered response', async () => {
  const secret = Buffer.from(new Uint8Array(32).fill(7)).toString('base64url')
  let requestUrl = ''
  let requestInit: RequestInit | undefined
  const fetcher: typeof fetch = async (input, init) => {
    requestUrl = String(input)
    requestInit = init
    const nonce = new Headers(init?.headers).get('X-Jona-Nonce')!
    const signature = companionResponseSignature(secret, 200, nonce, JSON.stringify({ ready: true, accepted: true }))
    return new Response(JSON.stringify({ ready: true, accepted: true }), { status: 200, headers: { 'X-Jona-Response-Signature': signature } })
  }
  const companion = { ...device, remoteMethod: 'companion' as const }
  assert.equal(await requestCompanion(companion, secret, 'status', fetcher, () => 1_777_777_777_000), true)
  assert.equal(requestUrl, 'http://192.168.1.25:47654/v1/status')
  const headers = new Headers(requestInit?.headers)
  assert.equal(headers.get('X-Jona-Signature'), companionRequestSignature(secret, 'GET', '/v1/status', 1_777_777_777, headers.get('X-Jona-Nonce')!, ''))
  const altered = await requestCompanion(companion, secret, 'status', async () => new Response(JSON.stringify({ ready: false }), { status: 200, headers: { 'X-Jona-Response-Signature': '0'.repeat(64) } }), () => 1_777_777_777_000)
  assert.equal(altered, false)
})

test('Companion method uses authenticated client for status and shutdown', async () => {
  const secret = Buffer.from(new Uint8Array(32).fill(8)).toString('base64url')
  const calls: string[] = []
  const companion = { ...device, remoteMethod: 'companion' as const }
  const runner = async (_target: Device, _secret: string, command: 'status' | 'shutdown-safe' | 'shutdown-force') => { calls.push(command); return true }
  const status = await checkDeviceStatus(companion, ssh, async (command) => { if (command === 'ping') return false; throw new Error('SSH must not run') }, 'linux', secret, runner)
  assert.equal(status.remoteReady, true)
  await sendShutdownCommand(companion, true, ssh, async () => { throw new Error('SSH must not run') }, secret, runner)
  assert.deepEqual(calls, ['status', 'shutdown-force'])
})
