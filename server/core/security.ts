import { createRemoteJWKSet, jwtVerify, type JWTVerifyGetKey } from 'jose'
import type { Settings } from './config.ts'
import { AppError } from './errors.ts'

export function createAccessVerifier(settings: Settings, providedKeys?: JWTVerifyGetKey) {
  const keys = providedKeys ?? (settings.developmentBypass ? undefined : createRemoteJWKSet(
    new URL(`${settings.teamDomain}/cdn-cgi/access/certs`), { timeoutDuration: 3000 },
  ))
  return async (token: string | undefined, remoteAddress?: string) => {
    if (settings.developmentBypass) {
      if (!['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(remoteAddress || '')) {
        throw new AppError(403, 'Development mode only allows local connections.')
      }
      return
    }
    if (!token || !keys) throw new AppError(401, 'Access this app through the Cloudflare Access-protected domain.')
    try {
      await jwtVerify(token, keys, {
        issuer: settings.teamDomain,
        audience: settings.audience,
        algorithms: ['RS256'],
        requiredClaims: ['exp', 'iat', 'sub'],
      })
    }
    catch { throw new AppError(401, 'Your session is invalid or expired. Sign in again through Cloudflare Access.') }
  }
}

export function checkMutationRequest(headers: { origin?: string, contentType?: string, fetchSite?: string }, expectedOrigin: string): void {
  if (headers.origin !== expectedOrigin || (headers.fetchSite && headers.fetchSite !== 'same-origin')) {
    throw new AppError(403, 'Request origin is not allowed.')
  }
  if (headers.contentType?.split(';')[0]?.trim().toLowerCase() !== 'application/json') {
    throw new AppError(415, 'The request must use application/json.')
  }
}
