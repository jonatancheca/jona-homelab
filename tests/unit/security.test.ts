import { test } from 'node:test'
import assert from 'node:assert/strict'
import { resolve } from 'node:path'
import { readSettings } from '../../server/core/config.ts'
import { checkJsonMutation } from '../../server/core/security.ts'

const production = {
  NODE_ENV: 'production', NITRO_HOST: '127.0.0.1',
  DB_PATH: resolve('data/testing.sqlite'),
}

test('production configuration fails closed', () => {
  assert.throws(() => readSettings({}))
  for (const key of ['DB_PATH', 'NITRO_HOST']) {
    assert.throws(() => readSettings({ ...production, [key]: '' }))
  }
})

test('validates runtime and network settings', () => {
  assert.equal(readSettings(production).wol.port, 9)
  assert.equal(readSettings(production).wol.broadcast, '255.255.255.255')
  for (const changes of [
    { NITRO_HOST: '0.0.0.0' }, { WOL_BROADCAST: 'localhost' }, { WOL_SOURCE_IP: 'eth0' },
    { WOL_PORT: '0' }, { WOL_PORT: '65536' }, { WOL_PORT: '1.5' }, { DB_PATH: 'relative.sqlite' },
    { SSH_IDENTITY_FILE: resolve('key') }, { SSH_KNOWN_HOSTS_FILE: resolve('known_hosts') },
    { SSH_IDENTITY_FILE: 'relative-key', SSH_KNOWN_HOSTS_FILE: resolve('known_hosts') },
    { SSH_IDENTITY_FILE: resolve('key'), SSH_KNOWN_HOSTS_FILE: resolve('known_hosts'), SSH_PORT: '0' },
  ]) assert.throws(() => readSettings({ ...production, ...changes }))
  assert.deepEqual(readSettings({
    ...production,
    SSH_IDENTITY_FILE: resolve('key'),
    SSH_KNOWN_HOSTS_FILE: resolve('known_hosts'),
  }).ssh, { identityFile: resolve('key'), knownHostsFile: resolve('known_hosts'), port: 22 })
})

test('development uses local defaults', () => {
  const settings = readSettings({ NODE_ENV: 'development' }, true)
  assert.equal(settings.databasePath, './data/homelab.sqlite')
  assert.equal(settings.ssh, null)
})

test('mutation payloads must use JSON', () => {
  checkJsonMutation('application/json; charset=utf-8')
  for (const contentType of [undefined, 'text/plain', 'application/x-www-form-urlencoded']) {
    assert.throws(() => checkJsonMutation(contentType), { statusCode: 415 })
  }
})
