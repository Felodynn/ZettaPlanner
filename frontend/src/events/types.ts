/**
 * The project event stream (CONTRACT §7.2).
 *
 * These types describe what the backend actually publishes. They are written
 * by hand — unavoidably, because a long-lived `text/event-stream` is not a
 * request/response operation and is deliberately absent from the OpenAPI
 * document (describing it as one would generate a client method that hangs).
 *
 * Everything here is therefore treated as untrusted shape: each payload is
 * read field by field with a fallback, never cast. frontend-notes §5 makes
 * that cheap to live with — the events are a **nudge to refetch**, not the
 * data. `plan.changed` carries a subject kind and id, not the new object, and
 * building a store that applies event payloads as state produces a second,
 * subtly different copy of the backend's model that is wrong exactly when two
 * people are editing.
 *
 * `turn.*` and `message.delta` are the stated exception: a streaming answer
 * has nowhere else to come from.
 */

export interface ProjectEvent {
  id: number
  type: string
  project_id: string
  at: string
  payload: Record<string, unknown>
}

/** Reads a string field without trusting the payload's shape. */
export function str(payload: Record<string, unknown>, key: string): string | undefined {
  const v = payload[key]
  return typeof v === 'string' ? v : undefined
}

export function num(payload: Record<string, unknown>, key: string): number | undefined {
  const v = payload[key]
  return typeof v === 'number' ? v : undefined
}

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'
