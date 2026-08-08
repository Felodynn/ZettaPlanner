import { create } from 'zustand'
import type { EdgeView, NodeView, SemanticGraph } from './model'
import type { NodeKind, Relation } from '@/api/vocabulary'

/**
 * The editor's working copy.
 *
 * Client state, and the only place this app keeps any (FRONTEND §1):
 * everything else is server state. It exists because a diagram edit is a
 * sequence of changes that becomes **one revision** on save (D9), so the
 * intermediate states have to live somewhere that is not the server.
 *
 * Two things are deliberately absent.
 *
 * **Coordinates.** §1.1 rule 2: the semantic save carries nodes, edges and
 * relations; layout goes to a different endpoint entirely. Keeping them in one
 * store is how a coordinate ends up in a revision payload, so the working copy
 * has no x or y in it at all and `pendingLayout` is a separate map that only
 * the layout writer reads.
 *
 * **Undo across a save.** Undo is local to the working copy and does not
 * survive it (F4). Server history is revisions; an undo stack that appeared to
 * reach past a save would be offering to undo something another person can
 * already see.
 */

interface Snapshot {
  nodes: NodeView[]
  edges: EdgeView[]
  /** What the change was, for the undo tooltip. */
  label: string
}

export interface PendingMove {
  x: number
  y: number
}

interface EditorState {
  /**
   * Which diagram the working copy holds, or null when it holds none.
   *
   * The seed guard, and it lives here rather than in a component ref for one
   * reason: React remounts an effect without remounting the component, so a
   * ref survives the cleanup that empties this store. The guard has to be the
   * same thing the cleanup clears, or "already seeded" outlives the seed.
   */
  diagramId: string | null
  /** Null until edit mode is entered. */
  base: SemanticGraph | null
  baseRevision: number
  nodes: NodeView[]
  edges: EdgeView[]

  past: Snapshot[]
  future: Snapshot[]

  /** Node id → coordinate, awaiting the layout write. Never sent in a revision. */
  pendingLayout: Record<string, PendingMove>
  /** True once anything has been dragged, so the manual-mode notice fires once. */
  flippedToManual: boolean

  begin: (graph: SemanticGraph, revision: number, diagramId: string) => void
  discard: () => void
  /** Adopts a newly-saved server state as the new base, clearing history. */
  rebase: (graph: SemanticGraph, revision: number) => void

  addNode: (kind: NodeKind, name: string, parent?: string) => string
  renameNode: (slug: string, name: string) => void
  setNodeKind: (slug: string, kind: NodeKind) => void
  setNodeSummary: (slug: string, summary: string) => void
  deleteNode: (slug: string) => void
  reparent: (slug: string, parent: string | undefined) => void

  addEdge: (from: string, to: string, relation: Relation) => void
  setEdgeRelation: (id: string, relation: Relation) => void
  setEdgeLabel: (id: string, label: string) => void
  deleteEdge: (id: string) => void

  moveNode: (nodeId: string, x: number, y: number) => void
  /**
   * Pins nodes at where they currently are, without treating it as a move.
   *
   * Used once, when a person first drags anything: everything else is written
   * down exactly as it looks so that nothing recomputes afterwards.
   */
  freezeLayout: (positions: Record<string, PendingMove>) => void
  /** Forgets the moves that have been written. Omit to forget all of them. */
  clearPendingLayout: (nodeIds?: string[]) => void
  /**
   * Adopts written coordinates as the working copy's own.
   *
   * The nodes here are the session's copies, seeded when the editor opened, so
   * their `layout` is whatever the server said then. Without this, letting go
   * of a pending move loses the only record of the drag and the box springs
   * back to where it was an hour ago. Layout is not semantic — this does not
   * touch undo history and cannot make the editor look dirty.
   */
  commitLayout: (placements: { node_id: string; x: number; y: number }[]) => void

  undo: () => void
  redo: () => void
  canUndo: () => boolean
  canRedo: () => boolean
  isDirty: () => boolean
}

/** A slug from a display name: lowercase, hyphenated, ASCII. */
export function slugify(name: string): string {
  return (
    name
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[^\w\s-]/g, '')
      .trim()
      .replace(/[\s_]+/g, '-')
      .replace(/-+/g, '-')
      .slice(0, 60) || 'node'
  )
}

const EMPTY: SemanticGraph = { nodes: [], edges: [], notes: [] }

