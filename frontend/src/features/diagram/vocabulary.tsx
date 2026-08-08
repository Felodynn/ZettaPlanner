import {
  Boxes,
  Clock,
  Cloud,
  Cpu,
  Database,
  Layers,
  ListEnd,
  Plug,
  Server,
  StickyNote,
  User,
  type LucideIcon,
} from 'lucide-react'
import type { NodeKind, Relation } from '@/api/vocabulary'

/**
 * How the closed vocabulary looks (D6, §3.3).
 *
 * F3: node kinds must be distinguishable **at a glance, by shape and
 * iconography, not colour alone**. A diagram where `service` and `component`
 * look alike is a diagram nobody trusts — and colour alone fails for the
 * ~8% of men with a red/green deficiency, on a projector, and in the
 * grayscale printout somebody brings to the review.
 *
 * So every kind differs in at least two of: silhouette, border treatment, and
 * icon. Colour is the third signal, never the first.
 */

export interface KindSpec {
  label: string
  Icon: LucideIcon
  /** Default footprint, and what dagre lays out against. */
  w: number
  h: number
  /** Outline treatment applied to the node body. */
  shape: string
  /** A CSS clip-path when the silhouette itself carries the meaning. */
  clip?: string
  /** Hue token, used for the icon and the border tint. */
  hue: string
  /** One line explaining what the kind means, for the palette and the panel. */
  means: string
}

export const KIND: Record<NodeKind, KindSpec> = {
  system: {
    label: 'System',
    Icon: Layers,
    w: 200,
    h: 62,
    // Double border: the outermost box in a context diagram.
    shape: 'rounded-xl border-2 ring-1 ring-offset-2 ring-offset-canvas',
    hue: 'oklch(62% 0.14 275)',
    means: 'A whole system under discussion',
  },
  service: {
    label: 'Service',
    Icon: Server,
    w: 190,
    h: 58,
    // Square-shouldered with a heavy left bar — the workhorse shape.
    shape: 'rounded-md border border-l-4',
    hue: 'oklch(62% 0.15 248)',
    means: 'An independently deployable process',
  },
  component: {
    label: 'Component',
    Icon: Cpu,
    w: 170,
    h: 54,
    // Tighter corners than a service, and no left bar: a part, not a process.
    shape: 'rounded-sm border border-dotted border-2',
    hue: 'oklch(64% 0.11 205)',
    means: 'A module inside a service. Not independently deployable',
  },
  datastore: {
    label: 'Datastore',
    Icon: Database,
    w: 170,
    h: 62,
    // A cylinder read from the side: flat sides, heavily rounded top and
    // bottom. Nothing else in the vocabulary is shaped like this.
    shape: 'rounded-[50%/22%] border-2',
    hue: 'oklch(66% 0.13 155)',
    means: 'A database, cache, object store, or file store',
  },
  queue: {
    label: 'Queue',
    Icon: ListEnd,
    w: 176,
    h: 50,
    // A stadium — ends fully round, sides flat. Reads as a pipe.
    shape: 'rounded-full border-2 border-dashed',
    hue: 'oklch(70% 0.14 86)',
    means: 'A message broker, topic, or stream',
  },
  external_system: {
    label: 'External',
    Icon: Cloud,
    w: 180,
    h: 56,
    // Dashed all round: outside the team's control.
    shape: 'rounded-lg border-2 border-dashed',
    hue: 'oklch(64% 0.02 250)',
    means: "Something outside the team's control",
  },
  actor: {
    label: 'Actor',
    Icon: User,
    w: 140,
    h: 56,
    // Pill, and the only kind drawn with a person.
    shape: 'rounded-full border-2',
    hue: 'oklch(66% 0.15 305)',
    means: 'A human role or class of user',
  },
  api: {
    label: 'API',
    Icon: Plug,
    w: 176,
    h: 56,
    // Chamfered corners: a surface rather than a box.
    shape: 'border-2',
    clip: 'polygon(12px 0, 100% 0, 100% calc(100% - 12px), calc(100% - 12px) 100%, 0 100%, 0 12px)',
    hue: 'oklch(66% 0.14 195)',
    means: 'A named interface surface, when the interface is the subject',
  },
  job: {
    label: 'Job',
    Icon: Clock,
    w: 176,
    h: 54,
    // Notched top edge, suggesting something torn off a schedule.
    shape: 'rounded-md border-2 border-t-4 border-t-dashed',
    hue: 'oklch(68% 0.13 45)',
    means: 'A scheduled or triggered batch process',
  },
  boundary: {
    label: 'Boundary',
    Icon: Boxes,
    w: 320,
    h: 200,
    shape: 'rounded-xl border-2 border-dashed',
    hue: 'oklch(60% 0.05 250)',
    means: 'A grouping container: trust zone, deployment unit, ownership',
  },
  note: {
    label: 'Note',
    Icon: StickyNote,
    w: 210,
    h: 84,
    // Folded corner. Carries no relations, and should not look like it could.
    shape: 'border',
    clip: 'polygon(0 0, 100% 0, 100% calc(100% - 18px), calc(100% - 18px) 100%, 0 100%)',
    hue: 'oklch(72% 0.12 86)',
    means: 'Free text pinned to the diagram. Carries no relations',
  },
}

