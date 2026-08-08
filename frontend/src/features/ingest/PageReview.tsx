import { useState } from 'react'
import { AlertTriangle, ChevronDown, ChevronRight, RotateCw, ScanLine } from 'lucide-react'
import { GraphCanvas } from '@/features/diagram/GraphCanvas'
import { Badge, EmptyState, Spinner } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { formatDuration, formatTokens } from '@/lib/format'
import { cn } from '@/lib/cn'
import { pageImageUrl } from './queries'
import { offPageEdges, pageGraph, type IngestView } from './model'

/**
 * One page: the image the model was shown, beside what it read off it.
 *
 * **Always side by side.** The reviewer's job here is comparison, and a screen
 * that makes them switch views to do it is a screen that gets approved without
 * being checked. The image is the *normalized* page — rotation applied,
 * downscaled — because reviewing against the original photograph means
 * comparing to something the model never saw.
 *
 * This shows the page's own extraction, before the merge reconciled anything.
 * Showing the merged graph beside one photograph would attribute another
 * page's boxes to this one, which is exactly the confusion the per-page call
 * exists to prevent.
 */
export function PageReview({
  ingest,
  idx,
  onRetry,
  retrying,
}: {
  ingest: IngestView
  idx: number
  onRetry: (idx: number) => void
  retrying: boolean
}) {
  const [rawOpen, setRawOpen] = useState(false)
  const page = (ingest.pages ?? []).find((p) => p.idx === idx)
  // Accepted is the only state the backend refuses a retry from.
  const canRetry = ingest.status !== 'accepted'

  if (!page) {
    return <EmptyState className="pt-16" title={`This ingest has no page ${idx + 1}.`} />
  }

  const extraction = page.extracted
  const graph = extraction ? pageGraph(extraction) : null
  const offPage = extraction ? offPageEdges(extraction) : []

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1">
        {/* ── the page the model was shown ── */}
        <div className="flex min-w-0 flex-1 flex-col border-r border-line bg-sunken">
          <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
            <ScanLine className="h-3.5 w-3.5 text-ink-faint" />
            <span className="font-semibold text-ink">Page {idx + 1}, as the model saw it</span>
            <div className="flex-1" />
            <a
              href={pageImageUrl(ingest.id, idx)}
              target="_blank"
              rel="noreferrer"
              className="text-[11px] text-ink-faint underline-offset-2 hover:text-ink hover:underline"
            >
              Open full size
            </a>
          </div>
          <div className="min-h-0 flex-1 overflow-auto p-3 zp-scroll">
            <img
              src={pageImageUrl(ingest.id, idx)}
              alt={`Page ${idx + 1} of ${ingest.source_filename || 'the drawing'}`}
              className="mx-auto max-w-full rounded border border-line bg-white"
            />
          </div>
        </div>

        {/* ── what it read off it ── */}
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
            <span className="font-semibold text-ink">What it read</span>
            <PageStatusBadge status={page.status} />
            {extraction?.title ? (
              <span className="truncate text-[11px] text-ink-faint">“{extraction.title}”</span>
            ) : null}
            <div className="flex-1" />
            {/* Offered on every page, not only the failed ones. A page that
                was read *badly* is the common case — the model returned
                something, and it is wrong — and retry is per page precisely so
                that fixing it costs one model call and disturbs nothing else.
                Hiding the button until a page fails outright is hiding it from
                the case people actually hit. */}
            {canRetry && page.status !== 'failed' ? (
              <Button
                size="sm"
                variant="secondary"
                loading={retrying}
                onClick={() => onRetry(idx)}
                title="One model call. Every other page's extraction is untouched, and the merge re-runs afterwards."
              >
                {retrying ? null : <RotateCw className="h-3.5 w-3.5" />}
                Re-read this page
              </Button>
            ) : null}
          </div>

          {page.status === 'failed' ? (
            <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-8 text-center">
              <AlertTriangle className="h-6 w-6 text-danger" />
              <p className="font-medium text-ink">This page could not be read.</p>
              <p className="text-ink-muted">{page.error_detail || page.error_class}</p>
              <p className="mt-1 text-[11px] text-ink-faint">
                {/* §3.4a: failure is per page, and so is retry. */}
                Four readable pages and one smudged one is a reviewable result, not a dead upload.
                Re-reading this page costs one model call and leaves every other page's extraction —
                and anything you have already corrected — exactly as it is.
              </p>
              <Button
                className="mt-2"
                variant="primary"
                loading={retrying}
                disabled={!canRetry}
                onClick={() => onRetry(idx)}
              >
                {retrying ? null : <RotateCw className="h-3.5 w-3.5" />}
                Re-read page {idx + 1}
              </Button>
            </div>
          ) : page.status !== 'extracted' ? (
            <div className="flex min-h-0 flex-1 items-center justify-center gap-2 text-ink-faint">
              <Spinner />
              Reading this page…
            </div>
          ) : graph && graph.nodes.length > 0 ? (
            <div className="min-h-0 flex-1">
              <GraphCanvas graph={graph} layoutMode="auto" mode="view" minimap={false} />
            </div>
          ) : (
            <EmptyState
              className="pt-16"
              title="Nothing was read off this page."
              body="The model returned no boxes. If there is a drawing on it, re-read the page; if the page is a cover sheet or a page of prose, this is the right answer."
            />
          )}

          {/* Arrows this page could not close on its own. The merge step is
              where they are resolved; naming them here is what makes that step
              feel earned rather than bureaucratic. */}
          {offPage.length > 0 ? (
            <div className="shrink-0 border-t border-line bg-warn-soft px-3 py-2">
              <p className="font-medium text-warn-ink">
                {offPage.length} arrow{offPage.length === 1 ? '' : 's'} with an end that is not on
                this page
              </p>
              <ul className="mt-1 flex flex-col gap-0.5">
                {offPage.map((e, i) => (
                  <li key={i} className="text-ink">
                    <span className="font-mono text-[11px]">{e.from || '(nothing legible)'}</span>{' '}
                    <span className="text-ink-faint">{e.relation}</span>{' '}
                    <span className="font-mono text-[11px]">{e.to || '(nothing legible)'}</span>
                    {e.continues_off_page ? (
                      <span className="text-ink-faint"> — the model marked it as leaving the page</span>
                    ) : null}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          {(extraction?.notes ?? []).length > 0 ? (
            <div className="shrink-0 border-t border-line px-3 py-2">
              <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                What the model saw and did not structure
              </p>
              <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-4 text-ink">
                {(extraction?.notes ?? []).map((n, i) => (
                  <li key={i}>{n}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      </div>

      {/* ── the evidence ── */}
      <div className="shrink-0 border-t border-line bg-surface">
        <button
          type="button"
          onClick={() => setRawOpen((v) => !v)}
          className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-ink-muted hover:bg-sunken hover:text-ink"
        >
          {rawOpen ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
          <span>The model's answer, byte for byte</span>
          <span className="zp-num text-[11px] text-ink-faint">
            {page.model ?? 'unknown model'}
            {page.duration_ms ? ` · ${formatDuration(page.duration_ms)}` : ''}
            {page.tokens_in ? ` · ${formatTokens(page.tokens_in)} in` : ''}
            {page.tokens_out ? ` · ${formatTokens(page.tokens_out)} out` : ''}
            {page.attempts > 1 ? ` · ${page.attempts} attempts` : ''}
          </span>
        </button>
        {rawOpen ? (
          <pre className="max-h-64 overflow-auto border-t border-line bg-sunken px-3 py-2 font-mono text-[11px] leading-relaxed text-ink zp-scroll">
            {page.raw ||
              'Nothing was recorded — this page has not been read yet, so there is no answer to show.'}
          </pre>
        ) : null}
      </div>
    </div>
  )
}

function PageStatusBadge({ status }: { status: string }) {
  const tone =
    status === 'extracted'
      ? 'good'
      : status === 'failed'
        ? 'danger'
        : status === 'extracting'
          ? 'info'
          : 'neutral'
  return <Badge tone={tone}>{status}</Badge>
}

/**
 * The page strip.
 *
 * Every page keeps its slot, including the ones that failed: "page 3 of 5 is
 * unreadable" is the useful thing to say, and renumbering around a failure
 * makes the drawing's own page numbers stop matching the screen.
 */
export function PageStrip({
  ingest,
  selected,
  onSelect,
}: {
  ingest: IngestView
  selected: number
  onSelect: (idx: number) => void
}) {
  const pages = [...(ingest.pages ?? [])].sort((a, b) => a.idx - b.idx)

  return (
    <div className="flex shrink-0 items-stretch gap-1.5 overflow-x-auto border-b border-line bg-surface px-3 py-2 zp-scroll">
      {pages.map((p) => {
        const active = p.idx === selected
        const nodes = (p.extracted?.nodes ?? []).length
        const flagged = (p.extracted?.nodes ?? []).filter((n) => !n.legible).length
        return (
          <button
            key={p.idx}
            type="button"
            onClick={() => onSelect(p.idx)}
            className={cn(
              'relative w-24 shrink-0 overflow-hidden rounded-md border text-left transition-colors',
              active
                ? 'border-accent ring-1 ring-accent'
                : 'border-line hover:border-line-strong hover:bg-sunken',
            )}
            title={`Page ${p.idx + 1} — ${p.status}`}
          >
            <img
              src={pageImageUrl(ingest.id, p.idx)}
              alt=""
              className={cn(
                'h-14 w-full bg-white object-cover',
                p.status === 'failed' ? 'opacity-40 grayscale' : '',
              )}
            />
            <div className="flex items-center gap-1 px-1.5 py-1">
              <span className="zp-num text-[11px] font-semibold text-ink">{p.idx + 1}</span>
              {p.status === 'failed' ? (
                <AlertTriangle className="h-3 w-3 text-danger" />
              ) : p.status === 'extracted' ? (
                <span className="zp-num text-[10px] text-ink-faint">
                  {nodes} box{nodes === 1 ? '' : 'es'}
                </span>
              ) : (
                <Spinner className="h-3 w-3" />
              )}
              {flagged > 0 ? (
                <span
                  className="zp-num ml-auto rounded bg-warn px-1 text-[9px] font-bold text-white"
                  title={`${flagged} unreadable element(s) on this page`}
                >
                  {flagged}
                </span>
              ) : null}
            </div>
          </button>
        )
      })}
    </div>
  )
}
