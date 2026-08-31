import { getHeader, setHeaders } from 'h3'
import { checkMutationRequest } from '../core/security.ts'
import { AppError } from '../core/errors.ts'
import { getRuntime } from '../utils/runtime'
import { apiHandler, readJson } from '../utils/http'

export default apiHandler(async (event) => {
  setHeaders(event, {
    'X-Content-Type-Options': 'nosniff',
    'X-Frame-Options': 'DENY',
    'Referrer-Policy': 'no-referrer',
    'Permissions-Policy': 'camera=(), microphone=(), geolocation=()',
    'Cache-Control': 'private, no-store',
    'Content-Security-Policy': "frame-ancestors 'none'; base-uri 'self'; object-src 'none'",
  })
  if (event.path.split('?')[0] === '/api/health' && event.method === 'GET') return
  const { settings, verifyAccess } = getRuntime()
  // Nuxt's development IPC proxy creates a virtual socket without an address.
  // nuxt.config.ts verifies the real listener is loopback-only; never trust X-Forwarded-For.
  const remoteAddress = event.node.req.socket.remoteAddress ?? (import.meta.dev ? '127.0.0.1' : undefined)
  await verifyAccess(getHeader(event, 'cf-access-jwt-assertion'), remoteAddress)
  if (['POST', 'PATCH', 'PUT', 'DELETE'].includes(event.method)) {
    checkMutationRequest({
      origin: getHeader(event, 'origin'),
      contentType: getHeader(event, 'content-type'),
      fetchSite: getHeader(event, 'sec-fetch-site'),
    }, settings.origin)
    const length = getHeader(event, 'content-length')
    if (length && (!Number.isInteger(Number(length)) || Number(length) < 0 || Number(length) > 4096)) {
      throw new AppError(413, 'Request is too large; maximum 4096 bytes.')
    }
    await readJson(event)
  }
})
