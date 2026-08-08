import type { ReactNode } from 'react'
import { AlertTriangle, CircleDashed, CircleDot, CircleSlash, Eye, Lock, Check, Ban } from 'lucide-react'
import { Badge, type Tone } from '@/components/ui/primitives'
import type { MilestoneStatus, Priority, Severity, WorkItemStatus } from '@/api/vocabulary'
import { cn } from '@/lib/cn'

/**
 * Status, priority and severity as small, consistent marks.
 *
 * Shape and icon carry the meaning as well as colour: `done` and `dropped` are
 * both "not open", and a reader scanning a column has to be able to tell them
 * apart at a glance, because one is a finished piece of work and the other is
 * a decision not to do it (§3.5a).
 */

const STATUS: Record<WorkItemStatus, { tone: Tone; Icon: typeof CircleDot; label: string }> = {
  todo: { tone: 'neutral', Icon: CircleDashed, label: 'To do' },
  in_progress: { tone: 'accent', Icon: CircleDot, label: 'In progress' },
  blocked: { tone: 'danger', Icon: AlertTriangle, label: 'Blocked' },
  review: { tone: 'info', Icon: Eye, label: 'Review' },
  done: { tone: 'good', Icon: Check, label: 'Done' },
  dropped: { tone: 'outline', Icon: Ban, label: 'Dropped' },
}

export function StatusBadge({ status }: { status: WorkItemStatus }) {
  const s = STATUS[status]
  return (
    <Badge
      tone={s.tone}
      title={
        status === 'dropped'
          ? 'Dropped is not deleted. It is the decision not to do something, kept because it is worth keeping.'
          : s.label
      }
    >
      <s.Icon className="h-3 w-3" />
      {s.label}
    </Badge>
  )
}

const MILESTONE_STATUS: Record<MilestoneStatus, Tone> = {
  planned: 'neutral',
  active: 'accent',
  done: 'good',
  dropped: 'outline',
}

export function MilestoneStatusBadge({ status }: { status: MilestoneStatus }) {
  return (
    <Badge
      tone={MILESTONE_STATUS[status]}
      title="Milestone statuses are deliberately coarser than item statuses: blocked and review are facts about the items inside, not about the milestone."
    >
      {status}
    </Badge>
  )
}

const PRIORITY: Record<Priority, { tone: Tone; label: string }> = {
  low: { tone: 'neutral', label: 'Low' },
  normal: { tone: 'neutral', label: 'Normal' },
  high: { tone: 'warn', label: 'High' },
  urgent: { tone: 'danger', label: 'Urgent' },
}

/**
 * Priority, including the absence of one.
 *
 * §3.5a: an unset priority is not `normal`. An untriaged item and one
 * explicitly judged ordinary are different facts, and the dashboard counts the
 * first separately — so this renders "untriaged" rather than nothing.
 */
export function PriorityBadge({ priority }: { priority: Priority | undefined }) {
  if (!priority) {
    return (
      <Badge
        tone="outline"
        className="border-dashed"
        title="Nobody has triaged this. That is different from having judged it ordinary."
      >
        untriaged
      </Badge>
    )
  }
  const p = PRIORITY[priority]
  return <Badge tone={p.tone}>{p.label}</Badge>
}

const SEVERITY: Record<Severity, Tone> = { low: 'neutral', medium: 'warn', high: 'danger' }

export function SeverityBadge({ severity }: { severity: Severity }) {
  return <Badge tone={SEVERITY[severity]}>{severity}</Badge>
}

export function ProvenanceBadge({ provenance }: { provenance: string }) {
  if (provenance === 'human_created' || provenance === 'human_edited') return null
  const agent = provenance.startsWith('agent')
  return (
    <Badge
      tone={agent ? 'info' : 'warn'}
      title={
        agent
          ? 'Written by an agent, acting with the requesting user’s permissions and recorded against both.'
          : `Origin: ${provenance}`
      }
    >
      {provenance.replace(/_/g, ' ')}
    </Badge>
  )
}

/**
 * A field the caller does not own.
 *
 * FRONTEND F2: field ownership must be visible *before* it is enforced.
 * Letting a developer type into a due-date field and then rejecting the save
 * is the worst of both, so an unowned field renders as text with the owning
 * role named and a route offered — never as an input that will fail.
 */
export function ReadOnlyField({
  why,
  children,
  onAskPlanner,
}: {
  why: string
  children: ReactNode
  onAskPlanner?: () => void
}) {
  return (
    <div className="group flex items-start gap-1.5">
      <div className="min-w-0 flex-1">{children}</div>
      <button
        type="button"
        onClick={onAskPlanner}
        title={`${why}${onAskPlanner ? '\n\nClick to ask the planner to propose this.' : ''}`}
        className={cn(
          'mt-0.5 shrink-0 rounded p-0.5 text-ink-faint transition-colors',
          onAskPlanner ? 'hover:bg-sunken hover:text-ink-muted' : 'cursor-help',
        )}
        aria-label={why}
      >
        <Lock className="h-3 w-3" />
      </button>
    </div>
  )
}

/** The sentence itself, for places with room to show it. */
export function OwnershipNote({ why, className }: { why: string; className?: string }) {
  return (
    <p className={cn('flex items-start gap-1.5 text-[11px] text-ink-faint', className)}>
      <Lock className="mt-0.5 h-3 w-3 shrink-0" />
      <span>{why}</span>
    </p>
  )
}

export function DroppedMark() {
  return (
    <span className="inline-flex items-center gap-1 text-ink-faint" title="Dropped, not deleted">
      <CircleSlash className="h-3 w-3" />
    </span>
  )
}
