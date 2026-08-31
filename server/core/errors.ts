export class AppError extends Error {
  readonly statusCode: number
  readonly retryAfter?: number

  constructor(statusCode: number, message: string, retryAfter?: number) {
    super(message)
    this.name = 'AppError'
    this.statusCode = statusCode
    this.retryAfter = retryAfter
  }
}
