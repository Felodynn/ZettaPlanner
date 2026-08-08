import { useCallback, useEffect, useRef, useState } from 'react'
import { create } from 'zustand'
import { client, unwrap } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'

export type Lease = components['schemas']['LeaseView']

/**
 * The `lease.lost` channel (§7.2).
 *
 * The event arrives on the project stream, which is opened by the project
 * layout, while the lease is held by whichever editor is mounted. A tiny store
 * carries it between them rather than threading a callback through every
 * screen in the subtree.
 */
interface LostSignal {
  /** diagram id → why. */
  lost: Record<string, string>
  report: (diagramId: string, reason: string) => void
  clear: (diagramId: string) => void
}

export const useLeaseLost = create<LostSignal>((set) => ({
  lost: {},
  report: (diagramId, reason) => set((s) => ({ lost: { ...s.lost, [diagramId]: reason } })),
  clear: (diagramId) =>
    set((s) => {
      const { [diagramId]: _gone, ...rest } = s.lost
      return { lost: rest }
    }),
}))

export type LeaseState =
  | { status: 'acquiring' }
  | { status: 'held'; lease: Lease }
  /** Somebody else holds it — a 409 naming the holder. */
  | { status: 'denied'; holder: string; message: string; requestId: string }
  /** The caller may not edit this diagram at all — a 403. Different problem,
   *  different sentence: waiting will not help. */
  | { status: 'forbidden'; message: string; requestId: string }
  | { status: 'lost'; reason: string }
  | { status: 'released' }

/**
 * The edit session (D9, frontend-notes §6).
 *
 * Three things this has to get right, all of them easy to leave out:
 *
 * **Acquire on entering edit mode, not on first change.** A user who makes
 * twenty edits and *then* discovers somebody else holds the lease has lost
 * twenty edits.
 *
 * **Heartbeat only while the tab is visible.** A lease held by a backgrounded
 * tab blocks a diagram for its whole TTL, and a laptop lid closing must not
 * hold one for an hour. On hide the heartbeat continues briefly and then the
 * lease is released.
 *
 * **`lease.lost` means stop.** The save is blocked and the person is told,
 * rather than the client quietly re-acquiring — the whole point is that they
 * find out before they overwrite somebody.
 */
export function useLease(diagramId: string, enabled: boolean) {
  const [state, setState] = useState<LeaseState>({ status: 'acquiring' })
  const timers = useRef<{ beat?: number; grace?: number }>({})

  const clearTimers = () => {
    if (timers.current.beat) window.clearInterval(timers.current.beat)
    if (timers.current.grace) window.clearTimeout(timers.current.grace)
    timers.current = {}
  }

  // Only a lease this tab actually took is worth releasing. Releasing one we
  // never held answers 404, which is noise rather than information.
  const held = useRef(false)
  /** The acquire request in flight, shared so a re-run does not issue another. */
  const inFlight = useRef<Promise<Lease> | null>(null)

  const release = useCallback(async () => {
    clearTimers()
    inFlight.current = null
    if (!held.current) return
    held.current = false
    try {
      await client.DELETE('/api/v1/diagrams/{id}/lease', { params: { path: { id: diagramId } } })
    } catch {
      // Best effort: the lease expires on its own, which is what the TTL is for.
    }
  }, [diagramId])

  /** Marks the lease lost from outside — the SSE `lease.lost` event. */
  const markLost = useCallback((reason: string) => {
    clearTimers()
    setState({ status: 'lost', reason })
  }, [])

  useEffect(() => {
    if (!enabled || !diagramId) return
    let cancelled = false

    const beat = async () => {
      try {
        await client.POST('/api/v1/diagrams/{id}/lease/heartbeat', {
          params: { path: { id: diagramId } },
        })
      } catch (e) {
        if (cancelled) return
        // Somebody else has it now, or it lapsed while this tab was asleep.
        clearTimers()
        setState({
          status: 'lost',
          reason:
            e instanceof ApiError
              ? e.message
              : 'The edit session could not be renewed. Your changes are still here, but they cannot be saved against this session.',
        })
      }
    }

    const startBeating = (seconds: number) => {
      clearTimers()
      // Half the published interval: one missed beat should not lapse a lease.
      timers.current.beat = window.setInterval(() => void beat(), Math.max(seconds, 5) * 500)
    }

    const acquire = async () => {
      setState({ status: 'acquiring' })
      try {
        // One acquire per diagram, ever — a second effect pass awaits the
        // first request rather than issuing its own.
        //
        // React runs this effect, tears it down and runs it again. Two POSTs
        // meant the second one asked for a lease the first had already taken,
        // came back 409, and left the editor showing "somebody else is
        // editing" about itself — with saving blocked on a diagram this tab
        // owned.
        if (!inFlight.current) {
          inFlight.current = unwrap(
            client.POST('/api/v1/diagrams/{id}/lease', { params: { path: { id: diagramId } } }),
          )
        }
        const lease = await inFlight.current

        // Marked held *before* the cancellation check, because the server has
        // one now whatever this closure has decided. Setting it afterwards
        // meant an acquire resolving after teardown left a real lease nobody
        // knew to release — `release()` only acts on one it believes it took —
        // so the diagram stayed locked for its whole TTL every time somebody
        // opened the editor and left. On a shared project that is somebody
        // else's afternoon.
        held.current = true
        if (cancelled) return

        setState({ status: 'held', lease })
        startBeating(lease.heartbeat_seconds)
      } catch (e) {
        // Do not keep a rejected promise: a later attempt must ask again
        // rather than await the failure that already happened.
        inFlight.current = null
        if (cancelled) return
        if (e instanceof ApiError) {
          // A diagram's semantic layer is manager-owned in full (§4.2), so a
          // developer is refused the session outright. That is not the same
          // problem as somebody else holding it, and telling them to wait
          // would be wrong.
          if (e.status === 403 || e.code === 'forbidden' || e.code === 'field_not_owned') {
            setState({ status: 'forbidden', message: e.message, requestId: e.requestId })
            return
          }
          setState({
            status: 'denied',
            holder: e.detail('holder') ?? 'somebody else',
            // The backend names the holder and when their session lapses; it
            // is a better sentence than any paraphrase (§7.3).
            message: e.message,
            requestId: e.requestId,
          })
        } else {
          setState({
            status: 'denied',
            holder: 'unknown',
            message: e instanceof Error ? e.message : String(e),
            requestId: '',
          })
        }
      }
    }

    void acquire()

    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        // Keep it alive briefly — switching tabs to read something is not
        // leaving — and then let it go.
        timers.current.grace = window.setTimeout(() => {
          void release()
          setState({ status: 'released' })
        }, 60_000)
      } else if (timers.current.grace) {
        window.clearTimeout(timers.current.grace)
        delete timers.current.grace
      }
    }

    document.addEventListener('visibilitychange', onVisibility)

    // `lease.lost` from the project stream. Blocks saving immediately; the
    // client never silently re-acquires, because the point is that the person
    // finds out before they overwrite somebody (frontend-notes §6).
    const unsubscribe = useLeaseLost.subscribe((s) => {
      const reason = s.lost[diagramId]
      if (!reason || cancelled) return
      clearTimers()
      setState({ status: 'lost', reason })
      useLeaseLost.getState().clear(diagramId)
    })

    return () => {
      unsubscribe()
      cancelled = true
      document.removeEventListener('visibilitychange', onVisibility)
      clearTimers()
      void release()
    }
  }, [diagramId, enabled, release])

  return { state, release, markLost }
}
