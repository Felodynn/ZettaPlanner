import { Minus, Pencil, Plus } from 'lucide-react'
import { changeSubject, fieldDiffs, humaniseTarget, type ProposalChange, type ProposalConflict } from './model'
import { Badge, Card } from '@/components/ui/primitives'
import { formatDay } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * Plan changes, field by field.
 *
 * F6: **field-level before/after**. "Updated ZP-14" is a line somebody skips;
 * `due_date  2026-08-06 → 2026-08-20` is a line they read and can disagree
 * with, which is the entire purpose of asking them.
 */
export function PlanDiff({
  changes,
  conflicts,
}: {
  changes: ProposalChange[]
  conflicts: ProposalConflict[]
}) {
  if (changes.length === 0) return null
  const bySeq = new Map<number, ProposalConflict[]>()
  for (const c of conflicts) bySeq.set(c.seq, [...(bySeq.get(c.seq) ?? []), c])

  return (
    <div className="flex flex-col gap-2">
      {changes.map((change) => (
        <ChangeCard
          key={change.seq}
          change={change}
          conflicts={bySeq.get(change.seq) ?? []}
        />
      ))}
    </div>
  )
}

const OP = {
  create: { Icon: Plus, label: 'create', tone: 'good' as const, tint: 'border-good/30' },
  update: { Icon: Pencil, label: 'change', tone: 'warn' as const, tint: 'border-warn/30' },
  delete: { Icon: Minus, label: 'delete', tone: 'danger' as const, tint: 'border-danger/30' },
}

function ChangeCard({
  change,
  conflicts,
}: {
  change: ProposalChange
  conflicts: ProposalConflict[]
}) {
  const op = OP[change.op]
  const diffs = fieldDiffs(change)

  return (
    <Card className={cn('overflow-hidden', conflicts.length > 0 ? 'border-danger/40' : op.tint)}>
      <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-3 py-2">
        <op.Icon
          className={cn(
            'h-3.5 w-3.5 shrink-0',
            change.op === 'create'
              ? 'text-good'
              : change.op === 'delete'
                ? 'text-danger'
                : 'text-warn',
          )}
        />
        <Badge tone={op.tone}>{op.label}</Badge>
        <span className="text-ink-faint">{humaniseTarget(change.target)}</span>
        <span className="font-medium text-ink">{changeSubject(change)}</span>
        <span className="zp-num ml-auto text-[11px] text-ink-faint">#{change.seq}</span>
      </div>

      {diffs.length === 0 ? (
        <p className="px-3 py-2 text-ink-muted">
          {change.op === 'delete'
            ? 'This object is removed. Deletion is soft — its history stays reachable and its ref is never reissued.'
            : 'No field would change. Approving this writes nothing.'}
        </p>
      ) : (
        <table className="w-full">
          <tbody>
            {diffs.map((d) => (
              <tr key={d.field} className="border-b border-line last:border-b-0">
                <td className="w-40 px-3 py-1.5 align-top text-ink-faint">{d.field}</td>
                <td className="px-3 py-1.5 align-top">
                  <Value value={d.before} field={d.field} muted />
                  <span className="mx-2 text-ink-faint">→</span>
                  <Value value={d.after} field={d.field} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {conflicts.map((c, i) => (
        <div key={i} className="border-t border-danger/30 bg-danger-soft px-3 py-2">
          <p className="font-medium text-danger-ink">
            {c.element} moved since this was raised
          </p>
          {/* The backend names the field, the value it was raised against and
              the value now. All three, verbatim. */}
          <p className="text-ink">
            {c.reason ??
              `${c.field ?? 'a field'} was ${quote(c.expected)} when this proposal was raised, and is ${quote(c.actual)} now.`}
          </p>
        </div>
      ))}
    </Card>
  )
}

function quote(v: string | undefined): string {
  return v === undefined || v === '' ? '(unset)' : `“${v}”`
}

/**
 * One value, rendered as what it is.
 *
 * Calendar days are strings end to end (frontend-notes §10) — formatted here
 * for reading, never parsed through a Date and back.
 */
function Value({ value, field, muted }: { value: unknown; field: string; muted?: boolean }) {
  const cls = cn('rounded px-1 py-0.5 font-mono text-[11px]', muted ? 'text-ink-faint line-through' : 'text-ink')

  if (value === undefined || value === null || value === '') {
    return <span className={cn(cls, 'italic')}>unset</span>
  }
  if (typeof value === 'string' && /_date$/.test(field) && /^\d{4}-\d{2}-\d{2}$/.test(value)) {
    return <span className={cls}>{formatDay(value)}</span>
  }
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return <span className={cls}>{String(value)}</span>
  }
  return <span className={cls}>{JSON.stringify(value)}</span>
}
