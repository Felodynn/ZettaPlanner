import { X } from 'lucide-react'
import type { EdgeView, NodeView, SemanticGraph } from './model'
import { isLowConfidence } from './model'
import { KIND, RELATION } from './vocabulary'
import { Badge } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { useEntity } from './queries'
import { Link } from 'react-router-dom'

/**
 * What is selected, and what it is connected to.
 *
 * Provenance and confidence are shown as facts rather than footnotes
 * (frontend-notes §7): for `vlm_extracted` the difference between "the system
 * knows this" and "a model read it off a photograph and nobody has checked" is
 * the whole point of storing it.
 */
export function NodePanel({
  projectId,
  graph,
  slug,
  onClose,
}: {
  projectId: string
  graph: SemanticGraph
  slug: string
  onClose: () => void
}) {
  const node = graph.nodes.find((n) => n.slug === slug)
  const entity = useEntity(node?.entity_id)

  if (!node) return null
  const spec = KIND[node.kind]

  const outgoing = graph.edges.filter((e) => e.from === slug)
  const incoming = graph.edges.filter((e) => e.to === slug)
  const notes = graph.notes.filter((n) => n.subject_slug === slug)

  return (
    <aside className="flex w-80 shrink-0 flex-col overflow-y-auto border-l border-line bg-surface zp-scroll">
      <div className="flex items-start gap-2 border-b border-line px-3 py-2.5">
        <spec.Icon className="mt-0.5 h-4 w-4 shrink-0" style={{ color: spec.hue }} />
        <div className="min-w-0 flex-1">
          <p className="wrap-break-word font-semibold text-ink">{node.name}</p>
          <p className="font-mono text-[11px] text-ink-faint">{node.slug}</p>
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="flex flex-col gap-3 px-3 py-3">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge tone="outline" title={spec.means}>
            {spec.label.toLowerCase()}
          </Badge>
          <ProvenanceChip node={node} />
        </div>

        {node.summary ? <p className="text-ink-muted">{node.summary}</p> : null}

        {node.parent ? (
          <Row label="Inside">
            <span className="font-mono text-xs">{node.parent}</span>
          </Row>
        ) : null}

        {Object.keys(node.attrs ?? {}).length > 0 ? (
          <div>
            <Label>Attributes</Label>
            <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
              {Object.entries(node.attrs).map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="font-mono text-[11px] text-ink-faint">{k}</dt>
                  <dd className="wrap-break-word text-xs text-ink">{String(v)}</dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}

        <EdgeGroup title="Outgoing" edges={outgoing} graph={graph} direction="to" />
        <EdgeGroup title="Incoming" edges={incoming} graph={graph} direction="from" />

        {notes.length > 0 ? (
          <div>
            <Label>Notes</Label>
            <ul className="mt-1 flex flex-col gap-1.5">
              {notes.map((n) => (
                <li
                  key={n.id}
                  className="rounded border border-warn/30 bg-warn-soft px-2 py-1.5 text-xs"
                >
                  <Badge tone="warn">{n.kind}</Badge>
                  <p className="mt-1 text-ink">{n.body}</p>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {/* One entity may be placed in several diagrams; this is what makes
            "what depends on the token store" answerable across the project
            rather than per picture (§3.4). */}
        {entity.data && (entity.data.placements ?? []).length > 1 ? (
          <div>
            <Label>Also appears in</Label>
            <ul className="mt-1 flex flex-col gap-0.5">
              {(entity.data.placements ?? []).map((p) => (
                <li key={p.diagram_id}>
                  <Link
                    to={`/p/${projectId}/diagrams/${p.diagram_id}`}
                    className="text-xs text-accent-ink hover:underline"
                  >
                    {p.diagram_name}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </aside>
  )
}

function Label({ children }: { children: React.ReactNode }) {
  return (
    <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">{children}</p>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-2">
      <span className="text-ink-faint">{label}</span>
      <span className="min-w-0 text-ink">{children}</span>
    </div>
  )
}

function ProvenanceChip({ node }: { node: NodeView }) {
  if (node.provenance === 'vlm_extracted') {
    const pct = node.confidence !== undefined ? Math.round(node.confidence * 100) : undefined
    return (
      <Badge
        tone="warn"
        title="A vision model read this off a drawing. Nobody has checked it. Correcting it is the highest-signal input the re-evaluate flow will ever receive."
      >
        unchecked extraction{pct !== undefined ? ` · ${pct}%` : ''}
      </Badge>
    )
  }
  if (node.provenance.startsWith('agent')) {
    return <Badge tone="info">{node.provenance.replace(/_/g, ' ')}</Badge>
  }
  if (node.provenance === 'human_edited') {
    return <Badge tone="good">corrected by a person</Badge>
  }
  return <Badge tone="neutral">{node.provenance.replace(/_/g, ' ')}</Badge>
}

function EdgeGroup({
  title,
  edges,
  graph,
  direction,
}: {
  title: string
  edges: EdgeView[]
  graph: SemanticGraph
  /** Which end of the edge names the other node. */
  direction: 'to' | 'from'
}) {
  if (edges.length === 0) return null
  const bySlug = new Map(graph.nodes.map((n) => [n.slug, n]))

  return (
    <div>
      <Label>
        {title} ({edges.length})
      </Label>
      <ul className="mt-1 flex flex-col gap-1">
        {edges.map((e) => {
          const otherSlug = direction === 'to' ? e.to : e.from
          const other = bySlug.get(otherSlug)
          const spec = RELATION[e.relation]
          return (
            <li key={e.id} className="flex items-baseline gap-1.5 text-xs">
              <span
                className="shrink-0 font-medium"
                style={{ color: spec.hue }}
                title={spec.means}
              >
                {spec.label}
              </span>
              <span className="min-w-0 truncate text-ink">{other?.name ?? otherSlug}</span>
              {e.label ? <span className="shrink-0 text-ink-faint">({e.label})</span> : null}
              {isLowConfidence(e.confidence) ? (
                <span className="zp-num shrink-0 text-[10px] text-warn-ink">
                  {Math.round((e.confidence ?? 0) * 100)}%
                </span>
              ) : null}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
