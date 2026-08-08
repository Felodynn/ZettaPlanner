import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import {
  AlertTriangle,
  ArrowLeft,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  GitPullRequestArrow,
  Lock,
  Network,
  User,
  X,
} from 'lucide-react'
import { GraphCanvas } from '@/features/diagram/GraphCanvas'
import { Legend } from '@/features/diagram/Legend'
import { diagramKeys } from '@/features/diagram/queries'
import type { DiagramView, SemanticGraph } from '@/features/diagram/model'
import { PlanDiff } from './PlanDiff'
import { RejectDialog } from './RejectDialog'
import { useApproveProposal, useProposal, useRejectProposal } from './queries'
import { useActorNames } from './useActorNames'
import {
  attribution,
  changeReason,
  changeSubject,
  diagramChanges,
  diagramDiff,
  humaniseTarget,
  planChanges,
  type ProposalChange,
  type ProposalConflict,
} from './model'
import { client, list, unwrap } from '@/api/client'
import { ApiError } from '@/api/errors'
import { Badge, Card, CardHeader, CardTitle, Loading, Separator, type Tone } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { notifySuccess } from '@/components/notify'
import { formatTimestamp } from '@/lib/format'
import type { ProposalStatus } from '@/api/vocabulary'

const STATUS_TONE: Record<ProposalStatus, Tone> = {
  pending: 'warn',
  approved: 'good',
  rejected: 'neutral',
  superseded: 'danger',
}

/**
 * Reviewing one proposal.
 *
 * The two halves are deliberately different shapes: plan changes are read
 * field by field, and diagram changes are read **on the canvas** — the same
 * renderer as the viewer and the editor, in `diff` mode (§1.1 rule 1). A list
 * of JSON node operations is a thing people approve without understanding.
 */
