import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  FileText,
  GitMerge,
  ImageIcon,
  ListChecks,
  Upload,
} from 'lucide-react'
import { PageReview, PageStrip } from './PageReview'
import { CorrectionList } from './CorrectionList'
import { MergeReview } from './MergeReview'
import { AcceptPanel, AcceptedSummary } from './AcceptPanel'
import { useAcceptIngest, useIngest, useRetryPage } from './queries'
import {
  isWorking,
  unresolvedIllegible,
  type IngestView,
  type Resolution,
  type Resolutions,
} from './model'
import { Badge, Loading, Spinner, type Tone } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { notifyInfo, notifySuccess } from '@/components/notify'
import { useOwnership } from '@/features/plan/ownership'
import { cn } from '@/lib/cn'

type Step = 'pages' | 'corrections' | 'merge' | 'accept'

const STATUS_TONE: Record<IngestView['status'], Tone> = {
  uploaded: 'info',
  extracting: 'info',
  merging: 'info',
  review: 'warn',
  accepted: 'good',
  failed: 'danger',
}

/**
 * The review screen (F5).
 *
 * Four steps, in order, and the order is the argument:
 *
 *  1. **Pages** — the image beside what was read off it, one page at a time.
 *  2. **Corrections** — least confident first, with the page still on screen.
 *  3. **Merge review** — its own step, not a footnote. Unskippable.
 *  4. **Accept** — deliberate, and it shows what it will do.
 *
 * Nothing before step 4 writes anything. The review state lives here and dies
 * with the screen: it is a set of decisions about an upload, not a document,
 * and persisting it would mean two people could hold different half-finished
 * opinions about the same ingest with no way to reconcile them.
 */
