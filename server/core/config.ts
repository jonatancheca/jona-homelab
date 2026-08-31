import { isIPv4 } from 'node:net'
import { isAbsolute } from 'node:path'

export interface Settings {
  developmentBypass: boolean
  origin: string
  databasePath: string
  teamDomain: string
  audience: string
  wol: { broadcast: string, port: number, sourceIp?: string }
}

export function readSettings(env: NodeJS.ProcessEnv, development = false): Settings {
  const requestedBypass = env.AUTH_DEV_BYPASS === 'true'
  if (requestedBypass && (!development || env.NODE_ENV !== 'development')) {
    throw new Error('AUTH_DEV_BYPASS is forbidden outside nuxt dev.')
  }
  const developmentBypass = requestedBypass && development && env.NODE_ENV === 'development'
  const origin = new URL(env.APP_ORIGIN || (developmentBypass ? 'http://127.0.0.1:3000' : ''))
  if (origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) {
    throw new Error('APP_ORIGIN must contain only the web origin.')
  }
  if (developmentBypass) {
    if (origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1') {
      throw new Error('The local bypass only allows APP_ORIGIN=http://127.0.0.1:port.')
    }
  }
  else if (origin.protocol !== 'https:') {
    throw new Error('APP_ORIGIN must use HTTPS.')
  }
  let teamDomain = ''
  const audience = env.CF_ACCESS_AUD?.trim() || ''
  if (!developmentBypass) {
    const team = new URL(env.CF_ACCESS_TEAM_DOMAIN || '')
    if (team.protocol !== 'https:' || !/^[a-z0-9-]+\.cloudflareaccess\.com$/.test(team.hostname)
      || team.port || team.username || team.password || team.pathname !== '/' || team.search || team.hash) {
      throw new Error('CF_ACCESS_TEAM_DOMAIN must be https://your-team.cloudflareaccess.com.')
    }
    if (!audience) throw new Error('CF_ACCESS_AUD is required.')
    teamDomain = team.origin
  }
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
  return { developmentBypass, origin: origin.origin, databasePath, teamDomain, audience, wol: { broadcast, port, sourceIp } }
}
