import { useSearchParams, useParams } from 'react-router-dom'
import { ListChecks } from 'lucide-react'
import { MilestoneStatusBadge } from './atoms'
import { WorkItemTable } from './WorkItemTable'
import { WarningList } from './Warnings'
import { filtersFromParams, useMilestones, useWarnings, useWorkItems } from './queries'
import { Loading } from '@/components/ui/primitives'
import { formatDayShort, isOverdue } from '@/lib/format'
import { cn } from '@/lib/cn'

export function PlanPage() {
  const { projectId = '' } = useParams()
  const [params, setParams] = useSearchParams()

  const filters = filtersFromParams(params)

  const milestones = useMilestones(projectId)
  const items = useWorkItems(projectId, filters)
  const warnings = useWarnings(projectId)

  const selectMilestone = (id: string) => {
    const next = new URLSearchParams(params)
    if (next.get('milestone') === id) next.delete('milestone')
    else next.set('milestone', id)
    setParams(next, { replace: true })
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-4">
        <ListChecks className="h-4 w-4 text-ink-faint" />
        <h1 className="font-semibold text-ink">Plan</h1>
        <span className="text-[11px] text-ink-faint">
          Linked to the architecture, never derived from it (§3.5)
        </span>
      </header>

      <div className="min-h-0 flex-1 overflow-auto zp-scroll">
        {/* Milestones read as a strip rather than a list: there are few of them,
            they are ordered, and their job here is to filter the table below. */}
        <section className="border-b border-line bg-surface px-4 py-3">
          {milestones.isPending ? (
            <Loading what="milestones" className="py-4" />
          ) : (milestones.data ?? []).length === 0 ? (
            <p className="text-ink-faint">No milestones yet. A manager creates them.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {(milestones.data ?? []).map((m) => {
                const selected = params.get('milestone') === m.id
                const late = isOverdue(m.due_date) && m.status !== 'done' && m.status !== 'dropped'
                return (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => selectMilestone(m.id)}
                    className={cn(
                      'min-w-52 rounded-card border px-3 py-2 text-left transition-colors',
                      selected
                        ? 'border-accent-line bg-accent-soft'
                        : 'border-line bg-surface hover:bg-sunken',
                    )}
                  >
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium text-ink">{m.name}</span>
                      <MilestoneStatusBadge status={m.status} />
                    </div>
                    {m.intent ? (
                      <p className="mt-0.5 line-clamp-1 text-xs text-ink-muted">{m.intent}</p>
                    ) : null}
                    <p className="zp-num mt-1 flex items-center gap-2 text-[11px] text-ink-faint">
                      <span>
                        {m.open_count} open of {m.item_count}
                      </span>
                      {m.due_date ? (
                        <span className={cn(late && 'font-medium text-danger-ink')}>
                          due {formatDayShort(m.due_date)}
                        </span>
                      ) : null}
                    </p>
                  </button>
                )
              })}
            </div>
          )}
        </section>

        {warnings.data && warnings.data.length > 0 ? (
          <section className="border-b border-warn/25 bg-warn-soft/40">
            <WarningList projectId={projectId} warnings={warnings.data} compact />
          </section>
        ) : null}

        <WorkItemTable
          projectId={projectId}
          items={items.data ?? []}
          milestones={milestones.data ?? []}
          warnings={warnings.data ?? []}
          loading={items.isPending}
        />
      </div>
    </div>
  )
}
