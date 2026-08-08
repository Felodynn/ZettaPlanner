import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useStream } from './streamStore'
import { str, type ConnectionState, type ProjectEvent } from './types'
import { threadKeys } from '@/features/chat/queries'
import { projectKeys } from '@/features/project/queries'
import { useLeaseLost } from '@/features/diagram/useLease'

/**
 * One SSE connection per project, not per view (frontend-notes §5).
 *
 * Mounted once by the project layout. Every screen underneath reads the same
 * stream, so opening four panels does not open four connections — and the
 * server's replay ring is per connection, so four of them would each have
 * their own idea of what had been missed.
 */

interface EventsValue {
  state: ConnectionState
  /** The last event id seen, for display when diagnosing a stuck stream. */
  lastId: number | null
  /** Set when the server said it could not replay and everything was refetched. */
  resyncedAt: number | null
}

const EventsContext = createContext<EventsValue>({
  state: 'closed',
  lastId: null,
  resyncedAt: null,
})

export function useProjectEvents(): EventsValue {
  return useContext(EventsContext)
}

/**
 * Maps an event onto the queries it invalidates.
 *
 * Deliberately coarse. The events name a subject, not a new object, so the
 * honest response to "something in the plan changed" is to refetch the plan
 * rather than to guess which row and patch it.
 */
function invalidate(qc: QueryClient, projectId: string, e: ProjectEvent): void {
  const t = e.type

  if (t.startsWith('turn.')) {
    void qc.invalidateQueries({ queryKey: threadKeys.turns(projectId) })
  }

  if (t === 'turn.queued' || t === 'turn.ended') {
    // Somebody posted, or an agent finished writing into the thread.
    void qc.invalidateQueries({ queryKey: threadKeys.allMessages() })
  }

  if (t.startsWith('plan.')) {
    void qc.invalidateQueries({ queryKey: ['plan', projectId] })
    void qc.invalidateQueries({ queryKey: ['dashboard', projectId] })
    void qc.invalidateQueries({ queryKey: ['warnings', projectId] })
  }

  if (t.startsWith('proposal.')) {
    void qc.invalidateQueries({ queryKey: ['proposals', projectId] })
    // An applied proposal is one revision against a diagram or the plan.
    void qc.invalidateQueries({ queryKey: ['diagrams'] })
    void qc.invalidateQueries({ queryKey: ['plan', projectId] })
  }

  if (t.startsWith('diagram.')) {
    void qc.invalidateQueries({ queryKey: ['diagrams'] })
  }

  if (t.startsWith('ingest.')) {
    void qc.invalidateQueries({ queryKey: ['ingests', projectId] })
  }

  if (t.startsWith('note.')) {
    void qc.invalidateQueries({ queryKey: ['notes', projectId] })
  }

  if (t === 'digest.updated') {
    void qc.invalidateQueries({ queryKey: ['digest', projectId] })
  }
}

