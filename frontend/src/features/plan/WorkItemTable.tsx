import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { AlertTriangle, X } from 'lucide-react'
import { useHotkeys, type Hotkey } from '@/lib/useHotkeys'
import type { Milestone, WorkItem, Warning } from './queries'
import { PriorityBadge, StatusBadge } from './atoms'
import { Badge, Select, EmptyState, Loading } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { WORK_ITEM_STATUS_VALUES } from '@/api/vocabulary'
import { useMembers, useProject } from '@/features/project/queries'
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
  // This project's own prefix, not a hardcoded example. Now that projects are
  // created in the UI, "a ref like ZP-14" is wrong on every project that is
  // not ZP — and it is the first sentence somebody reads on an empty plan.
  const projectKey = useProject(projectId).data?.key ?? ''
  const [params, setParams] = useSearchParams()
  const members = useMembers(projectId)
  const navigate = useNavigate()

  /**
   * Keyboard navigation (F7).
   *
   * `j`/`k` move, `Enter` opens, `Escape` lets go. The cursor is an index
   * rather than DOM focus because the rows are links inside a table: moving
   * real focus would scroll the page on every keypress and fight the browser's
   * own tab order, and this way `Tab` still does what `Tab` does.
   */
  const [cursor, setCursor] = useState(-1)
  const rowRefs = useRef<(HTMLTableRowElement | null)[]>([])

  // A filter change reshuffles the list under the cursor, so it is dropped.
  //
  // Keyed on the serialized query, **not** on the `params` object:
  // `useSearchParams` hands back a new URLSearchParams instance on every
  // render, so depending on the object runs this effect after every render and
  // resets the cursor the instant the keyboard moves it — which looks exactly
  // like `j` being ignored.
  const query = params.toString()
  useEffect(() => {
    setCursor(-1)
  }, [query])

  /**
   * Move the cursor.
   *
   * Functional, because holding `j` fires faster than React re-renders and a
   * closure over `cursor` would make every press in one frame compute the same
   * next index — the list would move one row and stop. The scroll is an effect
   * on the result rather than a side effect inside the updater, since an
   * updater has to be pure and StrictMode runs it twice.
   */
  const move = (delta: number) => {
    setCursor((c) => Math.min(items.length - 1, Math.max(0, c < 0 ? 0 : c + delta)))
  }

  useEffect(() => {
    if (cursor >= 0) rowRefs.current[cursor]?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  const hotkeys = useMemo<Hotkey[]>(
    () => [
      { key: 'j', run: () => move(1), describe: 'Next item' },
      { key: 'k', run: () => move(-1), describe: 'Previous item' },
      {
        key: 'enter',
        run: () => {
          const item = items[cursor]
          if (item) navigate(`/p/${projectId}/plan/${item.id}`)
        },
        describe: 'Open the selected item',
      },
      { key: 'escape', run: () => setCursor(-1), describe: 'Clear the selection' },
    ],
    // `items` and `cursor` are read inside, so the bindings are rebuilt when
    // either moves. Cheap: this is four objects.
    [items, cursor, projectId, navigate],
  )
  useHotkeys(hotkeys)

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
        <span className="hidden text-[11px] text-ink-faint md:inline">
          <kbd className="rounded border border-line-strong px-1">J</kbd>
          <kbd className="ml-0.5 rounded border border-line-strong px-1">K</kbd> to move,{' '}
          <kbd className="rounded border border-line-strong px-1">Enter</kbd> to open
        </span>
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
              : `A manager creates work items. Each gets a ref like ${projectKey || 'ZP'}-1, issued once and never reissued.`
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
              {items.map((item, i) => {
                const itemWarnings = warningsByItem.get(item.id) ?? []
                const overdue = isOverdue(item.due_date) && item.status !== 'done'
                const inactive = item.status === 'done' || item.status === 'dropped'
                const selected = i === cursor

                return (
                  <tr
                    key={item.id}
                    ref={(el) => {
                      rowRefs.current[i] = el
                    }}
                    onMouseEnter={() => setCursor(-1)}
                    aria-selected={selected}
                    className={cn(
                      'border-b border-line/60 transition-colors hover:bg-sunken',
                      selected && 'bg-accent-soft',
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
