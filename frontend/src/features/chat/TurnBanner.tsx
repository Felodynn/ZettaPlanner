import { CircleSlash, Clock, Loader2, TriangleAlert } from 'lucide-react'
import type { Turn } from './queries'
import { useCancelTurn } from './queries'
import { Button } from '@/components/ui/Button'
import { formatDuration, formatTokens, humanise } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * What the agent is doing, and for whom.
 *
 * F1: the queue must be visible. When another user's turn is running, the
 * person running it is named — silence here reads as a broken app, and a
 * second user watching a send button do nothing has no way to tell a busy
 * queue from a bug.
 */

/** Why a turn ended, in words rather than an enum. */
const FINISH_MEANING: Record<string, string> = {
  finished: 'The agent finished.',
  no_output:
    'The turn produced no write, no proposal and no handoff. That is a non-success, not an answer — it usually means the agent hit a wall it could not name.',
  max_turns: 'The turn hit its step ceiling.',
  max_tokens: 'The turn hit its token ceiling.',
  max_wall_clock: 'The turn hit its time ceiling.',
  error: 'The turn failed.',
  cancelled: 'Somebody stopped this turn.',
  abandoned: 'The process running this turn did not survive. Nothing it had not already written was applied.',
}

export function TurnBanner({ turn, isMine }: { turn: Turn; isMine: boolean }) {
  const cancel = useCancelTurn()

  if (turn.status === 'running' || turn.status === 'queued') {
    const queued = turn.status === 'queued'
    return (
      <div
        className={cn(
          'flex items-center gap-2.5 border-t border-line px-4 py-2',
          queued ? 'bg-sunken' : 'bg-accent-soft',
        )}
      >
        {queued ? (
          <Clock className="h-4 w-4 shrink-0 text-ink-muted" />
        ) : (
          <Loader2 className="h-4 w-4 shrink-0 animate-spin text-accent-ink" />
        )}
        <span className="min-w-0 flex-1">
          {queued ? (
            <span className="text-ink">
              Queued
              {turn.queue_position > 0 ? (
                <>
                  {' '}
                  at position <span className="zp-num font-semibold">{turn.queue_position}</span>
                </>
              ) : null}
              {turn.requested_by ? (
                <>
                  {' '}
                  — {isMine ? 'yours' : `${turn.requested_by}'s`}
                </>
              ) : null}
              .{' '}
              <span className="text-ink-muted">
                One agent turn runs per project at a time (§5.3).
              </span>
            </span>
          ) : (
            <span className="text-accent-ink">
              <span className="font-semibold">
                {isMine ? 'Your turn' : `${turn.requested_by ?? 'Someone'}'s turn`}
              </span>{' '}
              is running
              {turn.model ? <span className="text-ink-muted"> on {turn.model}</span> : null}
              {turn.steps > 0 ? (
                <span className="zp-num text-ink-muted"> · step {turn.steps}</span>
              ) : null}
            </span>
          )}
        </span>
        {/* Anybody on the project may cancel: a turn holds the project's only
            slot, and requiring its author to be present is how a queue stays
            blocked all afternoon (§5.3). */}
        <Button
          size="sm"
          variant="ghost"
          loading={cancel.isPending}
          onClick={() => cancel.mutate(turn.id)}
          title="Stop this turn. Cancelling is a real outcome, not a failure."
        >
          <CircleSlash className="h-3.5 w-3.5" />
          Stop
        </Button>
      </div>
    )
  }

  // A finished turn is only worth a line when it did not simply work.
  if (turn.status === 'complete' && turn.finish_reason === 'finished') return null

  const bad = turn.status === 'failed' || turn.status === 'abandoned'
  const reason = turn.finish_reason ?? turn.status

  return (
    <div
      className={cn(
        'flex items-start gap-2.5 border-t px-4 py-2',
        bad ? 'border-danger/30 bg-danger-soft' : 'border-line bg-sunken',
      )}
    >
      <TriangleAlert
        className={cn('mt-0.5 h-4 w-4 shrink-0', bad ? 'text-danger' : 'text-ink-muted')}
      />
      <span className="min-w-0 flex-1">
        <span className={cn('font-medium', bad ? 'text-danger-ink' : 'text-ink')}>
          {humanise(reason)}
        </span>
        <span className="text-ink-muted"> — {FINISH_MEANING[reason] ?? ''}</span>
        {turn.error_detail ? (
          <span className="mt-0.5 block wrap-break-word text-ink-muted">{turn.error_detail}</span>
        ) : null}
        <span className="zp-num mt-0.5 block text-[11px] text-ink-faint">
          {turn.steps} steps · {formatTokens(turn.tokens_in)} in · {formatTokens(turn.tokens_out)}{' '}
          out · {formatDuration(turn.wall_ms)}
          {turn.retries > 0 ? ` · ${turn.retries} retry` : ''}
        </span>
      </span>
    </div>
  )
}