export function ProjectEventsProvider({
  projectId,
  children,
}: {
  projectId: string
  children: ReactNode
}) {
  const qc = useQueryClient()
  const [state, setState] = useState<ConnectionState>('connecting')
  const [lastId, setLastId] = useState<number | null>(null)
  const [resyncedAt, setResyncedAt] = useState<number | null>(null)

  // Read inside the handler rather than closed over, so the effect does not
  // re-open the connection every time a delta arrives.
  const lastIdRef = useRef<number | null>(null)

  useEffect(() => {
    useStream.getState().clearAll()
    lastIdRef.current = null
    setState('connecting')

    let es: EventSource | null = null
    let retryTimer: number | undefined
    let attempt = 0
    let stopped = false

    const onMessage = (raw: MessageEvent<string>) => {
      let e: ProjectEvent
      try {
        e = JSON.parse(raw.data) as ProjectEvent
      } catch {
        return
      }
      if (typeof e.id === 'number') {
        lastIdRef.current = e.id
        setLastId(e.id)
      }

      switch (e.type) {
        case 'message.delta': {
          const turnId = str(e.payload, 'turn_id')
          const text = str(e.payload, 'text')
          if (turnId && text) useStream.getState().append(turnId, text)
          break
        }

        case 'message.complete': {
          // The payload carries the text but no message id or sequence, so
          // the canonical record is fetched rather than assembled here. That
          // is the right shape anyway: F1 says deltas never persist as the
          // record, and the overlay is held until the refetch lands so the
          // answer does not blink out and back in.
          const turnId = str(e.payload, 'turn_id')
          if (turnId) useStream.getState().settle(turnId)
          void qc
            .invalidateQueries({ queryKey: threadKeys.allMessages() })
            .then(() => {
              if (turnId) useStream.getState().clear(turnId)
            })
          break
        }

        case 'turn.ended': {
          const turnId = str(e.payload, 'turn_id')
          void qc.invalidateQueries({ queryKey: threadKeys.allMessages() }).then(() => {
            if (turnId) useStream.getState().clear(turnId)
          })
          invalidate(qc, projectId, e)
          break
        }

        case 'lease.lost': {
          const diagramId = str(e.payload, 'diagram_id')
          if (diagramId) {
            useLeaseLost
              .getState()
              .report(
                diagramId,
                str(e.payload, 'reason') ??
                  'The edit session for this diagram was taken over or expired.',
              )
          }
          break
        }

        case 'resync': {
          // The gap was wider than the server's replay ring. It says so
          // rather than resuming from a hole, and the only correct answer is
          // to refetch — patching state forward across a gap is how a view
          // ends up quietly wrong (frontend-notes §5).
          useStream.getState().clearAll()
          void qc.invalidateQueries()
          setResyncedAt(Date.now())
          // Everything has just been refetched, so the old resume point is
          // meaningless — and after a restart it is worse than meaningless,
          // because the server's ids begin again and this one is ahead of
          // them. Dropping it makes the next reconnect a new client, which
          // has missed nothing by definition.
          lastIdRef.current = null
          break
        }

        default:
          invalidate(qc, projectId, e)
      }
    }

    // The backend names every event, so nothing arrives as a bare `message`.
    const named = [
      'turn.queued',
      'turn.started',
      'turn.ended',
      'message.delta',
      'message.complete',
      'plan.changed',
      'proposal.created',
      'proposal.decided',
      'proposal.applied',
      'proposal.superseded',
      'diagram.created',
      'diagram.revised',
      'diagram.updated',
      'diagram.deleted',
      'diagram.layout_mode_changed',
      'lease.lost',
      'ingest.created',
      'ingest.extracted',
      'ingest.merged',
      'ingest.accepted',
      'ingest.failed',
      'ingest.page_retried',
      'note.created',
      'note.updated',
      'note.status_changed',
      'resync',
    ]

    /**
     * Opens the stream, and keeps it open.
     *
     * EventSource reconnects by itself, but **only after a transport-level
     * failure**. A response that arrives with an HTTP error status closes it
     * permanently, by specification — and that is exactly what a reverse proxy
     * produces while the backend behind it restarts. Left to itself, every
     * client would sit dead until somebody reloaded the page, which is the
     * failure this supervisor exists to prevent.
     *
     * Reconnecting by hand means resending the resume point by hand: a fresh
     * EventSource has no `Last-Event-ID` to offer, so it goes on the query
     * string, which is why the backend reads it from both places.
     */
    const connect = () => {
      if (stopped) return

      const since = lastIdRef.current
      const url =
        since === null
          ? `/api/v1/projects/${projectId}/events`
          : `/api/v1/projects/${projectId}/events?last_event_id=${since}`

      const source = new EventSource(url)
      es = source

      source.onopen = () => {
        attempt = 0
        setState('open')
      }

      source.onmessage = onMessage
      for (const name of named) source.addEventListener(name, onMessage as EventListener)

      source.onerror = () => {
        if (stopped) return
        setState('reconnecting')

        // Only supervise the terminal case. While EventSource is still
        // retrying on its own, stepping in would open a second connection
        // beside the one it is about to make.
        if (source.readyState !== EventSource.CLOSED) return

        source.close()
        attempt += 1
        // 1s, 2s, 4s, 8s, capped. A backend that is restarting is back in
        // seconds; one that is gone is not worth hammering.
        const delay = Math.min(1000 * 2 ** (attempt - 1), 15_000)
        retryTimer = window.setTimeout(connect, delay)
      }
    }

    connect()

    return () => {
      stopped = true
      window.clearTimeout(retryTimer)
      if (es) {
        for (const name of named) es.removeEventListener(name, onMessage as EventListener)
        es.close()
      }
      useStream.getState().clearAll()
    }
  }, [projectId, qc])

  // A project the caller was removed from, or a renamed one, is worth noticing
  // on reconnect.
  useEffect(() => {
    if (state === 'open') void qc.invalidateQueries({ queryKey: projectKeys.detail(projectId) })
  }, [state, projectId, qc])

  return (
    <EventsContext.Provider value={{ state, lastId, resyncedAt }}>{children}</EventsContext.Provider>
  )
}
