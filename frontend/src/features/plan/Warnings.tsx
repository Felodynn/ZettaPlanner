import { Link } from 'react-router-dom'
import { AlertTriangle } from 'lucide-react'
import type { Warning } from './queries'
import { Card, CardHeader, CardTitle } from '@/components/ui/primitives'
import { formatDay } from '@/lib/format'

/**
 * Date and dependency violations.
 *
 * Computed server-side and stored nowhere (§3.5). Render them; do not
 * recompute or cache — a stored warning is stale the moment a date moves, and
 * reads identically whether it is true now or was true in April.
 *
 * There is deliberately no "fix this" button. Moving a blocker's date produces
 * a warning and nothing else, because a cascade that is right 90% of the time
 * silently destroys the dates somebody set by hand, and they find out weeks
 * later. The backend's message already names both sides and says which of them
 * to move.
 */
export function WarningList({
  projectId,
  warnings,
  compact,
}: {
  projectId: string
  warnings: Warning[]
  compact?: boolean
}) {
  if (warnings.length === 0) return null

  const body = (
    <ul className="divide-y divide-line">
      {warnings.map((w) => (
        <li key={`${w.kind}-${w.item_id}-${w.related_id}`} className="flex gap-2.5 px-4 py-2">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" />
          <div className="min-w-0 flex-1">
            {/* The backend's sentence, unedited: it names both dates, the gap,
                and that nothing was rescheduled. */}
            <p className="text-ink">{w.message}</p>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-ink-faint">
              <Link
                to={`/p/${projectId}/plan/${w.item_id}`}
                className="font-mono text-accent-ink hover:underline"
              >
                {w.item_ref}
              </Link>
              {w.item_date ? <span className="zp-num">{formatDay(w.item_date)}</span> : null}
              <span>·</span>
              <span>
                {w.related_kind === 'milestone' ? 'milestone' : 'blocker'} {w.related_ref ?? ''}{' '}
                {w.related_name}
              </span>
              {w.related_date ? <span className="zp-num">{formatDay(w.related_date)}</span> : null}
            </p>
          </div>
        </li>
      ))}
    </ul>
  )

  if (compact) return body

  return (
    <Card className="border-warn/30">
      <CardHeader className="border-warn/20 bg-warn-soft">
        <CardTitle className="flex items-center gap-1.5 text-warn-ink">
          <AlertTriangle className="h-3.5 w-3.5" />
          {warnings.length} date warning{warnings.length === 1 ? '' : 's'}
        </CardTitle>
        <span className="text-[11px] text-warn-ink/80">Nothing has been rescheduled</span>
      </CardHeader>
      {body}
    </Card>
  )
}
