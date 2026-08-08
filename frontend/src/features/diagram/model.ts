import type { components } from '@/api/schema'
import type { NodeKind, Relation } from '@/api/vocabulary'

export type DiagramView = components['schemas']['DiagramView']
export type NodeView = components['schemas']['NodeView']
export type EdgeView = components['schemas']['EdgeView']
export type PlacementView = components['schemas']['PlacementView']
export type NoteView = components['schemas']['NoteView']

/**
 * The semantic graph, as the renderer consumes it.
 *
 * Identity is the **entity slug**, not the node id: the API keys edges by
 * `from`/`to` slugs and containment by `parent` slug, and a renderer that
 * indexed by node id would have to resolve every edge through a second map it
 * maintains itself. One identity, used everywhere.
 *
 * §1.1 rule 2: there are no coordinates in this type. Layout is a separate
 * input to the renderer, and a coordinate that reached a semantic save would
 * be a contract violation on the client side (D5).
 */
export interface SemanticGraph {
  nodes: NodeView[]
  edges: EdgeView[]
  notes: NoteView[]
}

/** How a node or edge differs from the state being compared against. */
export type DiffMark = 'added' | 'removed' | 'changed' | 'unchanged'

export interface DiffOverlay {
  nodes: Record<string, DiffMark>
  edges: Record<string, DiffMark>
  /** Field-level detail for a `changed` node, keyed by slug. */
  nodeChanges?: Record<string, { field: string; before: unknown; after: unknown }[]>
}

export type CanvasMode = 'view' | 'edit' | 'diff'

/**
 * The renderer's props (§1.1 rule 1).
 *
 * The canvas is one component used three times — ingest review, direct
 * editing, and proposal-diff review are the same graph renderer in three
 * modes. Building three canvases means three sets of bugs and three divergent
 * interpretations of the node vocabulary, so this interface is the only way in.
 */
export interface CanvasProps {
  graph: SemanticGraph
  /** Stored coordinates, by node id. Absent means auto-layout. */
  layout?: Record<string, PlacementView>
  layoutMode: 'auto' | 'manual'
  mode: CanvasMode
  diff?: DiffOverlay
  selected?: string | undefined
  onSelect?: (slug: string | undefined) => void
  /** Edit mode only. Absent handlers mean the affordance is not offered. */
  onNodeMoved?: (slug: string, x: number, y: number) => void
  onReparent?: (slug: string, parent: string | undefined) => void
  onConnect?: (from: string, to: string) => void
  className?: string
}

// ── helpers over the graph ───────────────────────────────────────────────────

export function nodeBySlug(graph: SemanticGraph): Map<string, NodeView> {
  return new Map(graph.nodes.map((n) => [n.slug, n]))
}

/**
 * A node's effective provenance is the stronger of its own and its entity's
 * (D56, frontend-notes §7).
 *
 * The backend computes this and sends the effective value, so this is only
 * about *reading* it — do not re-derive it from anything narrower, or a box a
 * person corrected yesterday will show as unchecked.
 */
export function isUnchecked(node: NodeView): boolean {
  return node.provenance === 'vlm_extracted'
}

/** Confidence low enough to be worth a reviewer's attention. */
export function isLowConfidence(confidence: number | undefined): boolean {
  return confidence !== undefined && confidence < 0.6
}

export function kindOf(node: NodeView): NodeKind {
  return node.kind
}

export function relationOf(edge: EdgeView): Relation {
  return edge.relation
}

/** Children of each container, by parent slug. */
export function childrenByParent(graph: SemanticGraph): Map<string, NodeView[]> {
  const map = new Map<string, NodeView[]>()
  for (const n of graph.nodes) {
    if (!n.parent) continue
    map.set(n.parent, [...(map.get(n.parent) ?? []), n])
  }
  return map
}

/** Nodes with no container. */
export function rootNodes(graph: SemanticGraph): NodeView[] {
  const slugs = new Set(graph.nodes.map((n) => n.slug))
  // A parent that is not itself on the diagram is a dangling reference; the
  // node is drawn at the top level rather than dropped.
  return graph.nodes.filter((n) => !n.parent || !slugs.has(n.parent))
}
