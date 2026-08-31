import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { createSocket, type Socket } from 'node:dgram'
import { createMagicPacket, sendMagicPacket } from '../../server/core/wol.ts'

test('magic packet contains 6 FF bytes and exactly 16 copies of the MAC', () => {
  const packet = createMagicPacket('AA:BB:CC:DD:EE:FF')
  assert.equal(packet.length, 102)
  assert.equal(packet.subarray(0, 6).toString('hex'), 'ffffffffffff')
  assert.equal(packet.subarray(6).toString('hex'), 'aabbccddeeff'.repeat(16))
})

test('native UDP delivers exact bytes on loopback, never on LAN', async () => {
  const receiver = createSocket('udp4')
  await new Promise<void>(resolve => receiver.bind(0, '127.0.0.1', resolve))
  try {
    const incoming = new Promise<Buffer>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('No loopback packet')), 3000)
      receiver.once('message', (packet) => { clearTimeout(timeout); resolve(packet) })
    })
    await sendMagicPacket('AA:BB:CC:DD:EE:FF', { broadcast: '127.0.0.1', sourceIp: '127.0.0.1', port: receiver.address().port })
    assert.deepEqual(await incoming, createMagicPacket('AA:BB:CC:DD:EE:FF'))
  }
  finally { receiver.close() }
})

class FakeSocket extends EventEmitter {
  closed = false
  behavior: 'bind' | 'broadcast' | 'send' | 'timeout' | 'success'
  constructor(behavior: FakeSocket['behavior']) { super(); this.behavior = behavior }
  bind(_port: number, _ip: string, callback: () => void) {
    if (this.behavior === 'bind') this.emit('error', new Error('EADDRNOTAVAIL'))
    else callback()
  }
  setBroadcast(enabled: boolean) {
    assert.equal(enabled, true)
    if (this.behavior === 'broadcast') throw new Error('EPERM')
  }
  send(_packet: Buffer, _port: number, _ip: string, callback: (error?: Error) => void) {
    if (this.behavior === 'send') callback(new Error('ENETUNREACH'))
    if (this.behavior === 'success') callback()
  }
  close() { this.closed = true }
}

for (const behavior of ['bind', 'broadcast', 'send', 'timeout', 'success'] as const) {
  test(`socket cleanup on ${behavior}`, async () => {
    const socket = new FakeSocket(behavior)
    const sending = sendMagicPacket('AA:BB:CC:DD:EE:FF', { broadcast: '127.0.0.1', port: 9 }, () => socket as unknown as Socket, 10)
    if (behavior === 'success') await sending
    else await assert.rejects(sending)
    assert.equal(socket.closed, true)
  })
}
