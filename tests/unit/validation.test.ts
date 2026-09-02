import { test } from 'node:test'
import assert from 'node:assert/strict'
import { normalizeMac, parseDeviceInput, parseDeviceId, parseShutdownInput } from '../../server/core/validation.ts'

const valid = { name: 'Living room PC', mac: 'aabbccddeeff', address: '192.168.1.25', sshUser: 'jona-homelab-remote' }

test('normalizes colon, dash and compact MAC addresses', () => {
  for (const mac of ['aa:bb:cc:dd:ee:ff', ' AA-BB-CC-DD-EE-FF ', 'aabbccddeeff']) {
    assert.equal(normalizeMac(mac), 'AA:BB:CC:DD:EE:FF')
  }
})

test('rejects malformed, mixed, multicast and zero MAC addresses', () => {
  for (const mac of ['', null, 'AA:BB:CC:DD:EE', 'AA-BB:CC-DD-EE-FF', 'GG:BB:CC:DD:EE:FF', 'FF:FF:FF:FF:FF:FF', '01:00:5E:00:00:01', '00:00:00:00:00:00', 'AA:BB:CC:DD:EE:FF;shutdown']) {
    assert.throws(() => normalizeMac(mac), { statusCode: 400 })
  }
})

test('validates names and rejects unexpected fields', () => {
  assert.deepEqual(parseDeviceInput({ ...valid, name: '  Living room PC  ' }), { ...valid, name: 'Living room PC', mac: 'AA:BB:CC:DD:EE:FF' })
  for (const value of [null, [], { ...valid, name: '' }, { ...valid, name: 'a'.repeat(81) }, { ...valid, name: 'x\ny' }, { ...valid, command: 'ls' }]) {
    assert.throws(() => parseDeviceInput(value), { statusCode: 400 })
  }
})

test('accepts private IPv4 and safe SSH users but rejects command injection and public targets', () => {
  for (const address of ['10.0.0.4', '172.16.0.4', '172.31.255.254', '192.168.50.4']) {
    assert.equal(parseDeviceInput({ ...valid, address }).address, address)
  }
  for (const value of [
    { ...valid, address: '8.8.8.8' },
    { ...valid, address: '192.168.1.25;shutdown' },
    { ...valid, sshUser: '-oProxyCommand=calc' },
    { ...valid, sshUser: 'domain\\user' },
  ]) assert.throws(() => parseDeviceInput(value), { statusCode: 400 })
})

test('shutdown accepts one boolean and rejects extra command fields', () => {
  assert.deepEqual(parseShutdownInput({ force: false }), { force: false })
  assert.deepEqual(parseShutdownInput({ force: true }), { force: true })
  for (const value of [null, {}, { force: 'true' }, { force: false, command: 'whoami' }]) {
    assert.throws(() => parseShutdownInput(value), { statusCode: 400 })
  }
})

test('invalid resource identifiers return 404', () => {
  for (const id of [undefined, '1', "' OR 1=1", '../etc/passwd']) assert.throws(() => parseDeviceId(id), { statusCode: 404 })
})
