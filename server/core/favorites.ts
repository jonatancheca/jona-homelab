import type { FavoriteInput } from '../../shared/types/favorite.ts'
import { AppError } from './errors.ts'

// eslint-disable-next-line no-control-regex -- Reject control characters before URL parsing can silently strip them.
const controlCharacters = /[\u0000-\u001f\u007f]/

export function parseFavoriteInput(value: unknown): FavoriteInput {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new AppError(400, 'Datos de favorito no válidos.')
  }
  const input = value as Record<string, unknown>
  if (Object.keys(input).some(key => key !== 'name' && key !== 'url')) {
    throw new AppError(400, 'Solo se permiten el nombre y la URL.')
  }
  if (typeof input.name !== 'string' || !input.name.trim() || input.name.trim().length > 80 || controlCharacters.test(input.name)) {
    throw new AppError(400, 'El nombre debe tener entre 1 y 80 caracteres válidos.')
  }
  if (typeof input.url !== 'string' || input.url.trim().length > 2048 || controlCharacters.test(input.url)) {
    throw new AppError(400, 'Introduce una URL válida de hasta 2048 caracteres.')
  }
  let url: URL
  try { url = new URL(input.url.trim()) }
  catch { throw new AppError(400, 'Introduce una URL completa que empiece por http:// o https://.') }
  if (!/^https?:\/\//i.test(input.url.trim()) || !['http:', 'https:'].includes(url.protocol) || !url.hostname) {
    throw new AppError(400, 'Solo se permiten URLs http:// o https://.')
  }
  if (url.username || url.password) throw new AppError(400, 'La URL no puede contener usuario ni contraseña.')
  if (url.href.length > 2048) throw new AppError(400, 'La URL es demasiado larga.')
  return { name: input.name.trim(), url: url.href }
}

export function parseFavoriteId(value: string | undefined): string {
  if (!value || !/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/i.test(value)) {
    throw new AppError(404, 'Favorito no encontrado.')
  }
  return value
}
