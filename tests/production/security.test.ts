import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { once } from 'node:events'
import { setTimeout as delay } from 'node:timers/promises'

async function stop(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return
  const exited = new Promise<void>(resolve => child.once('exit', () => resolve()))
  child.kill('SIGTERM')
  await exited
}

test('compiled server protects API and does not allow the development bypass', { timeout: 30000 }, async () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-production-'))
  const environment = { ...process.env, NODE_ENV: 'production', NITRO_HOST: '127.0.0.1', NITRO_PORT: '3124',
    AUTH_DEV_BYPASS: 'false', APP_ORIGIN: 'https://lab.example.com', DB_PATH: join(directory, 'prod.sqlite'),
    CF_ACCESS_TEAM_DOMAIN: 'https://testing.cloudflareaccess.com', CF_ACCESS_AUD: 'production-test-audience',
    WOL_BROADCAST: '127.0.0.1', WOL_SOURCE_IP: '127.0.0.1' }
  let child: ChildProcess | undefined
  try {
    let output = ''
    const start = (env: NodeJS.ProcessEnv) => {
      const server = spawn(process.execPath, [resolve('.output/server/index.mjs')], { env, windowsHide: true })
      server.stdout?.on('data', chunk => { output += chunk.toString() })
      server.stderr?.on('data', chunk => { output += chunk.toString() })
      return server
    }
    child = start(environment)
    let ready = false
    for (let attempt = 0; attempt < 60; attempt++) {
      if (child.exitCode !== null) throw new Error(output)
      try { ready = output.includes('Listening') && (await fetch('http://127.0.0.1:3124/api/health')).ok }
      catch { /* Wait for the fresh process, never reuse another server. */ }
      if (ready) break
      await delay(100)
    }
    assert.equal(ready, true, output)
    const missing = await fetch('http://127.0.0.1:3124/api/devices')
    assert.equal(missing.status, 401)
    assert.match(missing.headers.get('cache-control') || '', /no-store/)
    const forged = await fetch('http://127.0.0.1:3124/api/devices', { headers: { 'cf-access-jwt-assertion': 'forged', 'cf-access-authenticated-user-email': 'admin@example.com' } })
    assert.equal(forged.status, 401)
    assert.equal((await fetch('http://127.0.0.1:3124/api/devices/missing/wake', { method: 'POST', headers: { origin: environment.APP_ORIGIN, 'content-type': 'application/json' }, body: '{}' })).status, 401)
    await stop(child)
    for (const nodeEnv of ['production', 'development']) {
      output = ''
      child = start({ ...environment, NODE_ENV: nodeEnv, AUTH_DEV_BYPASS: 'true' })
      await once(child, 'exit', { signal: AbortSignal.timeout(5000) })
      assert.match(output, /AUTH_DEV_BYPASS is forbidden/)
      assert.notEqual(child.exitCode, null, 'Server should fail startup when bypass is requested')
      await stop(child)
    }
  }
  finally { if (child) await stop(child); rmSync(directory, { recursive: true, force: true }) }
})
