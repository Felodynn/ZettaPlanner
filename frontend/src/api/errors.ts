import type { components } from './schema'

export type ErrorBody = components['schemas']['ErrorBody']

/** The closed set of codes (CONTRACT §7.3). */
export type ErrorCode = ErrorBody['code']

/**
 * ApiError is the contract's error envelope, kept whole.
 *
 * The `message` is written to be read: every 403 names the owning role *and*
 * the route forward, and frontend-notes §3 is explicit that replacing it with
 * a paraphrase throws away the half that tells somebody what to do next. So
 * nothing here summarises, shortens, or rewrites it — the UI renders
 * `message` verbatim and `request_id` somewhere copyable.
 */
export class ApiError extends Error {
  readonly code: ErrorCode
  readonly details: Record<string, unknown>
  readonly requestId: string
  readonly status: number

  constructor(status: number, body: ErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.details = body.details ?? {}
    this.requestId = body.request_id
  }

  /** A string field out of `details`, when the backend named one. */
  detail(key: string): string | undefined {
    const v = this.details[key]
    return typeof v === 'string' ? v : undefined
  }

  get isAuth(): boolean {
    return this.code === 'unauthenticated'
  }
}

/**
 * A transport or parse failure — the request never reached the contract. Kept
 * distinct from ApiError because "the server said no" and "there was no
 * server" call for different words and different retries.
 */
export class TransportError extends Error {
  override readonly cause: unknown

  constructor(message: string, cause?: unknown) {
    super(message)
    this.name = 'TransportError'
    this.cause = cause
  }
}

/** Narrows unknown thrown values, since `catch` gives no types. */
export function asApiError(e: unknown): ApiError | undefined {
  return e instanceof ApiError ? e : undefined
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError || e instanceof TransportError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}
