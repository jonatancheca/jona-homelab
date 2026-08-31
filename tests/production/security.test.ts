import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

async function stop(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return
  const exited = new Promise<void>(resolve => child.once('exit', () => resolve()))
  child.kill('SIGTERM')
  await exited
}

test('compiled server relies on external Access', { timeout: 30000 }, async () => {
  const directory = mkdtempSync(join(tmpdir(), 'homelab-production-'))
  const environment = { ...process.env, NODE_ENV: 'production', NITRO_HOST: '127.0.0.1', NITRO_PORT: '3124',
    DB_PATH: join(directory, 'prod.sqlite'),
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
    const devices = await fetch('http://127.0.0.1:3124/api/devices')
    assert.equal(devices.status, 200)
    assert.deepEqual(await devices.json(), [])
    assert.match(devices.headers.get('cache-control') || '', /no-store/)
    assert.equal((await fetch('http://127.0.0.1:3124/api/devices/missing/wake', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' })).status, 404)
    await stop(child)
  }
  finally { if (child) await stop(child); rmSync(directory, { recursive: true, force: true }) }
})
