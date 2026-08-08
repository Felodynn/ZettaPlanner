import { useState } from 'react'
import { Brain, X } from 'lucide-react'
import { Textarea } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'

/** §5.2. The backend's floor, mirrored so the refusal is not a surprise. */
const MIN_RATIONALE = 8

/**
 * Rejecting, with the reason the box exists.
 *
 * frontend-notes §8 is blunt about this: the rationale is **fed back into the
 * agent's context**, and the measured effect is that it stops re-proposing what
 * was refused. A UI that makes the box feel like an obstacle gets "nope" typed
 * into it, and the loop that made this worth building does not close.
 *
 * So the purpose is stated before the box, not in a tooltip after it — and the
 * examples are there because "what do I write" is the actual reason people type
 * one word.
 */
export function RejectDialog({
  title,
  onReject,
  onClose,
  rejecting,
}: {
  title: string
  onReject: (rationale: string) => void
  onClose: () => void
  rejecting: boolean
}) {
  const [rationale, setRationale] = useState('')
  const trimmed = rationale.trim()
  const short = trimmed.length < MIN_RATIONALE

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-canvas/70 p-6">
      <div className="zp-shadow-pop w-full max-w-xl rounded-card border border-line bg-raised">
        <div className="flex items-start gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1">
            <p className="font-semibold text-ink">Refuse this proposal</p>
            <p className="truncate text-ink-muted">{title}</p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="px-4 py-3">
          <div className="flex items-start gap-2 rounded-md border border-info/30 bg-info-soft px-3 py-2">
            <Brain className="mt-0.5 h-4 w-4 shrink-0 text-info" />
            <p className="text-ink">
              <span className="font-medium">This goes back to the planner.</span> Rejected proposals
              and their reasons are read as context on every later turn, so a reason written here is
              what stops the same thing being proposed again next week. “No” is the one answer that
              teaches it nothing.
            </p>
          </div>

          <label className="mt-3 block">
            <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
              Why are you refusing?
            </span>
            <Textarea
              autoFocus
              rows={4}
              className="mt-1"
              value={rationale}
              onChange={(e) => setRationale(e.target.value)}
              placeholder="e.g. The date is fine — ZP-8 is blocked on a schema change, not on effort, so moving the due date hides the problem rather than fixing it."
            />
          </label>

          <p className="mt-1 text-[11px] text-ink-faint">
            {short
              ? `${MIN_RATIONALE - trimmed.length} more character${MIN_RATIONALE - trimmed.length === 1 ? '' : 's'} — the minimum exists because one-word refusals were what the agent kept being given.`
              : 'The proposal is refused, not deleted. It stays readable with this reason attached.'}
          </p>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-line px-4 py-3">
          <Button variant="ghost" onClick={onClose} disabled={rejecting}>
            Cancel
          </Button>
          <Button
            variant="danger"
            loading={rejecting}
            disabled={short || rejecting}
            onClick={() => onReject(trimmed)}
          >
            Reject with this reason
          </Button>
        </div>
      </div>
    </div>
  )
}
