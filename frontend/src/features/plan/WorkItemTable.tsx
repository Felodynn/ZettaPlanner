import { Link, useSearchParams } from 'react-router-dom'
import { AlertTriangle, X } from 'lucide-react'
import type { Milestone, WorkItem, Warning } from './queries'
import { PriorityBadge, StatusBadge } from './atoms'
import { Badge, Select, EmptyState, Loading } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { WORK_ITEM_STATUS_VALUES } from '@/api/vocabulary'
import { useMembers } from '@/features/project/queries'
import { formatDayShort, isOverdue } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * The work item table.
 *
 * Filters live in the query string so a filtered view is a link — F2 requires
 * them deep-linkable, and "the blocked items in M1" is exactly the thing
 * somebody pastes into the thread.
 */
export function WorkItemTable({
  projectId,
  items,
  milestones,
  warnings,
  loading,
}: {
  projectId: string
  items: WorkItem[]
  milestones: Milestone[]
  warnings: Warning[]
  loading: boolean
}) {
  const [params, setParams] = useSearchParams()
  const members = useMembers(projectId)

  const assignee = params.get('assignee') ?? ''
  const status = params.get('status') ?? ''
  const milestone = params.get('milestone') ?? ''
  const filtered = Boolean(assignee || status || milestone)

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next, { replace: true })
  }

  const warningsByItem = new Map<string, Warning[]>()
  for (const w of warnings) {
    warningsByItem.set(w.item_id, [...(warningsByItem.get(w.item_id) ?? []), w])
  }

  const milestoneName = new Map(milestones.map((m) => [m.id, m.name]))
  const memberName = new Map((members.data ?? []).map((m) => [m.user_id, m.display_name]))

  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2">
        <Select
          value={status}
          onChange={(e) => setFilter('status', e.target.value)}
          className="h-7 w-auto text-xs"
          aria-label="Filter by status"
        >
          <option value="">Any status</option>
          {WORK_ITEM_STATUS_VALUES.map((s) => (
            <option key={s} value={s}>
              {s.replace(/_/g, ' ')}
            </option>
          ))}
        </Select>

        <Select
          value={assignee}
          onChange={(e) => setFilter('assignee', e.target.value)}
          className="h-7 w-auto text-xs"
          aria-label="Filter by assignee"
        >
          <option value="">Anyone</option>
          {(members.data ?? []).map((m) => (
            <option key={m.user_id} value={m.user_id}>
              {m.display_name}
            </option>
          ))}
        </Select>

        <Select
          value={milestone}
          onChange={(e) => setFilter('milestone', e.target.value)}
          className="h-7 w-auto text-xs"
          aria-label="Filter by milestone"
        >
          <option value="">Any milestone</option>
          {milestones.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
            </option>
          ))}
        </Select>

        {filtered ? (
          <Button size="sm" variant="ghost" onClick={() => setParams({}, { replace: true })}>
            <X className="h-3 w-3" />
            Clear
          </Button>
        ) : null}

        <div className="flex-1" />
        <span className="zp-num text-xs text-ink-faint">
          {loading ? '' : `${items.length} item${items.length === 1 ? '' : 's'}`}
        </span>
      </div>

      {loading ? (
        <Loading what="work items" />
      ) : items.length === 0 ? (
        <EmptyState
          title={filtered ? 'Nothing matches these filters' : 'No work items yet'}
          body={
            filtered
              ? 'Clear a filter, or widen one.'
              : 'A manager creates work items. Each gets a ref like ZP-14, issued once and never reissued.'
          }
        />
      ) : (
        <div className="overflow-x-auto zp-scroll">
          <table className="w-full min-w-4xl border-collapse">
            <thead>
              <tr className="border-b border-line text-left text-[11px] tracking-wide text-ink-faint uppercase">
                <th className="px-4 py-1.5 font-semibold">Ref</th>
                <th className="px-2 py-1.5 font-semibold">Title</th>
                <th className="px-2 py-1.5 font-semibold">Status</th>
                <th className="px-2 py-1.5 font-semibold">Priority</th>
                <th className="px-2 py-1.5 font-semibold">Assignee</th>
                <th className="px-2 py-1.5 font-semibold">Milestone</th>
                <th className="px-2 py-1.5 font-semibold">Due</th>
                <th className="px-4 py-1.5 text-right font-semibold">Progress</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => {
                const itemWarnings = warningsByItem.get(item.id) ?? []
                const overdue = isOverdue(item.due_date) && item.status !== 'done'
                const inactive = item.status === 'done' || item.status === 'dropped'

                return (
                  <tr
                    key={item.id}
                    className={cn(
                      'border-b border-line/60 transition-colors hover:bg-sunken',
                      item.deleted_at && 'opacity-50',
                    )}
                  >
                    <td className="px-4 py-1.5 align-top">
                      <Link
                        to={`/p/${projectId}/plan/${item.id}`}
                        className="font-mono text-xs text-accent-ink hover:underline"
                      >
                        {item.ref}
                      </Link>
                    </td>
                    <td className="max-w-md px-2 py-1.5 align-top">
                      <Link
                        to={`/p/${projectId}/plan/${item.id}`}
                        className={cn('hover:underline', inactive && 'text-ink-muted')}
                      >
                        {item.title}
                      </Link>
                      {item.deleted_at ? (
                        <Badge tone="outline" className="ml-1.5">
                          deleted
                        </Badge>
                      ) : null}
                      {itemWarnings.length > 0 ? (
                        <span
                          className="ml-1.5 inline-flex align-middle text-warn"
                          title={itemWarnings.map((w) => w.message).join('\n\n')}
                        >
                          <AlertTriangle className="h-3.5 w-3.5" />
                        </span>
                      ) : null}
                    </td>
                    <td className="px-2 py-1.5 align-top">
                      <StatusBadge status={item.status} />
                    </td>
                    <td className="px-2 py-1.5 align-top">
                      <PriorityBadge priority={item.priority} />
                    </td>
                    <td className="px-2 py-1.5 align-top text-ink-muted">
                      {item.assignee_id ? (
                        (memberName.get(item.assignee_id) ?? '—')
                      ) : (
                        <span className="text-ink-faint">Unassigned</span>
                      )}
                    </td>
                    <td className="px-2 py-1.5 align-top text-ink-muted">
                      {item.milestone_id ? (
                        (milestoneName.get(item.milestone_id) ?? '—')
                      ) : (
                        <span className="text-ink-faint">—</span>
                      )}
                    </td>
                    <td
                      className={cn(
                        'zp-num px-2 py-1.5 align-top whitespace-nowrap',
                        overdue ? 'font-medium text-danger-ink' : 'text-ink-muted',
                      )}
                    >
                      {formatDayShort(item.due_date)}
                    </td>
                    <td className="px-4 py-1.5 text-right align-top">
                      <span className="zp-num text-xs text-ink-muted">{item.progress_pct}%</span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