export function IngestPage() {
  const { projectId = '', ingestId = '' } = useParams()
  const navigate = useNavigate()

  const ingest = useIngest(projectId, ingestId)
  const retry = useRetryPage(projectId, ingestId)
  const accept = useAcceptIngest(projectId, ingestId)
  // Accepting writes diagram content, and a diagram's semantic layer is
  // manager-owned in full (§4.2). Uploading is not: a photograph binds nobody
  // to anything, so any member may gather evidence and only the write is
  // gated.
  const ownership = useOwnership(projectId)
  const acceptPerm = ownership.field('diagram', '*')

  const [step, setStep] = useState<Step>('pages')
  const [page, setPage] = useState(0)
  const [resolutions, setResolutions] = useState<Resolutions>({})
  const [merged, setMerged] = useState(false)

  const mergedPayload = ingest.data?.merged

  // A retry re-opens an ingest somebody is already looking at, and the merge is
  // recomputed from scratch. Anything they decided about an element that is no
  // longer there would be sent as a resolution for a slug the backend does not
  // have, which it refuses by name — so stale decisions are pruned rather than
  // carried forward silently.
  useEffect(() => {
    const slugs = new Set((mergedPayload?.nodes ?? []).map((n) => n.slug))
    setResolutions((current) => {
      const kept = Object.entries(current).filter(([slug]) => slugs.has(slug))
      return kept.length === Object.keys(current).length ? current : Object.fromEntries(kept)
    })
  }, [mergedPayload])

  const blocking = useMemo(
    () => (mergedPayload ? unresolvedIllegible(mergedPayload, resolutions) : []),
    [mergedPayload, resolutions],
  )

  if (ingest.isPending) return <Loading what="this ingest" />
  if (ingest.error) throw ingest.error

  const data = ingest.data
  const counts = data.counts
  const reviewable = data.status === 'review'

  const setResolution = (slug: string, patch: Resolution) => {
    setResolutions((current) => ({ ...current, [slug]: { ...patch, slug } }))
    // A correction changes what the merge review was about, so the
    // acknowledgement is not carried across it.
    setMerged(false)
  }

  const header = (
    <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
      <Link
        to={`/p/${projectId}/ingest`}
        className="flex items-center gap-1 text-ink-faint hover:text-ink"
        aria-label="Back to ingests"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        <Upload className="h-3.5 w-3.5" />
      </Link>
      {data.source_kind === 'pdf' ? (
        <FileText className="h-3.5 w-3.5 text-ink-faint" />
      ) : (
        <ImageIcon className="h-3.5 w-3.5 text-ink-faint" />
      )}
      <h1 className="truncate font-semibold text-ink">
        {data.source_filename || 'untitled drawing'}
      </h1>
      <Badge tone={STATUS_TONE[data.status]}>{data.status}</Badge>
      <span className="zp-num text-[11px] text-ink-faint">
        {data.page_count} page{data.page_count === 1 ? '' : 's'}
        {counts ? ` · ${counts.nodes} elements · ${counts.edges} relations` : ''}
        {counts && counts.needs_review > 0 ? ` · ${counts.needs_review} flagged` : ''}
      </span>
      <div className="flex-1" />
      {isWorking(data.status) ? (
        <span className="flex items-center gap-1.5 text-ink-muted">
          <Spinner />
          {data.status === 'merging' ? 'Merging the pages…' : 'Reading the pages…'}
        </span>
      ) : null}
    </header>
  )

  if (data.status === 'accepted') {
    return (
      <div className="flex h-full flex-col">
        {header}
        <AcceptedSummary projectId={projectId} ingest={data} />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      {header}

      {data.status === 'failed' ? (
        <div className="flex items-start gap-2 border-b border-danger/30 bg-danger-soft px-4 py-2">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
          <div className="min-w-0">
            <p className="font-medium text-danger-ink">This ingest failed.</p>
            <p className="text-ink">{data.error_detail || data.error_class}</p>
            <p className="mt-0.5 text-[11px] text-ink-muted">
              Individual pages can still be re-read from the pages step; the upload itself does not
              need repeating.
            </p>
          </div>
        </div>
      ) : null}

      <Steps
        step={step}
        onStep={setStep}
        reviewable={reviewable}
        blocking={blocking.length}
        flagged={counts?.needs_review ?? 0}
        mergeItems={
          (counts?.unresolved ?? 0) + (counts?.conflicts ?? 0) + (data.merged?.pages_failed?.length ?? 0)
        }
        mergeAcknowledged={merged}
      />

      {step === 'pages' ? (
        <>
          <PageStrip ingest={data} selected={page} onSelect={setPage} />
          <PageReview
            ingest={data}
            idx={page}
            retrying={retry.isPending}
            onRetry={(idx) =>
              retry.mutate(idx, {
                onSuccess: () => {
                  setMerged(false)
                  notifyInfo(
                    `Page ${idx + 1} is being re-read. Every other page's extraction stays exactly as it is, and the merge re-runs when it lands.`,
                    'Re-reading one page',
                  )
                },
                // No onError here: the mutation cache reports every failed
                // write through the one error surface, and adding a second
                // handler shows the same denial twice.
              })
            }
          />
        </>
      ) : !reviewable ? (
        <WaitingForReview status={data.status} />
      ) : step === 'corrections' ? (
        <CorrectionList ingest={data} resolutions={resolutions} onChange={setResolution} />
      ) : step === 'merge' && data.merged ? (
        <MergeReview
          merged={data.merged}
          resolutions={resolutions}
          acknowledged={merged}
          onAcknowledge={setMerged}
        />
      ) : step === 'accept' ? (
        <AcceptPanel
          projectId={projectId}
          ingest={data}
          resolutions={resolutions}
          permission={acceptPerm}
          effectiveRole={ownership.effectiveRole}
          mergeAcknowledged={merged}
          onBackToMerge={() => setStep('merge')}
          accepting={accept.isPending}
          onAccept={(args) =>
            accept.mutate(args, {
              onSuccess: (result) => {
                const notes = result.notes ?? []
                notifySuccess(
                  `${result.nodes_added} element(s) and ${result.edges_added} relation(s) written as revision ${result.revision}.` +
                    (notes.length > 0
                      ? ` ${notes.length} note(s) recorded — arrows that found nothing, disagreements between pages, and low-confidence readings are kept as questions.`
                      : ''),
                  'Accepted',
                )
                navigate(`/p/${projectId}/diagrams/${result.diagram_id}`)
              },
            })
          }
        />
      ) : null}
    </div>
  )
}

function WaitingForReview({ status }: { status: IngestView['status'] }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-8 text-center">
      <Spinner className="h-5 w-5" />
      <p className="font-medium text-ink">
        {status === 'merging'
          ? 'Reconciling the pages against each other…'
          : 'Still reading the pages…'}
      </p>
      <p className="max-w-md text-ink-muted">
        Each page is one model call, and the merge is the separate step afterwards. There is nothing
        to correct until every page has settled — the pages step already shows the ones that have.
      </p>
    </div>
  )
}

