import { Link, useParams } from 'react-router-dom'
import { AlertTriangle, CircleUser } from 'lucide-react'
import { useSession } from '@/auth/session'
import { Badge, Card, CardHeader, CardTitle, EmptyState, Loading } from '@/components/ui/primitives'
import { PriorityBadge, StatusBadge } from './atoms'
import { useDeviations, useWarnings, useWorkItems } from './queries'
import { formatDayShort, daysUntil } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * The daily driver.
 *
 * Ordered by what is actually pressing rather than by date alone: an overdue
 * blocked item and an overdue in-progress one are different problems, and a
 * flat date sort buries the first among things nobody can act on.
 */
export function MyTasks() {
  const { projectId = '' } = useParams()
  const { user } = useSession()

  const items = useWorkItems(projectId, { assignee: user.id })
  const warnings = useWarnings(projectId)
  const deviations = useDeviations(projectId)

  if (items.isPending) return <Loading what="your items" />
  if (items.error) throw items.error

  const open = items.data.filter(
    (i) => i.status !== 'done' && i.status !== 'dropped' && !i.deleted_at,
  )
  const closed = items.data.filter(
    (i) => (i.status === 'done' || i.status === 'dropped') && !i.deleted_at,
  )

  const RANK: Record<string, number> = { blocked: 0, in_progress: 1, review: 2, todo: 3 }
  const sorted = [...open].sort((a, b) => {
    const overdueA = a.due_date ? Math.min(daysUntil(a.due_date) ?? 0, 0) : 0
    const overdueB = b.due_date ? Math.min(daysUntil(b.due_date) ?? 0, 0) : 0
    if (overdueA !== overdueB) return overdueA - overdueB
    const rank = (RANK[a.status] ?? 9) - (RANK[b.status] ?? 9)
    if (rank !== 0) return rank
    return (a.due_date ?? '9999').localeCompare(b.due_date ?? '9999')
  })

  const myWarnings = (warnings.data ?? []).filter((w) =>
    open.some((i) => i.id === w.item_id),
  )
  const myDeviations = (deviations.data ?? []).filter((d) => !d.resolved)

  return (
    <div className="mx-auto w-full max-w-4xl px-6 py-6">
      <div className="flex items-center gap-2">
        <CircleUser className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">My tasks</h1>
        <span className="zp-num text-ink-faint">{open.length} open</span>
      </div>

      {open.length === 0 ? (
        <EmptyState
          className="pt-16"
          title="Nothing assigned to you is open"
          body="Work items are assigned by a manager. Anything you have finished is below."
        />
      ) : (
        <ul className="mt-4 flex flex-col gap-1.5">
          {sorted.map((item) => {
            const days = item.due_date ? daysUntil(item.due_date) : null
            const overdue = days !== null && days < 0
            const soon = days !== null && days >= 0 && days <= 3
            const itemWarnings = myWarnings.filter((w) => w.item_id === item.id)

            return (
              <li key={item.id}>
                <Link to={`/p/${projectId}/plan/${item.id}`}>
                  <Card
                    className={cn(
                      'flex items-center gap-3 px-3 py-2 transition-colors hover:bg-sunken',
                      overdue && 'border-danger/30',
                    )}
                  >
                    <span className="w-14 shrink-0 font-mono text-xs text-ink-faint">
                      {item.ref}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-ink">{item.title}</span>
                      {itemWarnings.length > 0 ? (
                        <span className="mt-0.5 flex items-center gap-1 text-[11px] text-warn-ink">
                          <AlertTriangle className="h-3 w-3 shrink-0" />
                          <span className="truncate">{itemWarnings[0]?.message}</span>
                        </span>
                      ) : null}
                    </span>
                    <StatusBadge status={item.status} />
                    <PriorityBadge priority={item.priority} />
                    <span
                      className={cn(
                        'zp-num w-24 shrink-0 text-right text-xs',
                        overdue
                          ? 'font-medium text-danger-ink'
                          : soon
                            ? 'text-warn-ink'
                            : 'text-ink-faint',
                      )}
                    >
                      {item.due_date ? (
                        overdue ? (
                          `${-days!}d overdue`
                        ) : days === 0 ? (
                          'due today'
                        ) : (
                          formatDayShort(item.due_date)
                        )
                      ) : (
                        <span className="text-ink-faint">no date</span>
                      )}
                    </span>
                  </Card>
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      {myDeviations.length > 0 ? (
        <Card className="mt-5">
          <CardHeader>
            <CardTitle>Open deviations on this project</CardTitle>
            <span className="zp-num text-[11px] text-ink-faint">{myDeviations.length}</span>
          </CardHeader>
          <ul className="divide-y divide-line">
            {myDeviations.map((d) => (
              <li key={d.id} className="flex items-start gap-2 px-4 py-2">
                <Badge tone={d.severity === 'high' ? 'danger' : d.severity === 'medium' ? 'warn' : 'neutral'}>
                  {d.severity}
                </Badge>
                <span className="min-w-0 flex-1 text-ink">{d.summary}</span>
                {d.item_ref ? (
                  <span className="shrink-0 font-mono text-[11px] text-ink-faint">{d.item_ref}</span>
                ) : null}
              </li>
            ))}
          </ul>
        </Card>
      ) : null}

      {closed.length > 0 ? (
        <details className="mt-5">
          <summary className="cursor-pointer text-ink-muted hover:text-ink">
            {closed.length} finished or dropped
          </summary>
          <ul className="mt-2 flex flex-col gap-1">
            {closed.map((item) => (
              <li key={item.id}>
                <Link
                  to={`/p/${projectId}/plan/${item.id}`}
                  className="flex items-center gap-3 rounded px-3 py-1.5 hover:bg-sunken"
                >
                  <span className="w-14 shrink-0 font-mono text-xs text-ink-faint">{item.ref}</span>
                  <span className="min-w-0 flex-1 truncate text-ink-muted">{item.title}</span>
                  <StatusBadge status={item.status} />
                </Link>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  )
}