export interface RelationSpec {
  label: string
  /** Dash pattern, or undefined for a solid line. */
  dash?: string
  /** Arrowhead style: a filled head for a call, an open one for a dependency. */
  head: 'filled' | 'open' | 'none'
  hue: string
  width: number
  means: string
}

/**
 * The nine relations.
 *
 * Read/write and publish/subscribe are the pairs most often confused, so they
 * are separated on two axes at once: direction of the arrow *and* dash
 * pattern. A reader tracing dataflow should never have to hover to find out
 * which way the bytes go.
 */
export const RELATION: Record<Relation, RelationSpec> = {
  calls: {
    label: 'calls',
    head: 'filled',
    hue: 'oklch(62% 0.15 248)',
    width: 1.6,
    means: 'Synchronous request/response',
  },
  depends_on: {
    label: 'depends on',
    dash: '6 4',
    head: 'open',
    hue: 'oklch(60% 0.02 250)',
    width: 1.4,
    means: 'Needs to exist or be available. Non-specific',
  },
  reads_from: {
    label: 'reads from',
    dash: '2 3',
    head: 'filled',
    hue: 'oklch(62% 0.13 155)',
    width: 1.5,
    means: 'Reads data',
  },
  writes_to: {
    label: 'writes to',
    head: 'filled',
    hue: 'oklch(58% 0.15 155)',
    width: 2.1,
    means: 'Writes data',
  },
  publishes_to: {
    label: 'publishes to',
    head: 'filled',
    hue: 'oklch(66% 0.15 86)',
    width: 2,
    means: 'Emits to a queue or topic',
  },
  subscribes_to: {
    label: 'subscribes to',
    dash: '2 3',
    head: 'open',
    hue: 'oklch(66% 0.15 86)',
    width: 1.6,
    means: 'Consumes from a queue or topic',
  },
  contains: {
    label: 'contains',
    dash: '1 5',
    head: 'none',
    hue: 'oklch(60% 0.04 250)',
    width: 1.2,
    means: 'Structural containment. Only from a boundary or a system',
  },
  deploys_to: {
    label: 'deploys to',
    dash: '8 3 2 3',
    head: 'open',
    hue: 'oklch(62% 0.12 305)',
    width: 1.4,
    means: 'Runtime placement',
  },
  data_flow: {
    label: 'data flow',
    dash: '4 4',
    head: 'filled',
    hue: 'oklch(64% 0.1 195)',
    width: 1.5,
    means: 'Generic flow when nothing more specific is true',
  },
}

/** Kinds that may contain other nodes (§3.3). */
export const CONTAINER_KINDS: readonly NodeKind[] = ['boundary', 'system']

export function canContain(kind: NodeKind): boolean {
  return CONTAINER_KINDS.includes(kind)
}
