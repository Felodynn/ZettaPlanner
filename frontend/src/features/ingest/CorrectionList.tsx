import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, CircleHelp, Eye, EyeOff, Trash2, Undo2 } from 'lucide-react'
import { NODE_KIND_VALUES, type NodeKind } from '@/api/vocabulary'
import { KIND } from '@/features/diagram/vocabulary'
import { Badge, Input, Select } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'
import { pageImageUrl } from './queries'
import {
  corrections,
  displayName,
  flaggedEdges,
  illegibleEvidence,
  isEdited,
  pageWord,
  percent,
  resolvedKind,
  type IngestView,
  type MergedNode,
  type Resolution,
  type Resolutions,
} from './model'

/**
 * The corrections step.
 *
 * Two rules from F5, and the second one is the one that gets skipped:
 *
 *  1. **Sorted by ascending confidence.** A reviewer's attention is finite and
 *     belongs where the model was least sure. Forty elements in extraction
 *     order is forty elements nobody reads.
 *  2. **The source image stays on screen.** Correcting a name against a
 *     remembered photograph is guessing with extra steps, so the page the
 *     element was read on is beside the element being corrected — and it
 *     follows the selection.
 *
 * Grouped rather than flat: unreadable elements *block* the accept and are
 * their own group, flagged elements are the argument, and everything read
 * cleanly is collapsed behind a count so it does not dilute the two groups that
 * matter.
 */
