import { useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Check, FilePlus2, Layers, Lock, Network, Plus, Trash2 } from 'lucide-react'
import { GraphCanvas } from '@/features/diagram/GraphCanvas'
import { useDiagrams } from '@/features/diagram/queries'
import { DIAGRAM_LEVEL_VALUES, type DiagramLevel, type Role } from '@/api/vocabulary'
import { Badge, Card, CardHeader, CardTitle, Input, Select } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import type { FieldPermission } from '@/features/plan/ownership'
import {
  crossPageEdges,
  mergedGraph,
  pageWord,
  toResolutionList,
  unresolvedEdges,
  unresolvedIllegible,
  type IngestView,
  type Resolutions,
} from './model'
import type { AcceptArgs } from './queries'

/**
 * Accept: deliberate, and it shows what it will do.
 *
 * **Additive.** A photograph is evidence that these five services exist; it
 * says nothing whatsoever about the eleven already in the diagram, and treating
 * its silence as a deletion would let one badly-lit page destroy an afternoon's
 * architecture. Nothing on this screen removes anything.
 *
 * The preview is the same renderer the diagram viewer uses, showing the merged
 * graph with the reviewer's corrections folded in — so the picture being
 * approved is the picture that lands.
 */
export function AcceptPanel({
  projectId,
  ingest,
  resolutions,
  permission,
  effectiveRole,
  mergeAcknowledged,
  onBackToMerge,
  onAccept,
  accepting,
}: {
  projectId: string
  ingest: IngestView
  resolutions: Resolutions
  permission: FieldPermission
  effectiveRole: Role | undefined
  mergeAcknowledged: boolean
  onBackToMerge: () => void
  onAccept: (args: AcceptArgs) => void
  accepting: boolean
}) {
  const merged = ingest.merged
  const diagrams = useDiagrams(projectId)

  const [target, setTarget] = useState<'new' | string>('new')
  const [name, setName] = useState(merged?.title?.trim() ?? '')
  const [level, setLevel] = useState<DiagramLevel>(
    (merged?.level as DiagramLevel | undefined) ?? 'container',
  )
  const [summary, setSummary] = useState('')

  const graph = useMemo(
    () => (merged ? mergedGraph(merged, resolutions) : { nodes: [], edges: [], notes: [] }),
    [merged, resolutions],
  )

  if (!merged) return null

  const blocking = unresolvedIllegible(merged, resolutions)
  const dropped = (merged.nodes ?? []).filter((n) => resolutions[n.slug]?.drop === true)
  const unresolved = unresolvedEdges(merged)
  const crossPage = crossPageEdges(merged)
  const failed = merged.pages_failed ?? []
  const edited = (merged.nodes ?? []).filter(
    (n) => (resolutions[n.slug]?.name?.trim() ?? '').length > 0,
  )

  const creating = target === 'new'
  const nameMissing = creating && name.trim().length === 0

  /**
   * A diagram already carrying this name's slug.
   *
   * The backend refuses the duplicate — correctly, since a slug is identity and
   * is never reissued — but discovering that *after* the accept means reading a
   * conflict at the end of a review. And the right answer is almost always the
   * additive one: this is another photograph of that diagram. So the clash is
   * shown while the name is being typed, with the button that resolves it.
   */
  const wouldBeSlug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  const clash = creating ? (diagrams.data ?? []).find((d) => d.slug === wouldBeSlug) : undefined

  const blocked =
    !permission.writable ||
    !mergeAcknowledged ||
    blocking.length > 0 ||
    nameMissing ||
    Boolean(clash) ||
    accepting

  // The notes accept will record, itemised rather than totalled.
  //
  // Three of the four kinds are exactly predictable from the merged payload.
  // The fourth — the low-confidence roll-up — depends on a threshold the
  // backend owns and does not publish, and a total that is quietly one out is
  // worse than a list that says what it knows: a preview is only worth having
  // if it is right.
  const conflicts = merged.conflicts ?? []
  const lowConfidence = ingest.counts?.low_confidence ?? 0

  // The ownership helper builds its sentence around a field name, and a
  // diagram's semantic layer is owned wholesale — the field is the `*` wildcard
  // (§4.2), which is not a word to show anybody. So the refusal is written for
  // the action being refused, in the shape §7.3 asks for: the route first,
  // because a refusal that leads with the action gets the action. Should they
  // reach the endpoint anyway, the backend's own sentence is rendered verbatim
  // by the error surface.
  const reason = !permission.writable
    ? `Accepting writes to a diagram, so this needs a manager. Raise a proposal, or ask one — a diagram's contents are ${permission.owner}-owned, and your effective role on this project is ${effectiveRole ?? 'developer'}. Uploading and reviewing were always yours; only the write is not.`
    : !mergeAcknowledged
      ? 'Merge review has not been passed yet. Cross-page arrows are where a multi-page extraction goes wrong, and this is the step that gets skipped.'
      : blocking.length > 0
        ? `${blocking.length} element${blocking.length === 1 ? '' : 's'} could not be read and still ${blocking.length === 1 ? 'has' : 'have'} no name. Nothing invents one.`
        : nameMissing
          ? 'Give the new diagram a name. The drawing did not carry a legible title.'
          : clash
            ? `“${clash.name}” already has this identity. A slug is identity and is never reissued, so this cannot create a second one — but if this drawing is another photograph of that diagram, adding to it is the right answer.`
            : undefined

  return (
    <div className="min-h-0 flex-1 overflow-auto zp-scroll">
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-3 px-6 py-5">
        <div>
          <h2 className="font-semibold text-ink">What accepting will do</h2>
          <p className="text-ink-muted">
            Additive, always. A drawing is evidence that these things exist, never evidence that
            everything else does not — nothing on an existing diagram is removed by accepting this.
          </p>
        </div>

        <div className="grid grid-cols-1 gap-3 lg:grid-cols-[1fr_20rem]">
          {/* ── the picture that will land ── */}
          <Card className="flex min-h-96 flex-col overflow-hidden">
            <CardHeader>
              <CardTitle>The graph as it will be written</CardTitle>
              <span className="zp-num text-[11px] text-ink-faint">
                {graph.nodes.length} nodes · {graph.edges.length} edges
              </span>
            </CardHeader>
            <div className="min-h-0 flex-1">
              {graph.nodes.length > 0 ? (
                <GraphCanvas graph={graph} layoutMode="auto" mode="view" />
              ) : (
                <p className="px-4 py-10 text-center text-ink-muted">
                  Every element has been dropped. There is nothing left to accept.
                </p>
              )}
            </div>
            <p className="border-t border-line px-3 py-1.5 text-[11px] text-ink-faint">
              Dashed borders and the confidence badge mark what the model read and nobody has
              checked. They stay marked on the diagram afterwards — a hand-corrected diagram still
              contains extracted elements.
            </p>
          </Card>

          <div className="flex flex-col gap-3">
            {/* ── where it goes ── */}
            <Card>
              <CardHeader>
                <CardTitle>Where it goes</CardTitle>
              </CardHeader>
              <div className="flex flex-col gap-2 px-3 py-3">
                <Select
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                  aria-label="Target diagram"
                >
                  <option value="new">Create a new diagram</option>
                  {(diagrams.data ?? []).map((d) => (
                    <option key={d.id} value={d.id}>
                      Add to {d.name}
                    </option>
                  ))}
                </Select>

                {creating ? (
                  <>
                    <Input
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="Diagram name"
                      aria-label="Diagram name"
                    />
                    <label className="flex flex-col gap-1">
                      <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                        Altitude
                      </span>
                      <Select
                        value={level}
                        onChange={(e) => setLevel(e.target.value as DiagramLevel)}
                        aria-label="Diagram level"
                      >
                        {DIAGRAM_LEVEL_VALUES.map((l) => (
                          <option key={l} value={l}>
                            {l}
                          </option>
                        ))}
                      </Select>
                      <span className="text-[11px] leading-snug text-ink-faint">
                        {/* The backend's reason, because it is a good one. */}
                        A sketch almost never says which altitude it is drawn at, and guessing it
                        wrong mislabels the diagram permanently.
                        {merged.level ? ` The model read it as ${merged.level}.` : ''}
                      </span>
                    </label>
                  </>
                ) : (
                  <p className="text-ink-muted">
                    These elements are added to that diagram as one revision. Anything already on it
                    stays exactly as it is.
                  </p>
                )}

                <Input
                  value={summary}
                  onChange={(e) => setSummary(e.target.value)}
                  placeholder="Revision summary (optional)"
                  aria-label="Revision summary"
                />
              </div>
            </Card>

            {/* ── the effect, itemised ── */}
            <Card>
              <CardHeader>
                <CardTitle>Effect</CardTitle>
              </CardHeader>
              <div className="flex flex-col gap-1.5 px-3 py-3">
                <Effect
                  icon={<Plus className="h-3.5 w-3.5 text-good" />}
                  label={`${graph.nodes.length} element${graph.nodes.length === 1 ? '' : 's'} created or matched`}
                  detail={`${graph.edges.length} relation${graph.edges.length === 1 ? '' : 's'} drawn, read from ${pageWord(merged.pages_read)}`}
                />
                {edited.length > 0 ? (
                  <Effect
                    icon={<Check className="h-3.5 w-3.5 text-good" />}
                    label={`${edited.length} named by you`}
                    detail="Recorded as human_edited with no confidence, because you read them."
                  />
                ) : null}
                {dropped.length > 0 ? (
                  <Effect
                    icon={<Trash2 className="h-3.5 w-3.5 text-ink-faint" />}
                    label={`${dropped.length} dropped`}
                    detail={dropped.map((d) => d.slug).join(', ')}
                  />
                ) : null}
                {unresolved.length > 0 ? (
                  <Effect
                    icon={<FilePlus2 className="h-3.5 w-3.5 text-warn" />}
                    label={`${unresolved.length} unplaceable arrow${unresolved.length === 1 ? '' : 's'} kept as notes`}
                    detail="Not drawn — there is no box to attach them to — and not discarded, because a dropped dependency is the error nobody notices."
                  />
                ) : null}
                {conflicts.length > 0 ? (
                  <Effect
                    icon={<FilePlus2 className="h-3.5 w-3.5 text-warn" />}
                    label={`${conflicts.length} disagreement${conflicts.length === 1 ? '' : 's'} kept as notes`}
                    detail="What two pages could not agree on, recorded rather than settled."
                  />
                ) : null}
                {failed.length > 0 ? (
                  <Effect
                    icon={<FilePlus2 className="h-3.5 w-3.5 text-warn" />}
                    label="One note about the pages that could not be read"
                    detail={`Whatever was drawn on ${pageWord(failed)} is not here, and the note says so.`}
                  />
                ) : null}
                {lowConfidence > 0 ? (
                  <Effect
                    icon={<FilePlus2 className="h-3.5 w-3.5 text-warn" />}
                    label="One note listing what was read with low confidence"
                    detail="Accepted as they stand, with the confidence on each element. Check them against the drawing before anybody plans against them."
                  />
                ) : null}
                {crossPage.length > 0 ? (
                  <Effect
                    icon={<Layers className="h-3.5 w-3.5 text-info" />}
                    label={`${crossPage.length} join${crossPage.length === 1 ? '' : 's'} made across pages`}
                    detail="Drawn on the diagram. You confirmed these in merge review."
                  />
                ) : null}
                <Effect
                  icon={<Network className="h-3.5 w-3.5 text-ink-faint" />}
                  label="Nothing is removed"
                  detail="Accept never deletes. A name that plausibly matches an existing element is recorded as a note rather than merged (§3.4)."
                />
              </div>
            </Card>

            {reason ? (
              <div className="flex items-start gap-2 rounded-card border border-warn/30 bg-warn-soft px-3 py-2">
                <Lock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" />
                <div className="min-w-0">
                  {/* A denial's wording is a prompt layer (§7.3): rendered
                      verbatim when it came from the backend. */}
                  <p className="text-warn-ink">{reason}</p>
                  {clash ? (
                    <Button
                      size="sm"
                      variant="secondary"
                      className="mt-1.5"
                      onClick={() => setTarget(clash.id)}
                    >
                      Add to “{clash.name}” instead
                    </Button>
                  ) : !mergeAcknowledged && permission.writable && blocking.length === 0 ? (
                    <Button size="sm" variant="secondary" className="mt-1.5" onClick={onBackToMerge}>
                      Go to merge review
                    </Button>
                  ) : null}
                </div>
              </div>
            ) : null}

            <Button
              variant="primary"
              size="lg"
              loading={accepting}
              disabled={blocked}
              title={reason}
              onClick={() =>
                onAccept({
                  ...(creating ? {} : { diagramId: target }),
                  ...(creating && name.trim() ? { name: name.trim() } : {}),
                  ...(creating ? { level } : {}),
                  ...(summary.trim() ? { summary: summary.trim() } : {}),
                  resolutions: toResolutionList(resolutions),
                })
              }
            >
              {creating ? 'Create the diagram' : 'Add to the diagram'}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

function Effect({
  icon,
  label,
  detail,
}: {
  icon: ReactNode
  label: string
  detail: string
}) {
  return (
    <div className="flex items-start gap-2">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span className="min-w-0">
        <span className="block font-medium text-ink">{label}</span>
        <span className="block text-[11px] leading-snug text-ink-muted">{detail}</span>
      </span>
    </div>
  )
}

/** What an accepted ingest produced, once it has. */
export function AcceptedSummary({
  projectId,
  ingest,
}: {
  projectId: string
  ingest: IngestView
}) {
  const diagramId = ingest.diagram_id
  return (
    <div className="mx-auto w-full max-w-2xl px-6 py-10">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            <Check className="h-4 w-4 text-good" />
            Accepted
          </CardTitle>
          <Badge tone="good">{ingest.page_count} pages</Badge>
        </CardHeader>
        <div className="flex flex-col gap-2 px-4 py-4">
          <p className="text-ink">
            This drawing has been applied to a diagram. The uploaded bytes and every page's model
            answer are kept as they were, so what was accepted stays checkable against what was on
            the paper.
          </p>
          {diagramId ? (
            <Link to={`/p/${projectId}/diagrams/${diagramId}`}>
              <Button variant="primary" className="mt-1">
                <Network className="h-3.5 w-3.5" />
                Open the diagram
              </Button>
            </Link>
          ) : null}
        </div>
      </Card>
    </div>
  )
}