export function ProposalDetail() {
  const { projectId = '', proposalId = '' } = useParams()
  const proposal = useProposal(projectId, proposalId)
  const approve = useApproveProposal(projectId)
  const reject = useRejectProposal(projectId)
  const [rejecting, setRejecting] = useState(false)
  const [legendOpen, setLegendOpen] = useState(false)
  const nameFor = useActorNames(projectId)

  const changes = list(proposal.data?.changes)
  const byDiagram = useMemo(() => diagramChanges(changes), [changes])
  const diagramIds = [...byDiagram.keys()]

  // The diagrams a diagram proposal touches, fetched so the diff can be drawn
  // against what is on them *now* rather than against the snapshot the proposal
  // was raised from.
  const diagrams = useQueries({
    queries: diagramIds.map((id) => ({
      queryKey: diagramKeys.detail(id),
      queryFn: () => unwrap(client.GET('/api/v1/diagrams/{id}', { params: { path: { id } } })),
      staleTime: 0,
    })),
  })

  if (proposal.isPending) return <Loading what="this proposal" />
  if (proposal.error) throw proposal.error

  const p = proposal.data
  const conflicts = list(p.conflicts)
  const plan = planChanges(changes)
  const pending = p.status === 'pending'
  const superseded = p.status === 'superseded'

  // Every approve failure, not only the conflict.
  //
  // Rendering `conflict` alone meant a proposal the applier simply cannot take
  // — an agent naming a milestone that does not exist yet, or a priority
  // outside the closed set — failed with a 400 that reached nothing on screen.
  const approveFailed = approve.error instanceof ApiError ? approve.error : undefined
  // A 409 is terminal for this proposal (frontend-notes §8): the status becomes
  // superseded and nothing was applied, so the next action is "raise it again
  // against the current state", never "try again". Anything else is the
  // proposal being unapplicable as written, which rejecting is the answer to.
  const superseding = approveFailed?.code === 'conflict'

  const canApprove = p.can_approve === true && conflicts.length === 0 && pending

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <Link
          to={`/p/${projectId}/proposals`}
          className="flex items-center gap-1 text-ink-faint hover:text-ink"
          aria-label="Back to proposals"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          <GitPullRequestArrow className="h-3.5 w-3.5" />
        </Link>
        <h1 className="truncate font-semibold text-ink">{p.title}</h1>
        <Badge tone={STATUS_TONE[p.status]}>{p.status}</Badge>
        <Badge tone="outline">{p.target_kind}</Badge>
        <div className="flex-1" />
        {diagramIds.length > 0 ? (
          <Button size="sm" variant="ghost" onClick={() => setLegendOpen((v) => !v)}>
            Legend
          </Button>
        ) : null}
      </header>

      <div className="min-h-0 flex-1 overflow-auto zp-scroll">
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-3 px-6 py-5">
          {/* ── why ── */}
          <Card>
            <CardHeader>
              <CardTitle>Why this was raised</CardTitle>
              <span className="flex items-center gap-1.5 text-[11px] text-ink-faint">
                {p.turn_id ? <Bot className="h-3 w-3" /> : <User className="h-3 w-3" />}
                {attribution(p, nameFor)} · {formatTimestamp(p.created_at)}
              </span>
            </CardHeader>
            <p className="px-4 py-3 wrap-break-word text-ink">{p.rationale}</p>
            {p.on_behalf_of ? (
              <p className="border-t border-line px-4 py-2 text-[11px] text-ink-faint">
                {/* §6.5: an agent write is attributed to both the agent and the
                    instructing user. "Who decided this" has two answers. */}
                Raised by an agent acting with {nameFor(p.on_behalf_of) ?? p.on_behalf_of}'s permissions,
                and recorded against both.
              </p>
            ) : null}
          </Card>

          {/* ── what moved underneath ── */}
          {conflicts.length > 0 ? (
            <ConflictPanel conflicts={conflicts} superseded={superseded} />
          ) : null}

          {approveFailed ? (
            <Card className="border-danger/40 bg-danger-soft">
              <div className="flex items-start gap-2 px-4 py-3">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
                <div className="min-w-0">
                  <p className="font-semibold text-danger-ink">
                    {superseding
                      ? 'Something moved while you were reading'
                      : 'This proposal cannot be applied as it stands'}
                  </p>
                  {/* The backend's sentence: it names the change, the field and
                      what is wrong with it. Rendered verbatim (§7.3). */}
                  <p className="wrap-break-word text-ink">{approveFailed.message}</p>
                  <p className="mt-1 text-[11px] text-ink-muted">
                    {superseding
                      ? 'Nothing was applied, and this proposal is finished — its status is now superseded. Retrying cannot help: the next step is to ask for the change again against the state that exists now.'
                      : 'Nothing was applied — a proposal lands whole or not at all. Rejecting it with the reason above is the way forward: the rationale goes back to the planner, which is what stops it proposing the same unapplicable change again.'}
                  </p>
                  <p className="mt-0.5 font-mono text-[11px] text-ink-faint">
                    {approveFailed.requestId}
                  </p>
                  {!superseding ? (
                    <Button
                      size="sm"
                      variant="secondary"
                      className="mt-2"
                      onClick={() => setRejecting(true)}
                    >
                      Reject, and say why
                    </Button>
                  ) : null}
                </div>
              </div>
            </Card>
          ) : null}

          {/* ── the plan half ── */}
          {plan.length > 0 ? (
            <section>
              <h2 className="mb-1.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                Plan changes
              </h2>
              <PlanDiff changes={plan} conflicts={conflicts} />
            </section>
          ) : null}

          {/* ── the diagram half, on the canvas ── */}
          {diagramIds.map((id, i) => {
            const view = diagrams[i]?.data as DiagramView | undefined
            const forThis = byDiagram.get(id) ?? []
            return (
              <DiagramDiffCard
                key={id}
                projectId={projectId}
                diagramId={id}
                view={view}
                changes={forThis}
                loading={diagrams[i]?.isPending ?? true}
              />
            )
          })}

          {plan.length === 0 && diagramIds.length === 0 ? (
            <Card>
              <p className="px-4 py-6 text-center text-ink-muted">
                This proposal carries no changes this client can render. That is worth reporting —
                the change vocabulary is closed, and a shape not on it is a contract gap rather than
                a display problem.
              </p>
            </Card>
          ) : null}

          {/* ── the decision ── */}
          {pending ? (
            <Card>
              <CardHeader>
                <CardTitle>Decide</CardTitle>
                <span className="text-[11px] text-ink-faint">
                  Applied whole, as one revision — never partly
                </span>
              </CardHeader>
              <div className="flex flex-col gap-2 px-4 py-3">
                {!canApprove ? (
                  <div className="flex items-start gap-2 rounded-md border border-warn/30 bg-warn-soft px-3 py-2">
                    <Lock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" />
                    <p className="text-warn-ink">
                      {/* `approval_blocked_because` is a sentence written to be
                          shown as-is (frontend-notes §8). */}
                      {conflicts.length > 0
                        ? 'Something this proposal writes has moved since it was raised. Approving it would apply a decision made against a state that no longer exists, so it cannot be approved — reject it and ask again.'
                        : (p.approval_blocked_because ??
                          'You cannot approve this proposal. Approving is limited to admins and managers, and to developers whose own dev-owned fields are all it touches (§5.1).')}
                    </p>
                  </div>
                ) : null}

                <div className="flex items-center justify-end gap-2">
                  {/* Rejecting is open to every project member: refusing changes
                      nothing, and requiring a manager to be present to record a
                      reason is how the reason stops being recorded (§5.1). */}
                  <Button variant="secondary" onClick={() => setRejecting(true)}>
                    <X className="h-3.5 w-3.5" />
                    Reject
                  </Button>
                  <Button
                    variant="primary"
                    loading={approve.isPending}
                    disabled={!canApprove || approve.isPending}
                    onClick={() => {
                      approve.mutate(proposalId, {
                        onSuccess: () =>
                          notifySuccess(
                            'Applied as one revision. The audit trail records who raised it and who approved it.',
                            'Approved',
                          ),
                      })
                    }}
                  >
                    <Check className="h-3.5 w-3.5" />
                    Approve
                  </Button>
                </div>
              </div>
            </Card>
          ) : (
            <Decided
              status={p.status}
              by={p.decided_by}
              rationale={p.decision_rationale}
            />
          )}
        </div>
      </div>

      {legendOpen ? <Legend onClose={() => setLegendOpen(false)} /> : null}

      {rejecting ? (
        <RejectDialog
          title={p.title}
          rejecting={reject.isPending}
          onClose={() => setRejecting(false)}
          onReject={(rationale) =>
            reject.mutate(
              { id: proposalId, rationale },
              {
                onSuccess: () => {
                  setRejecting(false)
                  notifySuccess(
                    'Refused, with your reason attached. The planner reads it as context on every later turn.',
                    'Rejected',
                  )
                },
              },
            )
          }
        />
      ) : null}
    </div>
  )
}