export const useEditor = create<EditorState>((set, get) => {
  /** Records the current state on the undo stack, then applies a change. */
  const commit = (label: string, next: Partial<Pick<EditorState, 'nodes' | 'edges'>>) =>
    set((s) => ({
      past: [...s.past, { nodes: s.nodes, edges: s.edges, label }],
      // Any new change abandons the redo branch, as everywhere else.
      future: [],
      ...next,
    }))

  return {
    base: null,
    baseRevision: 0,
    diagramId: null,
    nodes: [],
    edges: [],
    past: [],
    future: [],
    pendingLayout: {},
    flippedToManual: false,

    begin: (graph, revision, diagramId) =>
      set({
        diagramId,
        base: graph,
        baseRevision: revision,
        nodes: graph.nodes.map((n) => ({ ...n })),
        edges: graph.edges.map((e) => ({ ...e })),
        past: [],
        future: [],
        pendingLayout: {},
        flippedToManual: false,
      }),

    discard: () =>
      set({
        diagramId: null,
        base: null,
        baseRevision: 0,
        nodes: [],
        edges: [],
        past: [],
        future: [],
        pendingLayout: {},
        flippedToManual: false,
      }),

    rebase: (graph, revision) =>
      set({
        base: graph,
        baseRevision: revision,
        nodes: graph.nodes.map((n) => ({ ...n })),
        edges: graph.edges.map((e) => ({ ...e })),
        past: [],
        future: [],
        pendingLayout: {},
      }),

    addNode: (kind, name, parent) => {
      const existing = new Set(get().nodes.map((n) => n.slug))
      let slug = slugify(name)
      let n = 2
      while (existing.has(slug)) slug = `${slugify(name)}-${n++}`

      const node: NodeView = {
        // A local id until the server issues one. Never sent: the save is
        // keyed by slug (§7.1), which is the node's identity.
        id: `new:${slug}`,
        entity_id: '',
        slug,
        kind,
        name,
        attrs: {},
        provenance: 'human_created',
        ...(parent ? { parent } : {}),
      }
      commit(`add ${kind}`, { nodes: [...get().nodes, node] })
      return slug
    },

    renameNode: (slug, name) =>
      commit('rename', {
        nodes: get().nodes.map((n) =>
          n.slug === slug ? { ...n, name, provenance: humanised(n) } : n,
        ),
      }),

    setNodeKind: (slug, kind) =>
      commit('change kind', {
        nodes: get().nodes.map((n) =>
          n.slug === slug ? { ...n, kind, provenance: humanised(n) } : n,
        ),
      }),

    setNodeSummary: (slug, summary) =>
      commit('edit summary', {
        nodes: get().nodes.map((n) =>
          n.slug === slug ? { ...n, summary, provenance: humanised(n) } : n,
        ),
      }),

    deleteNode: (slug) =>
      commit('delete node', {
        // Edges touching it go with it, and so does anyone's claim to be
        // inside it — a child left pointing at a deleted parent would be
        // dropped silently by the renderer.
        nodes: get()
          .nodes.filter((n) => n.slug !== slug)
          .map((n) => (n.parent === slug ? { ...n, parent: undefined } : n)),
        edges: get().edges.filter((e) => e.from !== slug && e.to !== slug),
      }),

    reparent: (slug, parent) =>
      commit(parent ? 'move into boundary' : 'move out of boundary', {
        nodes: get().nodes.map((n) => (n.slug === slug ? { ...n, parent } : n)),
      }),

    addEdge: (from, to, relation) => {
      const exists = get().edges.some(
        (e) => e.from === from && e.to === to && e.relation === relation,
      )
      // The backend's uniqueness is (diagram, from, to, relation); adding a
      // duplicate here would only fail on save.
      if (exists || from === to) return
      const edge: EdgeView = {
        id: `new:${from}:${relation}:${to}`,
        from,
        to,
        relation,
        attrs: {},
        provenance: 'human_created',
      }
      commit(`add ${relation}`, { edges: [...get().edges, edge] })
    },

    setEdgeRelation: (id, relation) =>
      commit('change relation', {
        edges: get().edges.map((e) => (e.id === id ? { ...e, relation } : e)),
      }),

    setEdgeLabel: (id, label) =>
      commit('label edge', {
        edges: get().edges.map((e) => (e.id === id ? { ...e, label } : e)),
      }),

    deleteEdge: (id) => commit('delete edge', { edges: get().edges.filter((e) => e.id !== id) }),

    // Not on the undo stack, and not in `nodes`: a coordinate is not a
    // semantic change, and mixing the two is how one ends up in a revision.
    moveNode: (nodeId, x, y) =>
      set((s) => ({
        pendingLayout: { ...s.pendingLayout, [nodeId]: { x, y } },
        flippedToManual: true,
      })),

    // Existing pending moves win: this only fills in the nodes nobody has
    // touched, so it can never undo a drag that is already in flight.
    freezeLayout: (positions) =>
      set((s) => ({
        pendingLayout: { ...positions, ...s.pendingLayout },
        flippedToManual: true,
      })),

    commitLayout: (placements) =>
      set((s) => {
        const byId = new Map(placements.map((p) => [p.node_id, p]))
        return {
          nodes: s.nodes.map((n) => {
            const p = byId.get(n.id)
            if (!p) return n
            return {
              ...n,
              layout: {
                ...(n.layout ?? { collapsed: false, unplaced: false }),
                x: p.x,
                y: p.y,
                // Written by a person, so no longer a fallback.
                unplaced: false,
              },
            }
          }),
        }
      }),

    clearPendingLayout: (nodeIds) =>
      set((s) => {
        if (!nodeIds) return { pendingLayout: {} }
        const rest = { ...s.pendingLayout }
        for (const id of nodeIds) delete rest[id]
        return { pendingLayout: rest }
      }),

    undo: () =>
      set((s) => {
        const previous = s.past.at(-1)
        if (!previous) return s
        return {
          past: s.past.slice(0, -1),
          future: [{ nodes: s.nodes, edges: s.edges, label: previous.label }, ...s.future],
          nodes: previous.nodes,
          edges: previous.edges,
        }
      }),

    redo: () =>
      set((s) => {
        const next = s.future[0]
        if (!next) return s
        return {
          past: [...s.past, { nodes: s.nodes, edges: s.edges, label: next.label }],
          future: s.future.slice(1),
          nodes: next.nodes,
          edges: next.edges,
        }
      }),

    canUndo: () => get().past.length > 0,
    canRedo: () => get().future.length > 0,

    isDirty: () => {
      const s = get()
      if (!s.base) return false
      return !sameGraph(s.base, { nodes: s.nodes, edges: s.edges, notes: EMPTY.notes })
    },
  }
})

