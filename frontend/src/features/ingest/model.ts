import type { components } from '@/api/schema'
import { NODE_KIND_VALUES, RELATION_VALUES, type NodeKind, type Relation } from '@/api/vocabulary'
import type { EdgeView, NodeView, SemanticGraph } from '@/features/diagram/model'

/**
 * Ingest review: the shapes, and the adapters onto the shared renderer.
 *
 * §3.4a — a drawing is evidence, not a statement. Nothing here writes; accept
 * does. Everything in this file is about presenting what the model read
 * *without* smoothing it: an unreadable box stays unreadable until somebody
 * names it, an arrow whose far end is nowhere stays visible as a question, and
 * a low confidence is rendered as a value rather than hidden as a defect.
 */

export type IngestView = components['schemas']['IngestView']
export type IngestSummary = components['schemas']['IngestSummary']
export type IngestPageView = components['schemas']['IngestPageView']
export type Merged = components['schemas']['Merged']
export type MergedNode = components['schemas']['MergedNode']
export type MergedEdge = components['schemas']['MergedEdge']
export type Conflict = components['schemas']['Conflict']
export type PageExtraction = components['schemas']['PageExtraction']
export type ExtractedNode = components['schemas']['ExtractedNode']
export type ExtractedEdge = components['schemas']['ExtractedEdge']
export type ReviewCounts = components['schemas']['ReviewCounts']
export type AcceptResponse = components['schemas']['AcceptResponse']

/**
 * One human decision about one extracted element.
 *
 * `Item4` is what the generator names the anonymous body member; the alias is
 * the only thing here that is not read straight off the document, and it
 * renames nothing.
 */
export type Resolution = components['schemas']['Item4']

/** Resolutions being edited, keyed by the merged element's slug. */
export type Resolutions = Record<string, Resolution>

// ── the closed vocabularies, narrowed ────────────────────────────────────────
//
// The merged payload types `kind` and `relation` as plain strings because the
// merge stores whatever it reconciled, while the diagram's node and edge views
// carry the enums. The backend validates every extraction against the same
// closed vocabulary before it is stored (D6), so an unknown value here is
// unreachable — these guards exist to keep the types honest across that seam
// rather than to cope with a value the API can produce.

export function asNodeKind(kind: string): NodeKind {
  return (NODE_KIND_VALUES as readonly string[]).includes(kind) ? (kind as NodeKind) : 'component'
}

export function asRelation(relation: string): Relation {
  return (RELATION_VALUES as readonly string[]).includes(relation)
    ? (relation as Relation)
    : 'depends_on'
}

/**
 * A presentation-only identity for a page's elements.
 *
 * The merged payload already carries real slugs; a single page's extraction
 * does not, because identity is decided by the merge. This never leaves the
 * client and is never sent anywhere — it exists so React Flow has a key.
 */
function localSlug(name: string): string {
  return (
    name
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'unnamed'
  )
}

// ── resolutions ──────────────────────────────────────────────────────────────

/** The name this element will carry, after the human's decision. */
export function resolvedName(node: MergedNode, r: Resolution | undefined): string {
  const supplied = r?.name?.trim()
  return supplied && supplied.length > 0 ? supplied : node.name
}

export function resolvedKind(node: MergedNode, r: Resolution | undefined): NodeKind {
  return r?.kind ?? asNodeKind(node.kind)
}

/**
 * What to call an element on screen.
 *
 * An unreadable element has **no name**, and the merge's `name` for one is the
 * model's positional description of where the box was — "box bottom left" —
 * or a generated placeholder. Rendering that as the element's name is the
 * invented label §3.4a exists to prevent, one step removed: it reads as
 * something somebody could accept, and nobody checks a box that already has a
 * name. So it is shown as evidence, labelled, and never as the title.
 */
export function displayName(node: MergedNode, r: Resolution | undefined): string {
  if (node.legible) return resolvedName(node, r)
  const supplied = r?.name?.trim()
  return supplied && supplied.length > 0 ? supplied : 'unreadable'
}

/** What the model could offer about an element it could not read. */
export function illegibleEvidence(node: MergedNode): string {
  const seen = node.verbatim?.trim()
  const where = node.name?.trim()
  if (seen && where) return `made out “${seen}”, described as “${where}”`
  if (seen) return `made out “${seen}” and nothing more`
  if (where) return `nothing legible; described only as “${where}”`
  return 'nothing legible at all'
}

