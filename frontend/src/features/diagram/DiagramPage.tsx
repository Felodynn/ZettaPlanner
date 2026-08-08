import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Code2, Info, Lock, Network, Pencil } from 'lucide-react'
import { GraphCanvas } from './GraphCanvas'
import { NodePanel } from './NodePanel'
import { Legend } from './Legend'
import { useDiagram, useDiagramYaml } from './queries'
import type { SemanticGraph } from './model'
import { Badge, Loading } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { list } from '@/api/client'
import { useOwnership } from '@/features/plan/ownership'
import { cn } from '@/lib/cn'

export function DiagramPage() {
  const { projectId = '', diagramId = '' } = useParams()
  const diagram = useDiagram(diagramId)
  const [selected, setSelected] = useState<string | undefined>()
  const [view, setView] = useState<'canvas' | 'yaml'>('canvas')
  const [legendOpen, setLegendOpen] = useState(false)
  const yaml = useDiagramYaml(diagramId, view === 'yaml')
  // A diagram's semantic layer is manager-owned in full (§4.2), so a developer
  // is not sent into an editor that will refuse them the session.
  const editPerm = useOwnership(projectId).field('diagram', '*')

  if (diagram.isPending) return <Loading what="this diagram" />
  if (diagram.error) throw diagram.error

  const d = diagram.data
  const graph: SemanticGraph = {
    nodes: list(d.nodes),
    edges: list(d.edges),
    notes: list(d.notes),
  }

  const layout = Object.fromEntries(
    graph.nodes.filter((n) => n.layout).map((n) => [n.id, n.layout!]),
  )

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <Link
          to={`/p/${projectId}/diagrams`}
          className="flex items-center gap-1 text-ink-faint hover:text-ink"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          <Network className="h-3.5 w-3.5" />
        </Link>
        <h1 className="font-semibold text-ink">{d.name}</h1>
        <Badge tone="outline">{d.level}</Badge>
        <Badge
          tone={d.layout_mode === 'manual' ? 'accent' : 'neutral'}
          title={
            d.layout_mode === 'auto'
              ? 'Automatic layout. Nothing is stored; the same graph produces the same picture, computed on render.'
              : 'Somebody has arranged this by hand. Coordinates are stored, and automatic layout will not rearrange it.'
          }
        >
          {d.layout_mode} layout
        </Badge>
        <span className="zp-num text-[11px] text-ink-faint">
          rev {d.revision} · {graph.nodes.length} nodes · {graph.edges.length} edges
        </span>

        <div className="flex-1" />

        <Button
          size="sm"
          variant={legendOpen ? 'subtle' : 'ghost'}
          onClick={() => setLegendOpen((v) => !v)}
        >
          <Info className="h-3.5 w-3.5" />
          Legend
        </Button>

        {editPerm.writable ? (
          <Link to={`/p/${projectId}/diagrams/${diagramId}/edit`}>
            <Button size="sm" variant="primary" title="Takes the edit session for this diagram">
              <Pencil className="h-3.5 w-3.5" />
              Edit
            </Button>
          </Link>
        ) : (
          <Button size="sm" variant="secondary" disabled title={editPerm.why}>
            <Lock className="h-3.5 w-3.5" />
            Edit
          </Button>
        )}

        <div className="flex rounded-md border border-line">
          <button
            type="button"
            onClick={() => setView('canvas')}
            className={cn(
              'rounded-l-md px-2.5 py-1 text-xs transition-colors',
              view === 'canvas' ? 'bg-accent-soft text-accent-ink' : 'text-ink-muted hover:bg-sunken',
            )}
          >
            Canvas
          </button>
          <button
            type="button"
            onClick={() => setView('yaml')}
            className={cn(
              'flex items-center gap-1 rounded-r-md border-l border-line px-2.5 py-1 text-xs transition-colors',
              view === 'yaml' ? 'bg-accent-soft text-accent-ink' : 'text-ink-muted hover:bg-sunken',
            )}
          >
            <Code2 className="h-3 w-3" />
            YAML
          </button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <div className="relative min-w-0 flex-1">
          {view === 'canvas' ? (
            <GraphCanvas
              graph={graph}
              layout={layout}
              layoutMode={d.layout_mode}
              mode="view"
              selected={selected}
              onSelect={setSelected}
            />
          ) : (
            <div className="h-full overflow-auto bg-sunken zp-scroll">
              <div className="border-b border-line bg-surface px-4 py-2">
                <p className="text-ink-muted">
                  {/* §6.2: one serializer, two consumers — the model context and
                      the git projection. Same bytes. */}
                  The canonical serialization: byte-for-byte what a model is shown and what the git
                  projection writes. It carries no coordinates, structurally (D5).
                </p>
              </div>
              {yaml.isPending ? (
                <Loading what="the serialization" />
              ) : yaml.error ? (
                <p className="px-4 py-6 text-ink-muted">{yaml.error.message}</p>
              ) : (
                <pre className="px-4 py-3 font-mono text-xs leading-relaxed text-ink">
                  {yaml.data}
                </pre>
              )}
            </div>
          )}

          {legendOpen ? <Legend onClose={() => setLegendOpen(false)} /> : null}
        </div>

        {selected && view === 'canvas' ? (
          <NodePanel
            projectId={projectId}
            graph={graph}
            slug={selected}
            onClose={() => setSelected(undefined)}
          />
        ) : null}
      </div>
    </div>
  )
}