/**
 * The step rail.
 *
 * Accept sits behind merge review, and it is not a suggestion: cross-page
 * arrows are where a multi-page extraction goes wrong, and that is exactly the
 * step people skip when it is offered as a tab beside the others.
 */
function Steps({
  step,
  onStep,
  reviewable,
  blocking,
  flagged,
  mergeItems,
  mergeAcknowledged,
}: {
  step: Step
  onStep: (s: Step) => void
  reviewable: boolean
  blocking: number
  flagged: number
  mergeItems: number
  mergeAcknowledged: boolean
}) {
  const items: {
    id: Step
    label: string
    Icon: typeof Check
    badge?: { text: string; tone: Tone }
    locked?: string
  }[] = [
    { id: 'pages', label: 'Pages', Icon: ImageIcon },
    {
      id: 'corrections',
      label: 'Corrections',
      Icon: ListChecks,
      ...(blocking > 0
        ? { badge: { text: `${blocking} unreadable`, tone: 'danger' as Tone } }
        : flagged > 0
          ? { badge: { text: `${flagged} flagged`, tone: 'warn' as Tone } }
          : {}),
      ...(reviewable ? {} : { locked: 'Every page has to settle before there is a merge to correct.' }),
    },
    {
      id: 'merge',
      label: 'Merge review',
      Icon: GitMerge,
      ...(mergeAcknowledged
        ? { badge: { text: 'checked', tone: 'good' as Tone } }
        : mergeItems > 0
          ? { badge: { text: String(mergeItems), tone: 'warn' as Tone } }
          : {}),
      ...(reviewable ? {} : { locked: 'The merge runs once every page has settled.' }),
    },
    {
      id: 'accept',
      label: 'Accept',
      Icon: Check,
      ...(reviewable
        ? mergeAcknowledged
          ? {}
          : { locked: 'Merge review has not been passed. It is where a multi-page extraction goes wrong, so it is not optional.' }
        : { locked: 'Nothing can be accepted until every page has settled.' }),
    },
  ]

  return (
    <nav className="flex shrink-0 items-center gap-1 border-b border-line bg-surface px-3 py-1.5">
      {items.map(({ id, label, Icon, badge, locked }, i) => (
        <div key={id} className="flex items-center gap-1">
          {i > 0 ? <span className="text-ink-faint">›</span> : null}
          <button
            type="button"
            disabled={Boolean(locked)}
            title={locked}
            onClick={() => onStep(id)}
            className={cn(
              'flex items-center gap-1.5 rounded-md px-2.5 py-1 transition-colors',
              step === id
                ? 'bg-accent-soft font-medium text-accent-ink'
                : locked
                  ? 'cursor-not-allowed text-ink-faint'
                  : 'text-ink-muted hover:bg-sunken hover:text-ink',
            )}
          >
            <span className="zp-num text-[11px] text-ink-faint">{i + 1}</span>
            <Icon className="h-3.5 w-3.5" />
            {label}
            {badge ? <Badge tone={badge.tone}>{badge.text}</Badge> : null}
          </button>
        </div>
      ))}

      <div className="flex-1" />

      {step !== 'accept' ? (
        <Button
          size="sm"
          variant="secondary"
          disabled={!reviewable || (step === 'merge' && !mergeAcknowledged)}
          onClick={() =>
            onStep(step === 'pages' ? 'corrections' : step === 'corrections' ? 'merge' : 'accept')
          }
          title={
            step === 'merge' && !mergeAcknowledged
              ? 'Tick the acknowledgement below first. Accept is not reachable around this step.'
              : undefined
          }
        >
          Next
        </Button>
      ) : null}
    </nav>
  )
}
