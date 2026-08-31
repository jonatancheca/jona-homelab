import { spawn, spawnSync } from 'node:child_process'
import { createSocket } from 'node:dgram'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const directory = mkdtempSync(join(tmpdir(), 'homelab-e2e-'))
const receiver = createSocket('udp4')
await new Promise(resolve => receiver.bind(0, '127.0.0.1', resolve))
receiver.on('message', () => {})
const child = spawn(process.execPath, [resolve('node_modules/nuxt/bin/nuxt.mjs'), 'dev', '--host', '127.0.0.1', '--port', '3123'], {
  windowsHide: true,
  stdio: 'inherit',
  env: { ...process.env, NODE_ENV: 'development', AUTH_DEV_BYPASS: 'true', APP_ORIGIN: 'http://127.0.0.1:3123',
    DB_PATH: join(directory, 'test.sqlite'), WOL_BROADCAST: '127.0.0.1', WOL_SOURCE_IP: '127.0.0.1', WOL_PORT: String(receiver.address().port) },
})
let stopping = false
function stop() {
  if (stopping) return
  stopping = true
  if (child.pid && process.platform === 'win32') spawnSync('taskkill', ['/pid', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
  else child.kill('SIGTERM')
}
process.on('SIGINT', stop)
process.on('SIGTERM', stop)
child.on('exit', (code) => {
  receiver.close()
  rmSync(directory, { recursive: true, force: true })
  process.exitCode = stopping ? 0 : code ?? 1
})
