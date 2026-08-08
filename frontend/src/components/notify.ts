import { create } from 'zustand'
import { ApiError, TransportError } from '@/api/errors'

export type NoticeKind = 'error' | 'success' | 'info' | 'warn'

export interface NoticeAction {
  label: string
  onClick: () => void
}

export interface Notice {
  id: number
  kind: NoticeKind
  /** A short heading. For an ApiError this is the contract's `code`. */
  title: string
  /**
   * The backend's sentence, verbatim.
   *
   * frontend-notes §3: a denial's wording is a prompt layer. Every 403 names
   * the owning role *and* the route forward, in a sentence written to produce
   * an action, and a UI that replaces it with "Permission denied" throws away
   * the half that tells somebody what to do next. Nothing paraphrases this.
   */
  message: string
  /** Copyable on every error surface. It is what turns "it broke" into a row
   *  in the audit log. */
  requestId?: string
  details?: Record<string, unknown>
  action?: NoticeAction
  /** Errors stay until dismissed; confirmations do not. */
  timeout: number | null
}

interface NoticeStore {
  notices: Notice[]
  push: (n: Omit<Notice, 'id'>) => number
  dismiss: (id: number) => void
  clear: () => void
}

let nextId = 1

export const useNotices = create<NoticeStore>((set) => ({
  notices: [],
  push: (n) => {
    const id = nextId++
    set((s) => ({ notices: [...s.notices, { ...n, id }] }))
    return id
  },
  dismiss: (id) => set((s) => ({ notices: s.notices.filter((n) => n.id !== id) })),
  clear: () => set({ notices: [] }),
}))

const CODE_TITLES: Record<string, string> = {
  unauthenticated: 'Signed out',
  forbidden: 'Not allowed',
  field_not_owned: 'That field belongs to another role',
  not_found: 'Not found',
  conflict: 'Conflict',
  lease_held: 'Someone else is editing',
  stale_base_revision: 'Someone else saved first',
  validation_failed: 'That will not save',
  turn_in_progress: 'The agent is busy',
  model_unavailable: 'The model is unavailable',
  context_overflow: 'Too much context',
  internal: 'Something went wrong',
}

/**
 * Reports a thrown value.
 *
 * The ApiError branch keeps every part of the envelope: the code names the
 * kind of failure, the message is the backend's own words, and the request_id
 * is what makes the failure findable afterwards.
 */
export function notifyError(e: unknown, action?: NoticeAction): void {
  const push = useNotices.getState().push

  if (e instanceof ApiError) {
    push({
      kind: e.code === 'lease_held' || e.code === 'stale_base_revision' ? 'warn' : 'error',
      title: CODE_TITLES[e.code] ?? e.code,
      message: e.message,
      requestId: e.requestId,
      details: e.details,
      ...(action ? { action } : {}),
      timeout: null,
    })
    return
  }

  if (e instanceof TransportError) {
    push({ kind: 'error', title: 'Cannot reach the server', message: e.message, timeout: null })
    return
  }

  push({
    kind: 'error',
    title: 'Something went wrong',
    message: e instanceof Error ? e.message : String(e),
    timeout: null,
  })
}

export function notifySuccess(message: string, title = 'Done'): void {
  useNotices.getState().push({ kind: 'success', title, message, timeout: 4000 })
}

export function notifyInfo(message: string, title = 'Note'): void {
  useNotices.getState().push({ kind: 'info', title, message, timeout: 6000 })
}

export function notifyWarn(message: string, title = 'Careful'): void {
  useNotices.getState().push({ kind: 'warn', title, message, timeout: null })
}
