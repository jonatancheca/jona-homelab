import { getHeader, setHeaders } from 'h3'
import { checkJsonMutation } from '../core/security.ts'
import { AppError } from '../core/errors.ts'
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
  if (['POST', 'PATCH', 'PUT', 'DELETE'].includes(event.method)) {
    checkJsonMutation(getHeader(event, 'content-type'))
    const length = getHeader(event, 'content-length')
    if (length && (!Number.isInteger(Number(length)) || Number(length) < 0 || Number(length) > 4096)) {
      throw new AppError(413, 'Request is too large; maximum 4096 bytes.')
    }
    await readJson(event)
  }
})
