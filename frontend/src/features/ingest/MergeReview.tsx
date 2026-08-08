import { AlertTriangle, ArrowRightLeft, CheckCircle2, GitMerge, Scale, Unlink } from 'lucide-react'
import { Badge, Card, CardHeader, CardTitle } from '@/components/ui/primitives'
import { cn } from '@/lib/cn'
import {
  crossPageEdges,
  isDropped,
  pageWord,
  percent,
  unresolvedEdges,
  type Merged,
  type Resolutions,
} from './model'

/**
 * Merge review — its own step, deliberately.
 *
 * §3.4a: the merge is a **separate, deterministic step**, not a detail of
 * extraction. The "continues on the next page" arrow is exactly where
 * extraction goes wrong, and every judgement the merge declined to make is
 * recorded here rather than guessed.
 *
 * F5 is explicit that this must not be a footnote on the last page. Cross-page
 * arrows are where extraction fails, and that is precisely the step people skip
 * if it is not made explicit — so accept is reachable only through this screen,
 * and only after somebody says they have looked.
 *
 * Three things are shown, all of them things the merge refused to decide:
 *
 *  - **Arrows that found nothing.** Kept, not drawn and not dropped. Accept
 *    records each as a Note, because a dropped dependency is the error nobody
 *    notices.
 *  - **Arrows joined across pages.** The merge's actual work, flagged rather
 *    than smoothed over.
 *  - **Disagreements between pages.** Recorded, never resolved: only somebody
 *    who knows the system can say.
 */
