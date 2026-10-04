import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { DeviceStore } from '../../server/core/database.ts'
import { parseFavoriteId, parseFavoriteInput } from '../../server/core/favorites.ts'

test('favorites accept local services, ports, paths, queries and fragments', () => {
  for (const url of ['http://192.168.1.10:8080/admin', 'https://NAS.local:8006/', 'http://MY-PC/', 'https://homelab.example.com/app?view=all#status', 'http://[::1]:9000/']) {
    assert.deepEqual(parseFavoriteInput({ name: '  Mi servicio  ', url: ` ${url} ` }), { name: 'Mi servicio', url: new URL(url).href })
  }
  assert.equal(parseFavoriteInput({ name: 'NAS', url: 'HTTPS://NAS.LOCAL:443' }).url, 'https://nas.local/')
})

test('favorites reject unsafe links, credentials, invalid data and identifiers', () => {
  for (const url of ['javascript:alert(1)', 'data:text/html,test', 'file:///C:/test', 'ftp://nas.local/', '//nas.local/', 'nas.local', 'https:example.com', 'https://user:password@nas.local', 'https://nas.\nlocal/', '', 'https://nas.local/' + 'x'.repeat(2048), 42]) {
    assert.throws(() => parseFavoriteInput({ name: 'NAS', url }), { statusCode: 400 })
  }
  for (const input of [null, [], {}, { name: '', url: 'http://nas/' }, { name: 'x'.repeat(81), url: 'http://nas/' }, { name: 'NAS\u0000', url: 'http://nas/' }, { name: 'NAS', url: 'http://nas/', id: 'custom' }]) {
    assert.throws(() => parseFavoriteInput(input), { statusCode: 400 })
  }
  for (const id of [undefined, '', '../devices', "'; DROP TABLE favorites; --"]) {
    assert.throws(() => parseFavoriteId(id), { statusCode: 404 })
  }
})

test('favorites persist, edit and delete independently of devices and reject duplicates', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-favorites-test-'))
  const path = join(directory, 'test.sqlite')
  let store = new DeviceStore(path)
  try {
    const device = store.create({ name: 'PC', mac: 'AA:BB:CC:DD:EE:FF', address: null, sshUser: null, remoteMethod: 'none' })
    const input = parseFavoriteInput({ name: 'NAS', url: 'https://nas.local' })
    const favorite = store.createFavorite(input, 1000)
    assert.equal(parseFavoriteId(favorite.id), favorite.id)
    assert.throws(() => store.createFavorite(parseFavoriteInput({ ...input, url: 'HTTPS://NAS.LOCAL:443/' })), { statusCode: 409 })
    const other = store.createFavorite({ name: 'Admin', url: 'http://nas.local:9000/' })
    assert.throws(() => store.updateFavorite(other.id, input), { statusCode: 409 })
    const updated = store.updateFavorite(favorite.id, { name: "'; DROP TABLE devices; --", url: 'https://nas.local/files?view=all#home' }, 2000)
    assert.equal(updated.createdAt, favorite.createdAt)
    assert.equal(updated.updatedAt, '1970-01-01T00:00:02.000Z')
    assert.deepEqual(store.get(device.id), device)
    store.close()
    store = new DeviceStore(path)
    assert.deepEqual({ ...store.getFavorite(favorite.id) }, { ...updated })
    assert.equal(store.listFavorites().length, 2)
    store.deleteFavorite(favorite.id)
    assert.throws(() => store.getFavorite(favorite.id), { statusCode: 404 })
    assert.throws(() => store.updateFavorite(favorite.id, input), { statusCode: 404 })
    assert.throws(() => store.deleteFavorite(favorite.id), { statusCode: 404 })
    assert.equal(store.listFavorites()[0]?.id, other.id)
    assert.deepEqual(store.get(device.id), device)
  }
  finally { store.close(); rmSync(directory, { recursive: true, force: true }) }
})

test('version 5 migrates once while preserving device history', () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-favorites-migration-test-'))
  const path = join(directory, 'test.sqlite')
  let store = new DeviceStore(path)
  try {
    const device = store.create({ name: 'Existing PC', mac: 'AA:BB:CC:DD:EE:01', address: null, sshUser: null, remoteMethod: 'none' })
    const saved = store.markSent(device.id, device.mac)
    store.close()
    const legacy = new DatabaseSync(path)
    legacy.exec('DROP TABLE favorites; PRAGMA user_version = 5;')
    legacy.close()
    store = new DeviceStore(path)
    assert.deepEqual(store.get(device.id), saved)
    assert.deepEqual(store.listFavorites(), [])
    const favorite = store.createFavorite(parseFavoriteInput({ name: 'NAS', url: 'https://nas.local' }))
    store.close()
    store = new DeviceStore(path)
    assert.deepEqual(store.get(device.id), saved)
    assert.equal(store.listFavorites()[0]?.id, favorite.id)
  }
  finally { store.close(); rmSync(directory, { recursive: true, force: true }) }
})