export function CorrectionList({
  ingest,
  resolutions,
  onChange,
}: {
  ingest: IngestView
  resolutions: Resolutions
  onChange: (slug: string, patch: Resolution) => void
}) {
  const merged = ingest.merged
  const groups = useMemo(() => (merged ? corrections(merged) : null), [merged])
  const edges = useMemo(() => (merged ? flaggedEdges(merged) : []), [merged])
  const [focused, setFocused] = useState<string | undefined>()
  const [cleanOpen, setCleanOpen] = useState(false)

  if (!merged || !groups) return null

  const focusedNode =
    (merged.nodes ?? []).find((n) => n.slug === focused) ??
    groups.blocking[0] ??
    groups.flagged[0] ??
    (merged.nodes ?? [])[0]
  const focusedPage = focusedNode?.pages?.[0] ?? 0

  const row = (node: MergedNode) => (
    <CorrectionRow
      key={node.slug}
      node={node}
      resolution={resolutions[node.slug]}
      active={focusedNode?.slug === node.slug}
      onFocus={() => setFocused(node.slug)}
      onChange={(patch) => onChange(node.slug, patch)}
    />
  )

  return (
    <div className="flex min-h-0 flex-1">
      <div className="min-h-0 min-w-0 flex-1 overflow-auto zp-scroll">
        <div className="flex flex-col gap-4 px-4 py-4">
          {groups.blocking.length > 0 ? (
            <section>
              <GroupHeading
                title={`${groups.blocking.length} element${groups.blocking.length === 1 ? '' : 's'} could not be read`}
                tone="danger"
                body="These cannot reach a diagram under an invented name — there is no threshold and no heuristic, because a heuristic here picks confidently and wrongly, and a plausible wrong label is worse than a blank one because nobody checks it. Read each one off the page and type what it says, or drop it."
              />
              <div className="mt-2 flex flex-col gap-1.5">{groups.blocking.map(row)}</div>
            </section>
          ) : null}

          {groups.flagged.length > 0 ? (
            <section>
              <GroupHeading
                title={`${groups.flagged.length} flagged for review`}
                tone="warn"
                body="Least confident first. The model read these, and said how sure it was; a low reading is stored deliberately rather than replaced with a plausible guess."
              />
              <div className="mt-2 flex flex-col gap-1.5">{groups.flagged.map(row)}</div>
            </section>
          ) : null}

          {groups.clean.length > 0 ? (
            <section>
              <button
                type="button"
                onClick={() => setCleanOpen((v) => !v)}
                className="flex w-full items-center gap-1.5 text-left text-ink-muted hover:text-ink"
              >
                {cleanOpen ? (
                  <ChevronDown className="h-3.5 w-3.5" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5" />
                )}
                <span className="font-semibold">
                  {groups.clean.length} read cleanly and not flagged
                </span>
                {cleanOpen ? null : (
                  <span className="text-[11px] text-ink-faint">
                    — still extracted, still worth a look before anybody plans against them
                  </span>
                )}
              </button>
              {cleanOpen ? (
                <div className="mt-2 flex flex-col gap-1.5">{groups.clean.map(row)}</div>
              ) : null}
            </section>
          ) : null}

          {edges.length > 0 ? (
            <section>
              <GroupHeading
                title={`${edges.length} arrow${edges.length === 1 ? '' : 's'} flagged`}
                tone="warn"
                body="Arrows carry no correction here. Accept takes decisions about boxes; a relation is fixed on the canvas afterwards, where you can see what it connects. Dropping a box drops every arrow touching it."
              />
              <ul className="mt-2 flex flex-col gap-1">
                {edges.map((e, i) => (
                  <li
                    key={`${e.from}-${e.to}-${i}`}
                    className="rounded-md border border-line px-2.5 py-1.5"
                  >
                    <p className="text-ink">
                      <span className="font-mono text-[11px]">{e.from || '(nothing legible)'}</span>{' '}
                      <span className="text-ink-faint">{e.relation}</span>{' '}
                      <span className="font-mono text-[11px]">{e.to || '(nothing legible)'}</span>
                    </p>
                    <p className="text-[11px] text-ink-muted">
                      {e.review_reason} · read on {pageWord(e.pages)} · {percent(e.confidence)}{' '}
                      confident
                    </p>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </div>
      </div>

      {/* The page it was read on, beside the thing being corrected. */}
      <div className="flex w-[38%] min-w-80 shrink-0 flex-col border-l border-line bg-sunken">
        <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
          <Eye className="h-3.5 w-3.5 text-ink-faint" />
          <span className="font-semibold text-ink">
            {focusedNode
              ? `${displayName(focusedNode, resolutions[focusedNode.slug])} — ${pageWord(focusedNode.pages)}`
              : 'The drawing'}
          </span>
          <div className="flex-1" />
          <a
            href={pageImageUrl(ingest.id, focusedPage)}
            target="_blank"
            rel="noreferrer"
            className="text-[11px] text-ink-faint underline-offset-2 hover:text-ink hover:underline"
          >
            Full size
          </a>
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-2 zp-scroll">
          <img
            src={pageImageUrl(ingest.id, focusedPage)}
            alt={`Page ${focusedPage + 1}`}
            className="mx-auto max-w-full rounded border border-line bg-white"
          />
        </div>
        {focusedNode && !focusedNode.legible ? (
          <p className="shrink-0 border-t border-line bg-warn-soft px-3 py-2 text-warn-ink">
            The model {illegibleEvidence(focusedNode)}. Find it on the page and type what it says, or
            drop it.
          </p>
        ) : null}
      </div>
    </div>
  )
}

function GroupHeading({
  title,
  body,
  tone,
}: {
  title: string
  body: string
  tone: 'danger' | 'warn'
}) {
  return (
    <div
      className={cn(
        'rounded-md border px-3 py-2',
        tone === 'danger' ? 'border-danger/30 bg-danger-soft' : 'border-warn/30 bg-warn-soft',
      )}
    >
      <p className={cn('font-semibold', tone === 'danger' ? 'text-danger-ink' : 'text-warn-ink')}>
        {title}
      </p>
      <p className="mt-0.5 text-ink">{body}</p>
    </div>
  )
}

function CorrectionRow({
  node,
  resolution,
  active,
  onFocus,
  onChange,
}: {
  node: MergedNode
  resolution: Resolution | undefined
  active: boolean
  onFocus: () => void
  onChange: (patch: Resolution) => void
}) {
  const dropped = resolution?.drop === true
  const edited = isEdited(node, resolution)
  const kind = resolvedKind(node, resolution)
  const spec = KIND[kind]

  const patch = (next: Partial<Resolution>) =>
    onChange({ slug: node.slug, ...resolution, ...next })

  return (
    <div
      onFocus={onFocus}
      onClick={onFocus}
      className={cn(
        'rounded-md border px-2.5 py-2 transition-colors',
        dropped ? 'border-line bg-sunken opacity-60' : 'border-line bg-surface',
        active ? 'border-accent-line ring-1 ring-accent-line' : '',
      )}
    >
      <div className="flex items-center gap-2">
        <spec.Icon className="h-4 w-4 shrink-0" style={{ color: spec.hue }} aria-hidden />
        <span className={cn('min-w-0 flex-1 truncate font-medium text-ink', dropped && 'line-through')}>
          {displayName(node, resolution)}
        </span>

        {!node.legible ? (
          <Badge tone="danger">
            <CircleHelp className="h-3 w-3" />
            unreadable
          </Badge>
        ) : (
          <Badge
            tone={node.confidence < 0.6 ? 'warn' : 'neutral'}
            title="The vision model's own certainty reading this element. Low is a value, not a defect."
          >
            {percent(node.confidence)}
          </Badge>
        )}
        {edited ? <Badge tone="good">you read it</Badge> : null}

        <Button
          size="icon"
          variant="ghost"
          className="h-6 w-6"
          title={
            dropped
              ? 'Keep this element after all'
              : 'Discard this element and every arrow touching it'
          }
          aria-label={dropped ? `Keep ${node.slug}` : `Drop ${node.slug}`}
          onClick={() => (dropped ? patch({ drop: false }) : patch({ drop: true }))}
        >
          {dropped ? <Undo2 className="h-3.5 w-3.5" /> : <Trash2 className="h-3.5 w-3.5" />}
        </Button>
      </div>

      <p className="mt-0.5 flex flex-wrap items-center gap-1.5 text-[11px] text-ink-faint">
        <span className="font-mono">{node.slug}</span>
        <span>· {pageWord(node.pages)}</span>
        {node.review_reason ? <span className="text-warn-ink">· {node.review_reason}</span> : null}
        {/* Evidence, labelled as evidence. Never the title. */}
        {!node.legible ? <span>· the model {illegibleEvidence(node)}</span> : null}
      </p>

      {!dropped ? (
        <div className="mt-1.5 flex items-center gap-1.5">
          <Input
            className="h-7 flex-1 text-xs"
            value={resolution?.name ?? ''}
            placeholder={
              node.legible
                ? `${node.name} — type a correction only if this is wrong`
                : 'what does this box say?'
            }
            onChange={(e) => patch({ name: e.target.value })}
            aria-label={`Name for ${node.slug}`}
          />
          <Select
            className="h-7 w-40 text-xs"
            value={kind}
            onChange={(e) => patch({ kind: e.target.value as NodeKind })}
            aria-label={`Kind for ${node.slug}`}
            title={spec.means}
          >
            {NODE_KIND_VALUES.map((k) => (
              <option key={k} value={k}>
                {KIND[k].label}
              </option>
            ))}
          </Select>
        </div>
      ) : (
        <p className="mt-1.5 flex items-center gap-1.5 text-[11px] text-ink-muted">
          <EyeOff className="h-3 w-3" />
          Dropped. This element and every arrow touching it stay out of the diagram.
        </p>
      )}

      {edited ? (
        <p className="mt-1 text-[11px] text-good-ink">
          {/* §3.7: anything a human renamed carries human_edited and no
              confidence, because a human read it. */}
          Recorded as read by you, with no confidence attached — the model's uncertainty was about a
          label it no longer supplied.
        </p>
      ) : null}
    </div>
  )
}
