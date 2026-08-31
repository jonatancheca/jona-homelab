import { test } from 'node:test'
import assert from 'node:assert/strict'
import { normalizeMac, parseDeviceInput, parseDeviceId } from '../../server/core/validation.ts'

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
  assert.deepEqual(parseDeviceInput({ name: '  Living room PC  ', mac: 'aabbccddeeff' }), { name: 'Living room PC', mac: 'AA:BB:CC:DD:EE:FF' })
  for (const value of [null, [], { name: '', mac: 'aabbccddeeff' }, { name: 'a'.repeat(81), mac: 'aabbccddeeff' }, { name: 'x\ny', mac: 'aabbccddeeff' }, { name: 'PC', mac: 'aabbccddeeff', command: 'ls' }]) {
    assert.throws(() => parseDeviceInput(value), { statusCode: 400 })
  }
})

test('invalid resource identifiers return 404', () => {
  for (const id of [undefined, '1', "' OR 1=1", '../etc/passwd']) assert.throws(() => parseDeviceId(id), { statusCode: 404 })
})
