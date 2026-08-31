import { test } from 'node:test'
import assert from 'node:assert/strict'
import { resolve } from 'node:path'
import { createLocalJWKSet, exportJWK, generateKeyPair, SignJWT } from 'jose'
import { readSettings } from '../../server/core/config.ts'
import { checkMutationRequest, createAccessVerifier } from '../../server/core/security.ts'

const production = {
  NODE_ENV: 'production', NITRO_HOST: '127.0.0.1', APP_ORIGIN: 'https://lab.example.com',
  DB_PATH: resolve('data/testing.sqlite'), CF_ACCESS_TEAM_DOMAIN: 'https://testing.cloudflareaccess.com', CF_ACCESS_AUD: 'expected-audience',
}

test('production configuration fails closed and forbids development bypass', () => {
  assert.throws(() => readSettings({}))
  assert.throws(() => readSettings({ ...production, AUTH_DEV_BYPASS: 'true' }))
  assert.throws(() => readSettings({ ...production, AUTH_DEV_BYPASS: 'true' }, true))
  assert.throws(() => readSettings({ ...production, NODE_ENV: 'development', AUTH_DEV_BYPASS: 'true' }, false))
  for (const key of ['APP_ORIGIN', 'CF_ACCESS_TEAM_DOMAIN', 'CF_ACCESS_AUD', 'DB_PATH', 'NITRO_HOST']) {
    assert.throws(() => readSettings({ ...production, [key]: '' }))
  }
})

test('validates configured origins and network settings', () => {
  assert.equal(readSettings(production).wol.port, 9)
  assert.equal(readSettings(production).wol.broadcast, '255.255.255.255')
  for (const changes of [
    { APP_ORIGIN: 'http://lab.example.com' }, { APP_ORIGIN: 'https://lab.example.com/path' },
    { CF_ACCESS_TEAM_DOMAIN: 'https://attacker.test' }, { CF_ACCESS_TEAM_DOMAIN: 'https://x.cloudflareaccess.com.evil.test' },
    { NITRO_HOST: '0.0.0.0' }, { WOL_BROADCAST: 'localhost' }, { WOL_SOURCE_IP: 'eth0' },
    { WOL_PORT: '0' }, { WOL_PORT: '65536' }, { WOL_PORT: '1.5' }, { DB_PATH: 'relative.sqlite' },
  ]) assert.throws(() => readSettings({ ...production, ...changes }))
})

test('development bypass is explicit and restricted to the socket loopback address', async () => {
  const settings = readSettings({ NODE_ENV: 'development', AUTH_DEV_BYPASS: 'true' }, true)
  const verify = createAccessVerifier(settings)
  await verify(undefined, '127.0.0.1')
  await assert.rejects(verify(undefined, '192.168.1.12'), { statusCode: 403 })
  assert.throws(() => readSettings({ NODE_ENV: 'development' }, true))
})

test('JWT validates signature, issuer, audience, expiry and required claims', async () => {
  const pair = await generateKeyPair('RS256')
  const other = await generateKeyPair('RS256')
  const jwk = await exportJWK(pair.publicKey)
  const settings = readSettings(production)
  const verify = createAccessVerifier(settings, createLocalJWKSet({ keys: [{ ...jwk, kid: 'test', alg: 'RS256' }] }))
  const sign = async (changes: Record<string, unknown> = {}, key = pair.privateKey) => new SignJWT({
    iss: settings.teamDomain, aud: settings.audience, sub: 'user', iat: Math.floor(Date.now() / 1000), exp: Math.floor(Date.now() / 1000) + 300, ...changes,
  }).setProtectedHeader({ alg: 'RS256', kid: 'test' }).sign(key)
  await verify(await sign())
  await assert.rejects(verify(undefined), { statusCode: 401 })
  await assert.rejects(verify('forged'), { statusCode: 401 })
  for (const claims of [{ iss: 'https://attacker.test' }, { aud: 'other-app' }, { exp: 1 }, { exp: undefined }, { sub: undefined }, { iat: undefined }]) {
    await assert.rejects(verify(await sign(claims)), { statusCode: 401 })
  }
  await assert.rejects(verify(await sign({}, other.privateKey)), { statusCode: 401 })
})

test('CSRF rejects missing, foreign and null origins, cross-site and non-JSON requests', () => {
  const origin = production.APP_ORIGIN
  checkMutationRequest({ origin, contentType: 'application/json; charset=utf-8', fetchSite: 'same-origin' }, origin)
  for (const request of [
    { contentType: 'application/json' }, { origin: 'null', contentType: 'application/json' },
    { origin: `${origin}.evil.test`, contentType: 'application/json' }, { origin, contentType: 'text/plain' },
    { origin, contentType: 'application/json', fetchSite: 'cross-site' },
  ]) assert.throws(() => checkMutationRequest(request, origin))
})