/** True once somebody has read this element themselves. */
export function isEdited(node: MergedNode, r: Resolution | undefined): boolean {
  const named = (r?.name?.trim() ?? '').length > 0
  const rekinded = r?.kind !== undefined && r.kind !== node.kind
  return named || rekinded
}

/**
 * Unreadable elements still waiting on a decision.
 *
 * §3.4a: an element the model could not read **cannot reach a diagram until a
 * human supplies the name**. No threshold and no heuristic — the backend
 * refuses the accept, and this is the same gate rendered before the refusal so
 * nobody discovers it by being turned away.
 */
export function unresolvedIllegible(merged: Merged, res: Resolutions): MergedNode[] {
  return (merged.nodes ?? []).filter((n) => {
    if (n.legible) return false
    const r = res[n.slug]
    if (r?.drop) return false
    return (r?.name?.trim() ?? '').length === 0
  })
}

/** Resolutions worth sending: the ones that actually decide something. */
export function toResolutionList(res: Resolutions): Resolution[] {
  return Object.values(res).filter(
    (r) => r.drop === true || (r.name?.trim() ?? '').length > 0 || r.kind !== undefined,
  )
}

export function isDropped(res: Resolutions, slug: string): boolean {
  return res[slug]?.drop === true
}

// ── the correction list ──────────────────────────────────────────────────────

/**
 * The reviewer's work, grouped and sorted.
 *
 * F5: **sort by confidence ascending** — attention is finite and belongs where
 * the model was least sure. And not as a flat list of forty: unreadable
 * elements block the accept and are their own group, flagged ones are the ones
 * worth arguing about, and everything the model read cleanly is collapsed
 * behind a count so it does not dilute the two groups that matter.
 */
export interface Corrections {
  /** Unreadable. Blocks accept until named or dropped. */
  blocking: MergedNode[]
  /** The merge flagged these: low confidence, or a disagreement between pages. */
  flagged: MergedNode[]
  /** Read cleanly and not flagged. */
  clean: MergedNode[]
}

const byConfidence = (a: MergedNode, b: MergedNode) => a.confidence - b.confidence || a.slug.localeCompare(b.slug)

export function corrections(merged: Merged): Corrections {
  const nodes = merged.nodes ?? []
  return {
    blocking: nodes.filter((n) => !n.legible).sort(byConfidence),
    flagged: nodes.filter((n) => n.legible && n.needs_review).sort(byConfidence),
    clean: nodes.filter((n) => n.legible && !n.needs_review).sort(byConfidence),
  }
}

/** Edges the merge flagged. Sorted the same way, and read-only (see below). */
export function flaggedEdges(merged: Merged): MergedEdge[] {
  return (merged.edges ?? [])
    .filter((e) => e.needs_review)
    .sort((a, b) => a.confidence - b.confidence || a.from.localeCompare(b.from))
}

/** Arrows that ran off a page and found nothing. */
export function unresolvedEdges(merged: Merged): MergedEdge[] {
  return (merged.edges ?? []).filter((e) => e.unresolved)
}

/** Arrows whose two ends were read on different pages. */
export function crossPageEdges(merged: Merged): MergedEdge[] {
  return (merged.edges ?? []).filter((e) => e.cross_page && !e.unresolved)
}

// ── onto the shared renderer (§1.1 rule 1) ───────────────────────────────────

/**
 * The merged payload as the renderer consumes it, with the human's decisions
 * already folded in.
 *
 * This is the same `GraphCanvas` the diagram viewer and the editor use, in
 * `view` mode. Building a second canvas for review would mean a second
 * interpretation of the node vocabulary, which is the one thing §1.1 exists to
 * prevent — and it would mean a reviewer judging a picture that is not the
 * picture they are about to accept.
 *
 * Unresolved arrows are deliberately absent: there is no second node to attach
 * them to, and drawing one would require inventing the box it points at. They
 * are surfaced in merge review instead, which is where the backend records
 * them as Notes.
 */
