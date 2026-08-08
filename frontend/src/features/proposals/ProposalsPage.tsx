import { Link, useParams, useSearchParams } from 'react-router-dom'
import { Bot, GitPullRequestArrow, ListChecks, Network, Shuffle, User } from 'lucide-react'
import { useProposals } from './queries'
import { useActorNames } from './useActorNames'
import { attribution, countByOp, type Proposal } from './model'
import { PROPOSAL_STATUS_VALUES, type ProposalStatus } from '@/api/vocabulary'
import { Badge, Card, EmptyState, Loading, type Tone } from '@/components/ui/primitives'
import { list } from '@/api/client'
import { formatRelative } from '@/lib/format'
import { cn } from '@/lib/cn'

const STATUS_TONE: Record<ProposalStatus, Tone> = {
  pending: 'warn',
  approved: 'good',
  rejected: 'neutral',
  superseded: 'danger',
}

const TARGET_ICON = {
  diagram: Network,
  plan: ListChecks,
  mixed: Shuffle,
} as const

type Filter = ProposalStatus | 'all'

/**
 * The queue.
 *
 * F6: it shows actor, rationale and target — the three things that decide
 * whether a proposal is worth opening. An agent's proposal is attributed to
 * both the agent and the person who instructed it, because "who decided this"
 * has two answers and the audit trail keeps both (§6.5).
 */
export function ProposalsPage() {
  const { projectId = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const raw = params.get('status') ?? 'pending'
  const filter: Filter = (PROPOSAL_STATUS_VALUES as readonly string[]).includes(raw)
    ? (raw as ProposalStatus)
    : 'all'

  const proposals = useProposals(projectId, filter)
  const nameFor = useActorNames(projectId)

  const setFilter = (next: Filter) => {
    const p = new URLSearchParams(params)
    p.set('status', next)
    setParams(p, { replace: true })
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <GitPullRequestArrow className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Proposals</h1>
      </div>
      <p className="mt-0.5 text-ink-muted">
        The governed route through field ownership, in both directions: an agent proposes what it
        may not write, and an approved proposal carries write authority a direct patch would not.
        Every one is applied whole or not at all.
      </p>

      <div className="mt-4 flex items-center gap-1">
        {(['pending', 'approved', 'rejected', 'superseded', 'all'] as const).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => setFilter(f)}
            className={cn(
              'rounded-md px-2.5 py-1 transition-colors',
              filter === f
                ? 'bg-accent-soft font-medium text-accent-ink'
                : 'text-ink-muted hover:bg-sunken hover:text-ink',
            )}
          >
            {f === 'all' ? 'All' : f[0]?.toUpperCase() + f.slice(1)}
          </button>
        ))}
      </div>

      {proposals.isPending ? (
        <Loading what="proposals" />
      ) : proposals.error ? (
        <p className="py-4 text-ink-muted">{proposals.error.message}</p>
      ) : proposals.data.length === 0 ? (
        <EmptyState
          className="pt-16"
          icon={<GitPullRequestArrow className="h-7 w-7" />}
          title={filter === 'pending' ? 'Nothing is waiting for a decision' : `No ${filter} proposals`}
          body={
            filter === 'pending'
              ? 'Ask the planner for a change it cannot make directly, or re-evaluate a diagram from its own page.'
              : undefined
          }
        />
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {proposals.data.map((p) => (
            <Row key={p.id} projectId={projectId} proposal={p} nameFor={nameFor} />
          ))}
        </div>
      )}
    </div>
  )
}

function Row({
  projectId,
  proposal,
  nameFor,
}: {
  projectId: string
  proposal: Proposal
  nameFor: (userId: string) => string | undefined
}) {
  const Icon = TARGET_ICON[proposal.target_kind]
  const changes = list(proposal.changes)
  const counts = countByOp(changes)
  const conflicts = list(proposal.conflicts)

  return (
    <Link to={`/p/${projectId}/proposals/${proposal.id}`}>
      <Card className="px-4 py-3 transition-colors hover:border-accent-line hover:bg-accent-soft/40">
        <div className="flex items-start gap-2.5">
          <Icon className="mt-0.5 h-4 w-4 shrink-0 text-ink-faint" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="font-medium text-ink">{proposal.title}</span>
              <Badge tone={STATUS_TONE[proposal.status]}>{proposal.status}</Badge>
              {conflicts.length > 0 && proposal.status === 'pending' ? (
                <Badge tone="danger">
                  {conflicts.length} conflict{conflicts.length === 1 ? '' : 's'}
                </Badge>
              ) : null}
            </div>

            {/* The rationale is why this exists. It is the first thing a
                reviewer reads, so it is not behind a click. */}
            <p className="mt-0.5 line-clamp-2 text-ink-muted">{proposal.rationale}</p>

            <p className="mt-1 flex flex-wrap items-center gap-1.5 text-[11px] text-ink-faint">
              {proposal.turn_id ? (
                <Bot className="h-3 w-3" aria-label="raised by an agent" />
              ) : (
                <User className="h-3 w-3" aria-label="raised by a person" />
              )}
              <span>{attribution(proposal, nameFor)}</span>
              <span>· {formatRelative(proposal.created_at)}</span>
              <span>· {proposal.target_kind}</span>
              {/* The list endpoint sends an empty `changes` array — the shape
                  is there and the contents are not, by design, since a queue
                  does not need every field of every change. So the counts are
                  rendered only when there is something to count rather than as
                  a row of confident zeros. */}
              {changes.length > 0 ? (
                <span className="zp-num">
                  ·{counts.added > 0 ? ` +${counts.added}` : ''}
                  {counts.changed > 0 ? ` ~${counts.changed}` : ''}
                  {counts.removed > 0 ? ` −${counts.removed}` : ''}
                </span>
              ) : null}
            </p>
          </div>
        </div>
      </Card>
    </Link>
  )
}