export function MergeReview({
  merged,
  resolutions,
  acknowledged,
  onAcknowledge,
}: {
  merged: Merged
  resolutions: Resolutions
  acknowledged: boolean
  onAcknowledge: (v: boolean) => void
}) {
  const unresolved = unresolvedEdges(merged)
  const crossPage = crossPageEdges(merged)
  const conflicts = merged.conflicts ?? []
  const failed = merged.pages_failed ?? []
  const nothingToSettle =
    unresolved.length === 0 && crossPage.length === 0 && conflicts.length === 0 && failed.length === 0

  return (
    <div className="min-h-0 flex-1 overflow-auto zp-scroll">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-6 py-5">
        <div className="flex items-start gap-2">
          <GitMerge className="mt-0.5 h-4 w-4 shrink-0 text-ink-faint" />
          <div>
            <h2 className="font-semibold text-ink">
              The merge, across {pageWord(merged.pages_read)}
            </h2>
            <p className="text-ink-muted">
              Each page was read on its own, then reconciled by deterministic code — never a second
              model call. A model asked to decide whether the box on page 3 is the box on page 1
              returns a confident answer over data that is already uncertain, and the second failure
              compounds the first with nothing left to inspect. So everything it refused to decide
              is here.
            </p>
          </div>
        </div>

        {failed.length > 0 ? (
          <Card className="border-danger/30 bg-danger-soft">
            <div className="flex items-start gap-2 px-4 py-3">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
              <div>
                <p className="font-semibold text-danger-ink">
                  {pageWord(failed)} could not be read at all
                </p>
                <p className="text-ink">
                  Whatever was drawn there is not in this merge. Go back to the pages step and re-read
                  it, or accept knowing it is missing — accepting records a note saying so.
                </p>
              </div>
            </div>
          </Card>
        ) : null}

        {/* ── arrows that found nothing ── */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              <Unlink className="h-3.5 w-3.5 text-ink-faint" />
              Arrows that found nothing
            </CardTitle>
            <Badge tone={unresolved.length > 0 ? 'warn' : 'neutral'}>{unresolved.length}</Badge>
          </CardHeader>
          {unresolved.length === 0 ? (
            <p className="px-4 py-3 text-ink-muted">
              Every arrow the model read has both ends on a box that was read. Nothing was left
              hanging.
            </p>
          ) : (
            <div className="flex flex-col">
              <p className="border-b border-line px-4 py-2 text-ink-muted">
                One end of each of these names something no page was read as a box. Nothing was
                invented to receive them and nothing was discarded — accepting records each as a
                note against the end that does exist.
              </p>
              {unresolved.map((e, i) => (
                <div key={i} className="border-b border-line px-4 py-2 last:border-b-0">
                  <p className="text-ink">
                    <Endpoint slug={e.from} resolutions={resolutions} />
                    <span className="mx-1.5 text-ink-faint">{e.relation}</span>
                    <Endpoint slug={e.to} resolutions={resolutions} />
                    {e.label ? <span className="text-ink-faint"> · “{e.label}”</span> : null}
                  </p>
                  <p className="text-[11px] text-ink-muted">
                    {e.review_reason} · read on {pageWord(e.pages)} · {percent(e.confidence)}{' '}
                    confident
                  </p>
                </div>
              ))}
            </div>
          )}
        </Card>

        {/* ── joined across pages ── */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              <ArrowRightLeft className="h-3.5 w-3.5 text-ink-faint" />
              Arrows joined across pages
            </CardTitle>
            <Badge tone={crossPage.length > 0 ? 'info' : 'neutral'}>{crossPage.length}</Badge>
          </CardHeader>
          {crossPage.length === 0 ? (
            <p className="px-4 py-3 text-ink-muted">
              No arrow crossed a page. Every join the merge made was within one drawing.
            </p>
          ) : (
            <div className="flex flex-col">
              <p className="border-b border-line px-4 py-2 text-ink-muted">
                These are the joins the merge actually made: the arrow was drawn on one page and its
                far end was read on another. This is where extraction is weakest — check each one
                against both drawings.
              </p>
              {crossPage.map((e, i) => (
                <div key={i} className="border-b border-line px-4 py-2 last:border-b-0">
                  <p className="text-ink">
                    <Endpoint slug={e.from} resolutions={resolutions} />
                    <span className="mx-1.5 text-ink-faint">{e.relation}</span>
                    <Endpoint slug={e.to} resolutions={resolutions} />
                  </p>
                  <p className="text-[11px] text-ink-muted">
                    {e.review_reason} · {pageWord(e.pages)} · {percent(e.confidence)} confident
                  </p>
                </div>
              ))}
            </div>
          )}
        </Card>

        {/* ── what the pages disagreed about ── */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              <Scale className="h-3.5 w-3.5 text-ink-faint" />
              What the pages disagreed about
            </CardTitle>
            <Badge tone={conflicts.length > 0 ? 'warn' : 'neutral'}>{conflicts.length}</Badge>
          </CardHeader>
          {conflicts.length === 0 ? (
            <p className="px-4 py-3 text-ink-muted">
              Nothing was read two different ways. Where pages overlapped, they agreed.
            </p>
          ) : (
            <div className="flex flex-col">
              {conflicts.map((c, i) => (
                <div key={i} className="border-b border-line px-4 py-2 last:border-b-0">
                  <p className="flex items-center gap-1.5 font-medium text-ink">
                    {c.name}
                    <Badge tone="outline">{c.kind.replace(/_/g, ' ')}</Badge>
                  </p>
                  {/* The backend's sentence, written to be read by a human and
                      by a model. Not paraphrased. */}
                  <p className="text-ink">{c.detail}</p>
                  <p className="text-[11px] text-ink-faint">{pageWord(c.pages)}</p>
                </div>
              ))}
            </div>
          )}
        </Card>

        {(merged.notes ?? []).length > 0 ? (
          <Card>
            <CardHeader>
              <CardTitle>What the model saw and did not structure</CardTitle>
            </CardHeader>
            <ul className="flex list-disc flex-col gap-1 px-8 py-3 text-ink">
              {(merged.notes ?? []).map((n, i) => (
                <li key={i}>{n}</li>
              ))}
            </ul>
          </Card>
        ) : null}

        {/* The gate. Not a formality — this is the step that gets skipped. */}
        <label
          className={cn(
            'flex cursor-pointer items-start gap-2.5 rounded-card border px-4 py-3 transition-colors',
            acknowledged
              ? 'border-good/30 bg-good-soft'
              : 'border-line-strong bg-surface hover:bg-sunken',
          )}
        >
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={(e) => onAcknowledge(e.target.checked)}
            className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--zp-accent)]"
          />
          <span className="min-w-0">
            <span className="flex items-center gap-1.5 font-medium text-ink">
              {acknowledged ? <CheckCircle2 className="h-3.5 w-3.5 text-good" /> : null}
              {nothingToSettle
                ? 'I have read the merge. There was nothing for it to settle.'
                : `I have checked ${describe(unresolved.length, crossPage.length, conflicts.length)}.`}
            </span>
            <span className="mt-0.5 block text-ink-muted">
              Accepting is not reachable until this is ticked. Cross-page arrows are where a
              multi-page extraction goes wrong, and a step nobody is made to stop at is a step
              nobody stops at.
            </span>
          </span>
        </label>
      </div>
    </div>
  )
}

function describe(unresolved: number, crossPage: number, conflicts: number): string {
  const parts: string[] = []
  if (unresolved > 0) parts.push(`${unresolved} unplaceable arrow${unresolved === 1 ? '' : 's'}`)
  if (crossPage > 0) parts.push(`${crossPage} cross-page join${crossPage === 1 ? '' : 's'}`)
  if (conflicts > 0) parts.push(`${conflicts} disagreement${conflicts === 1 ? '' : 's'}`)
  if (parts.length === 0) return 'the merge'
  if (parts.length === 1) return parts[0] as string
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

/**
 * One end of an edge.
 *
 * An unresolved end holds the text the model read, verbatim — it is not an
 * identity, because there is nothing for it to identify. Rendering it as a slug
 * would suggest a box exists that does not.
 */
function Endpoint({ slug, resolutions }: { slug: string; resolutions: Resolutions }) {
  const dropped = isDropped(resolutions, slug)
  return (
    <span
      className={cn('font-mono text-[11px]', dropped ? 'text-ink-faint line-through' : 'text-ink')}
      title={dropped ? 'You dropped this element, so this arrow will not be recorded either.' : ''}
    >
      {slug.trim() || '(nothing legible)'}
    </span>
  )
}