function ConflictPanel({
  conflicts,
  superseded,
}: {
  conflicts: ProposalConflict[]
  superseded: boolean
}) {
  return (
    <Card className="border-danger/40">
      <CardHeader className="bg-danger-soft">
        <CardTitle className="flex items-center gap-1.5 text-danger-ink">
          <AlertTriangle className="h-3.5 w-3.5" />
          {superseded
            ? 'This was superseded'
            : conflicts.length === 1
              ? 'Something moved underneath this'
              : `${conflicts.length} things moved underneath this`}
        </CardTitle>
      </CardHeader>
      <div className="flex flex-col">
        <p className="border-b border-line px-4 py-2 text-ink-muted">
          A change's premise is every field it would write, plus its target still existing. These no
          longer hold. Nothing was applied and nothing was merged — a proposal decided against a
          state that has moved is not a decision anybody made.
        </p>
        {conflicts.map((c, i) => (
          <div key={i} className="border-b border-line px-4 py-2 last:border-b-0">
            <p className="flex flex-wrap items-center gap-1.5">
              <span className="font-medium text-ink">{c.element}</span>
              <span className="text-[11px] text-ink-faint">
                {c.target} · change #{c.seq}
              </span>
            </p>
            <p className="text-ink">
              {c.reason ?? (
                <>
                  <span className="font-mono text-[11px]">{c.field}</span> was{' '}
                  <span className="font-mono text-[11px] line-through">{c.expected || 'unset'}</span>{' '}
                  when this was raised, and is{' '}
                  <span className="font-mono text-[11px]">{c.actual || 'unset'}</span> now.
                </>
              )}
            </p>
          </div>
        ))}
      </div>
    </Card>
  )
}

