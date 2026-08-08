import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from './schema'
import { ApiError, TransportError, type ErrorBody } from './errors'

/**
 * The one HTTP client.
 *
 * `credentials: 'include'` is the whole of authentication (frontend-notes §2):
 * the session is an HttpOnly cookie, there is no token to read or attach, and
 * a login page that keeps a copy in localStorage is building a second, weaker
 * copy of something the browser already does correctly.
 *
 * The base URL is empty because the document's paths are absolute and a
 * deployment serves the SPA from the same binary as the API (D2). In
 * development Vite proxies /api, so the browser stays on one origin and the
 * cookie behaves exactly as it will in production.
 */
export const client = createClient<paths>({
  baseUrl: '',
  credentials: 'include',
  headers: { 'Content-Type': 'application/json' },
})

// ── the 401 channel ──────────────────────────────────────────────────────────

type Listener = (requestId: string) => void
const sessionLostListeners = new Set<Listener>()

/**
 * Called when any request comes back `unauthenticated`.
 *
 * A 401 mid-session is real, not a glitch (frontend-notes §2): changing a role
 * or a password, or deactivating an account, revokes every live session it
 * holds. So this is "you have been logged out", never a retry.
 */
export function onSessionLost(fn: Listener): () => void {
  sessionLostListeners.add(fn)
  return () => sessionLostListeners.delete(fn)
}

/** Requests whose 401 is an answer rather than an expiry. */
const AUTH_ROUTES = ['/api/v1/auth/login', '/api/v1/auth/me', '/api/v1/auth/logout']

const errorMiddleware: Middleware = {
  async onResponse({ request, response }) {
    if (response.ok) return response

    const cloned = response.clone()
    let body: ErrorBody | undefined
    try {
      const parsed: unknown = await cloned.json()
      if (
        typeof parsed === 'object' &&
        parsed !== null &&
        'error' in parsed &&
        typeof (parsed as { error: unknown }).error === 'object'
      ) {
        body = (parsed as { error: ErrorBody }).error
      }
    } catch {
      // Not the envelope. Fall through to the synthesised one below.
    }

    if (!body) {
      // Something answered that is not this API — a proxy, a gateway, a crash
      // before the envelope middleware. Say so rather than inventing a code
      // that looks like the contract's.
      body = {
        code: 'internal',
        message: `The server answered ${response.status} ${response.statusText} without an error envelope. This is not a response this API produces; the request may not have reached it.`,
        request_id: response.headers.get('X-Request-Id') ?? '(none)',
      }
    }
    if (!body.request_id) {
      body.request_id = response.headers.get('X-Request-Id') ?? '(none)'
    }

    const url = new URL(request.url).pathname
    if (body.code === 'unauthenticated' && !AUTH_ROUTES.includes(url)) {
      for (const fn of sessionLostListeners) fn(body.request_id)
    }

    throw new ApiError(response.status, body)
  },

  async onError({ error }) {
    throw new TransportError(
      'Could not reach the server. It may be down, or the connection dropped.',
      error,
    )
  },
}

client.use(errorMiddleware)

// ── unwrapping ───────────────────────────────────────────────────────────────

type Result<T> = { data?: T; error?: unknown }

/**
 * Turns openapi-fetch's `{ data, error }` into a value or a throw.
 *
 * The middleware above already throws on any non-ok response, so reaching the
 * `error` branch here means openapi-fetch itself declined the body.
 */
export async function unwrap<T>(p: Promise<Result<T>>): Promise<T> {
  const { data, error } = await p
  if (error !== undefined) {
    throw new TransportError('The server sent a body this client could not read.', error)
  }
  if (data === undefined) {
    throw new TransportError('The server sent an empty body where one was expected.')
  }
  return data
}

/**
 * huma renders an empty Go slice as `null` rather than `[]`, so every list
 * field in the generated types is `T[] | null`. Collapsing that at the edge
 * keeps `?? []` out of every component that renders a list.
 */
export function list<T>(v: T[] | null | undefined): T[] {
  return v ?? []
}
