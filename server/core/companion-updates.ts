import type { CompanionUpdateResult, Device, DeviceStatus } from '../../shared/types/device.ts'
import { AppError } from './errors.ts'
import { readCompanionStatus, requestCompanionPayload } from './remote.ts'

const repository = 'jonatancheca/jona-homelab'
const archive = 'jona-homelab-companion-win-x64.zip'
const tagPattern = /^main-[a-f0-9]{12}$/
export interface CompanionRelease { version: string | null, checkedAt: string, error?: string }

export function createReleaseLookup(fetcher: typeof fetch = fetch, now = Date.now) {
  let cached: CompanionRelease | undefined
  let expires = 0
  let pending: Promise<CompanionRelease> | undefined
  return (): Promise<CompanionRelease> => {
    if (cached && now() < expires) return Promise.resolve(cached)
    if (pending) return pending
    pending = (async () => {
      try {
        const response = await fetcher(`https://api.github.com/repos/${repository}/releases/latest`, {
          headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'JonaHomelab' }, signal: AbortSignal.timeout(5000), redirect: 'error',
        })
        if (!response.ok) throw new Error('GitHub no respondió correctamente.')
        const release = await response.json() as { tag_name?: unknown, draft?: boolean, prerelease?: boolean, assets?: Array<{ name: string, browser_download_url: string }> }
        if (typeof release.tag_name !== 'string' || !tagPattern.test(release.tag_name) || release.draft || release.prerelease || !Array.isArray(release.assets)) throw new Error('Release no válida.')
        const prefix = `https://github.com/${repository}/releases/download/${release.tag_name}/`
        for (const name of [archive, `${archive}.sha256`]) {
          if (!release.assets.some(asset => asset.name === name && asset.browser_download_url === prefix + name)) throw new Error('Falta el paquete de Companion o su checksum.')
        }
        cached = { version: release.tag_name, checkedAt: new Date(now()).toISOString() }
        expires = now() + 5 * 60_000
      }
      catch {
        cached = { version: null, checkedAt: new Date(now()).toISOString(), error: 'No se pudo comprobar la última versión publicada.' }
        expires = now() + 30_000
      }
      return cached
    })().finally(() => { pending = undefined })
    return pending
  }
}

export const latestCompanionRelease = createReleaseLookup()
export const activeUpdatePhases = new Set(['checking', 'scheduled', 'downloading', 'verifying', 'installing', 'restarting'])

export function withCompanionRelease(status: DeviceStatus, release: CompanionRelease): DeviceStatus {
  if (!status.companion) return status
  const companion = { ...status.companion, latestVersion: release.version, releaseCheckedAt: release.checkedAt, releaseError: release.error }
  const phase = companion.operation?.phase
  if (!status.remoteReady || !companion.version) companion.state = 'unknown'
  else if (!tagPattern.test(companion.version)) companion.state = 'local'
  else if (phase && activeUpdatePhases.has(phase)) companion.state = 'updating'
  else if (phase === 'failed' || phase === 'rolled-back') companion.state = 'failed'
  else if (!release.version) companion.state = 'unknown'
  else companion.state = companion.version === release.version ? 'current' : 'available'
  return { ...status, companion }
}

const updatesInFlight = new Set<string>()

export async function updateRemoteCompanion(device: Device, secret: string, fetcher: typeof fetch = fetch): Promise<CompanionUpdateResult> {
  if (device.remoteMethod !== 'companion') throw new AppError(409, 'Este dispositivo no usa Companion.')
  if (updatesInFlight.has(device.id)) throw new AppError(409, 'Ya hay una petición de actualización en curso.')
  updatesInFlight.add(device.id)
  try {
    const current = await readCompanionStatus(device, secret, fetcher)
    if (!current.ready) throw new AppError(409, 'Companion no está disponible.')
    if (!current.remoteUpdate) throw new AppError(409, 'Esta versión necesita una primera instalación manual para admitir actualizaciones remotas.')
    if (!current.version || !tagPattern.test(current.version)) throw new AppError(409, 'Las versiones locales requieren instalación manual.')
    const payload = await requestCompanionPayload(device, secret, '/v1/update', '{}', fetcher, Date.now, 40_000)
    if (typeof payload.scheduled !== 'boolean' || (payload.targetVersion !== undefined && (typeof payload.targetVersion !== 'string' || !tagPattern.test(payload.targetVersion)))) throw new AppError(502, 'Respuesta de actualización no válida.')
    return { scheduled: payload.scheduled, localBuild: payload.localBuild === true, targetVersion: payload.targetVersion as string | undefined }
  }
  catch (error) {
    if (error instanceof AppError) throw error
    throw new AppError(502, 'No se pudo confirmar la petición. Comprueba el estado antes de reintentar.')
  }
  finally { updatesInFlight.delete(device.id) }
}
