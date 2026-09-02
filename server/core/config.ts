import { isIPv4 } from 'node:net'
import { isAbsolute } from 'node:path'

export interface Settings {
  databasePath: string
  wol: { broadcast: string, port: number, sourceIp?: string }
  ssh: { identityFile: string, knownHostsFile: string, port: number } | null
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
  const identityFile = env.SSH_IDENTITY_FILE?.trim() || ''
  const knownHostsFile = env.SSH_KNOWN_HOSTS_FILE?.trim() || ''
  if (Boolean(identityFile) !== Boolean(knownHostsFile)) {
    throw new Error('SSH_IDENTITY_FILE and SSH_KNOWN_HOSTS_FILE must be configured together.')
  }
  if ((identityFile && !isAbsolute(identityFile)) || (knownHostsFile && !isAbsolute(knownHostsFile))) {
    throw new Error('SSH key and known_hosts paths must be absolute.')
  }
  const sshPort = Number(env.SSH_PORT || '22')
  if (!Number.isInteger(sshPort) || sshPort < 1 || sshPort > 65535) throw new Error('SSH_PORT must be between 1 and 65535.')
  return {
    databasePath,
    wol: { broadcast, port, sourceIp },
    ssh: identityFile && knownHostsFile ? { identityFile, knownHostsFile, port: sshPort } : null,
  }
}
