import { createError, defineEventHandler, getRouterParam, setHeader, type H3Event } from 'h3'
import { AppError } from '../core/errors.ts'
import { parseDeviceId } from '../core/validation.ts'

export function apiHandler<T>(handler: (event: H3Event) => T | Promise<T>) {
  return defineEventHandler(async (event) => {
    try { return await handler(event) }
    catch (error) {
      if (error instanceof AppError) {
        if (error.retryAfter) setHeader(event, 'Retry-After', error.retryAfter)
        throw createError({ statusCode: error.statusCode, message: error.message, data: { message: error.message, retryAfter: error.retryAfter } })
      }
      console.error('[homelab] Internal error:', error instanceof Error ? error.name : 'UnknownError')
      throw createError({ statusCode: 500, message: 'Internal error. Check the service and storage.', data: { message: 'Internal error. Check the service and storage.' } })
    }
  })
}

export const deviceId = (event: H3Event) => parseDeviceId(getRouterParam(event, 'id'))

export async function readJson(event: H3Event): Promise<unknown> {
  if (Object.hasOwn(event.context, 'homelabBody')) return event.context.homelabBody
  const raw = await new Promise<string>((resolve, reject) => {
    const request = event.node.req
    let size = 0
    const chunks: Buffer[] = []
    const cleanup = () => {
      request.off('data', onData)
      request.off('end', onEnd)
      request.off('error', onError)
      request.off('aborted', onAborted)
    }
    const onError = () => { cleanup(); reject(new AppError(400, 'The request could not be read.')) }
    const onAborted = () => onError()
    const onEnd = () => { cleanup(); resolve(Buffer.concat(chunks).toString('utf8')) }
    const onData = (chunk: Buffer) => {
      size += chunk.length
      if (size > 4096) {
        cleanup()
        request.resume()
        reject(new AppError(413, 'Request is too large; maximum 4096 bytes.'))
      }
      else chunks.push(chunk)
    }
    request.on('data', onData)
    request.once('end', onEnd)
    request.once('error', onError)
    request.once('aborted', onAborted)
  })
  try { event.context.homelabBody = JSON.parse(raw) as unknown }
  catch { throw new AppError(400, 'Invalid JSON.') }
  return event.context.homelabBody
}
