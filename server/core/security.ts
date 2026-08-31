import { AppError } from './errors.ts'

export function checkJsonMutation(contentType?: string): void {
  if (contentType?.split(';')[0]?.trim().toLowerCase() !== 'application/json') {
    throw new AppError(415, 'The request must use application/json.')
  }
}