/**
 * A person touched this, so it is no longer purely what a model read.
 *
 * §3.7: anything a human corrected carries `human_edited` and no confidence,
 * because a human read it. That correction is the highest-signal input the
 * re-evaluate flow will ever get, so it must not be lost by leaving the
 * extraction's provenance in place.
 */
function humanised(node: NodeView): NodeView['provenance'] {
  return node.provenance === 'human_created' ? 'human_created' : 'human_edited'
}

/** Semantic equality, ignoring anything presentational. */
function sameGraph(a: SemanticGraph, b: SemanticGraph): boolean {
  const key = (g: SemanticGraph) =>
    JSON.stringify({
      nodes: [...g.nodes]
        .sort((x, y) => x.slug.localeCompare(y.slug))
        .map((n) => [n.slug, n.kind, n.name, n.summary ?? '', n.parent ?? '', n.attrs]),
      edges: [...g.edges]
        .sort((x, y) => (x.from + x.to + x.relation).localeCompare(y.from + y.to + y.relation))
        .map((e) => [e.from, e.to, e.relation, e.label ?? '']),
    })
  return key(a) === key(b)
}

/** The working copy as a save payload — slug-keyed, and free of coordinates. */
export function toSavePayload(state: Pick<EditorState, 'nodes' | 'edges'>) {
  return {
    nodes: state.nodes.map((n) => ({
      slug: n.slug,
      kind: n.kind,
      name: n.name,
      ...(n.summary ? { summary: n.summary } : {}),
      ...(n.parent ? { parent: n.parent } : {}),
      ...(n.attrs && Object.keys(n.attrs).length ? { attrs: n.attrs } : {}),
      provenance: n.provenance,
      // Confidence belongs to an extraction. A human-edited node has none
      // (§3.7), and the editor never invents one.
      ...(n.provenance === 'vlm_extracted' && n.confidence !== undefined
        ? { confidence: n.confidence }
        : {}),
    })),
    edges: state.edges.map((e) => ({
      from: e.from,
      to: e.to,
      relation: e.relation,
      ...(e.label ? { label: e.label } : {}),
      ...(e.attrs && Object.keys(e.attrs).length ? { attrs: e.attrs } : {}),
      provenance: e.provenance,
      ...(e.provenance === 'vlm_extracted' && e.confidence !== undefined
        ? { confidence: e.confidence }
        : {}),
    })),
  }
}