function DiagramDiffCard({
  projectId,
  diagramId,
  view,
  changes,
  loading,
}: {
  projectId: string
  diagramId: string
  view: DiagramView | undefined
  changes: ProposalChange[]
  loading: boolean
}) {
  const current: SemanticGraph = useMemo(
    () => ({
      nodes: list(view?.nodes),
      edges: list(view?.edges),
      notes: list(view?.notes),
    }),
    [view],
  )
  const { graph, diff } = useMemo(() => diagramDiff(current, changes), [current, changes])

  const counts = {
    added: Object.values(diff.nodes).filter((m) => m === 'added').length,
    changed: Object.values(diff.nodes).filter((m) => m === 'changed').length,
    removed: Object.values(diff.nodes).filter((m) => m === 'removed').length,
    edges: Object.keys(diff.edges).length,
  }

  return (
    <section>
      <h2 className="mb-1.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
        Diagram changes
      </h2>
      <Card className="flex h-[30rem] flex-col overflow-hidden">
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            <Network className="h-3.5 w-3.5 text-ink-faint" />
            {view ? (
              <Link
                to={`/p/${projectId}/diagrams/${diagramId}`}
                className="hover:text-accent-ink hover:underline"
              >
                {view.name}
              </Link>
            ) : (
              'This diagram'
            )}
          </CardTitle>
          <span className="zp-num flex items-center gap-2 text-[11px]">
            <span className="text-good">+{counts.added}</span>
            <span className="text-warn">~{counts.changed}</span>
            <span className="text-danger">−{counts.removed}</span>
            <span className="text-ink-faint">· {counts.edges} relations touched</span>
          </span>
        </CardHeader>

        {loading ? (
          <Loading what="the diagram" />
        ) : (
          <div className="min-h-0 flex-1">
            <GraphCanvas graph={graph} layoutMode="auto" mode="diff" diff={diff} minimap={false} />
          </div>
        )}

        <p className="border-t border-line px-3 py-1.5 text-[11px] leading-snug text-ink-faint">
          <span className="font-bold text-good">+</span> added,{' '}
          <span className="font-bold text-warn">~</span> changed,{' '}
          <span className="font-bold text-danger">−</span> removed. Removed elements are still
          drawn — you cannot judge a deletion you cannot see. Positions are computed, not proposed:
          layout is a separate write and no coordinate is ever in a proposal.
        </p>
      </Card>

      <ChangeReasons changes={changes} />
    </section>
  )
}

/**
 * Every change, with the reason its proposer gave for it.
 *
 * The canvas answers "what would this do to the picture". This answers "why",
 * one change at a time — and the two together are what makes a large
 * destructive proposal reviewable instead of intimidating.
 */
function ChangeReasons({ changes }: { changes: ProposalChange[] }) {
  const [open, setOpen] = useState(false)
  const withReason = changes.filter((c) => changeReason(c) !== undefined)
  if (withReason.length === 0) return null

  return (
    <div className="mt-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-ink-muted hover:text-ink"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        <span className="font-medium">
          Why each of the {withReason.length} changes
        </span>
      </button>
      {open ? (
        <ul className="mt-1.5 flex flex-col gap-1">
          {withReason.map((c) => (
            <li key={c.seq} className="rounded-md border border-line px-2.5 py-1.5">
              <p className="flex flex-wrap items-center gap-1.5">
                <Badge
                  tone={c.op === 'create' ? 'good' : c.op === 'delete' ? 'danger' : 'warn'}
                >
                  {c.op === 'create' ? 'add' : c.op === 'delete' ? 'remove' : 'change'}
                </Badge>
                <span className="font-mono text-[11px] text-ink">{changeSubject(c)}</span>
                <span className="text-[11px] text-ink-faint">{humaniseTarget(c.target)}</span>
              </p>
              <p className="mt-0.5 text-ink-muted">{changeReason(c)}</p>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

function Decided({
  status,
  by,
  rationale,
}: {
  status: ProposalStatus
  by: string | undefined
  rationale: string | undefined
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          {status === 'approved'
            ? 'Approved and applied'
            : status === 'rejected'
              ? 'Refused'
              : 'Superseded'}
        </CardTitle>
        {by ? <span className="text-[11px] text-ink-faint">by {by}</span> : null}
      </CardHeader>
      <div className="px-4 py-3">
        {rationale ? (
          <>
            <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
              The reason given
            </p>
            <p className="mt-0.5 wrap-break-word text-ink">{rationale}</p>
            <Separator className="my-2" />
            <p className="text-[11px] text-ink-muted">
              This is read by the planner as context on every later turn, which is what stops the
              same thing being proposed again.
            </p>
          </>
        ) : status === 'superseded' ? (
          <p className="text-ink-muted">
            Something this proposal would have written moved before it was decided. Nothing was
            applied. Asking again against the current state is the way forward.
          </p>
        ) : (
          <p className="text-ink-muted">Applied as one revision.</p>
        )}
      </div>
    </Card>
  )
}
