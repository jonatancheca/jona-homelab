import { isIPv4 } from 'node:net'
import { isAbsolute } from 'node:path'

export interface Settings {
  databasePath: string
  wol: { broadcast: string, port: number, sourceIp?: string }
}

export function readSettings(env: NodeJS.ProcessEnv, development = false): Settings {
  if (!development && (env.NITRO_HOST || env.HOST) !== '127.0.0.1') {
    throw new Error('Production requires NITRO_HOST=127.0.0.1.')
  }
  const databasePath = env.DB_PATH || (development ? './data/homelab.sqlite' : '')
  if (!databasePath || (!development && !isAbsolute(databasePath))) {
    throw new Error('Production requires an absolute, persistent DB_PATH.')
  }
  const broadcast = env.WOL_BROADCAST || '255.255.255.255'
  const sourceIp = env.WOL_SOURCE_IP || undefined
  const port = Number(env.WOL_PORT || '9')
  if (!isIPv4(broadcast) || broadcast === '0.0.0.0' || (sourceIp && (!isIPv4(sourceIp) || sourceIp === '0.0.0.0'))) {
    throw new Error('WOL_BROADCAST and WOL_SOURCE_IP must be valid IPv4 addresses.')
  }
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('WOL_PORT must be between 1 and 65535.')
  return { databasePath, wol: { broadcast, port, sourceIp } }
}