export function mergedGraph(merged: Merged, res: Resolutions): SemanticGraph {
  const kept = (merged.nodes ?? []).filter((n) => !isDropped(res, n.slug))
  const keptSlugs = new Set(kept.map((n) => n.slug))

  const nodes: NodeView[] = kept.map((n) => {
    const r = res[n.slug]
    const edited = isEdited(n, r)
    return {
      id: n.slug,
      slug: n.slug,
      entity_id: '',
      name: resolvedName(n, r),
      kind: resolvedKind(n, r),
      attrs: {},
      // A human who supplies the name has read it, so the provenance says so
      // and the confidence goes entirely — the model's uncertainty was about a
      // label it no longer supplied. This mirrors what accept writes.
      provenance: edited ? 'human_edited' : 'vlm_extracted',
      ...(edited ? {} : { confidence: n.confidence }),
      ...(n.summary ? { summary: n.summary } : {}),
      ...(n.parent && keptSlugs.has(n.parent) ? { parent: n.parent } : {}),
    }
  })

  const edges: EdgeView[] = (merged.edges ?? [])
    .filter((e) => !e.unresolved && keptSlugs.has(e.from) && keptSlugs.has(e.to))
    .map((e) => ({
      id: `${e.from} ${e.to} ${e.relation}`,
      from: e.from,
      to: e.to,
      relation: asRelation(e.relation),
      attrs: {},
      provenance: 'vlm_extracted',
      confidence: e.confidence,
      ...(e.label ? { label: e.label } : {}),
    }))

  return { nodes, edges, notes: [] }
}

/**
 * One page's extraction, exactly as the model returned it.
 *
 * Shown beside the page image, because the reviewer's job is comparison and a
 * screen that makes them switch views is a screen that gets approved without
 * being checked. This is the *page*, before the merge reconciled anything —
 * comparing the merged graph to one photograph would attribute another page's
 * boxes to this one.
 */
export function pageGraph(extraction: PageExtraction): SemanticGraph {
  const bySlug = new Map<string, NodeView>()
  const slugForName = new Map<string, string>()
  let illegible = 0

  for (const n of extraction.nodes ?? []) {
    const slug = n.legible ? localSlug(n.name) : `unreadable-${++illegible}`
    if (bySlug.has(slug)) continue
    if (n.legible) slugForName.set(n.name.trim().toLowerCase(), slug)
    bySlug.set(slug, {
      id: slug,
      slug,
      entity_id: '',
      name: n.legible ? n.name : n.verbatim ? `“${n.verbatim}”` : 'unreadable',
      kind: asNodeKind(n.kind),
      attrs: {},
      provenance: 'vlm_extracted',
      confidence: n.confidence,
      ...(n.summary ? { summary: n.summary } : {}),
    })
  }

  // Containment, after every name on this page is known. A parent naming
  // nothing on this page is left off rather than invented.
  for (const n of extraction.nodes ?? []) {
    if (!n.legible || !n.parent) continue
    const self = bySlug.get(localSlug(n.name))
    const parent = slugForName.get(n.parent.trim().toLowerCase())
    if (self && parent && parent !== self.slug) self.parent = parent
  }

  const edges: EdgeView[] = []
  for (const [i, e] of (extraction.edges ?? []).entries()) {
    const from = slugForName.get(e.from.trim().toLowerCase())
    const to = slugForName.get(e.to.trim().toLowerCase())
    // An arrow with an end this page does not contain is not drawable here. It
    // is listed beside the canvas instead — that is what the merge step is for.
    if (!from || !to) continue
    edges.push({
      id: `${i}:${from} ${to}`,
      from,
      to,
      relation: asRelation(e.relation),
      attrs: {},
      provenance: 'vlm_extracted',
      confidence: e.confidence,
      ...(e.label ? { label: e.label } : {}),
    })
  }

  return { nodes: [...bySlug.values()], edges, notes: [] }
}

/** Arrows on this page whose other end is not on it. */
export function offPageEdges(extraction: PageExtraction): ExtractedEdge[] {
  const names = new Set(
    (extraction.nodes ?? []).filter((n) => n.legible).map((n) => n.name.trim().toLowerCase()),
  )
  return (extraction.edges ?? []).filter(
    (e) => !names.has(e.from.trim().toLowerCase()) || !names.has(e.to.trim().toLowerCase()),
  )
}

// ── presentation helpers ─────────────────────────────────────────────────────

/** Page indices as humans count them, from one. Matches the backend's wording. */
export function pageWord(pages: number[] | null | undefined): string {
  const list = pages ?? []
  if (list.length === 0) return 'no page'
  const parts = list.map((p) => String(p + 1))
  if (parts.length === 1) return `page ${parts[0]}`
  return `pages ${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

export function percent(confidence: number | undefined): string {
  return confidence === undefined ? '—' : `${Math.round(confidence * 100)}%`
}

/** Nothing further happens on its own. */
export function isTerminal(status: IngestSummary['status']): boolean {
  return status === 'accepted' || status === 'failed'
}

/** The ingest is still working. Progress is worth showing, review is not yet. */
export function isWorking(status: IngestSummary['status']): boolean {
  return status === 'uploaded' || status === 'extracting' || status === 'merging'
}
