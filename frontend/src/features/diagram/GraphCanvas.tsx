import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  applyNodeChanges,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
  type NodeChange,
  type OnConnect,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { CanvasProps } from './model'
import { autoLayout, mergeLayout } from './layout'
import { GraphContainer, GraphNode, type GraphNodeData } from './GraphNode'
import { EdgeMarkers, GraphEdge, type GraphEdgeData } from './GraphEdge'
import { KIND, canContain } from './vocabulary'
import { useHotkeys, type Hotkey } from '@/lib/useHotkeys'
import { cn } from '@/lib/cn'

const nodeTypes = { kind: GraphNode, container: GraphContainer }
const edgeTypes = { relation: GraphEdge }

/**
 * The graph renderer (§1.1 rule 1).
 *
 * One component, three uses: ingest review, direct editing, and proposal-diff
 * review. Its props are the semantic graph, an optional layout, a mode, and
 * change handlers — and it is consumed only through that interface, so the
 * node vocabulary has exactly one interpretation in the product.
 *
 * `auto` mode stores nothing: dagre runs on render, and the same graph
 * produces the same picture (§3.2).
 */
function Canvas({
  graph,
  layout,
  layoutMode,
  mode,
  diff,
  selected,
  onSelect,
  onNodeMoved,
  onConnect,
  minimap = true,
  className,
}: CanvasProps) {
  const placements = useMemo(
    () => (layoutMode === 'manual' && layout ? mergeLayout(graph, layout) : autoLayout(graph)),
    [graph, layout, layoutMode],
  )

  const computedNodes = useMemo<Node[]>(() => {
    const bySlug = new Map(graph.nodes.map((n) => [n.slug, n]))
    const hasChildren = new Set(
      graph.nodes.filter((n) => n.parent && bySlug.has(n.parent)).map((n) => n.parent as string),
    )

    // Containers must be declared before their children, or React Flow drops
    // the parent link on the first render.
    const ordered = [...graph.nodes].sort((a, b) => {
      const aParent = hasChildren.has(a.slug) ? 0 : 1
      const bParent = hasChildren.has(b.slug) ? 0 : 1
      return aParent - bParent || a.slug.localeCompare(b.slug)
    })

    return ordered.map((node) => {
      const place = placements[node.slug] ?? { x: 0, y: 0, ...KIND[node.kind] }
      const isContainer = hasChildren.has(node.slug) && canContain(node.kind)
      const parent = node.parent && bySlug.has(node.parent) ? node.parent : undefined

      const data: GraphNodeData = {
        node,
        mode,
        ...(diff?.nodes[node.slug] ? { diff: diff.nodes[node.slug] } : {}),
        ...(layout?.[node.id]?.unplaced ? { unplaced: true } : {}),
      }

      return {
        id: node.slug,
        type: isContainer ? 'container' : 'kind',
        // Selection is React Flow's, not ours. Rebuilding this array when the
        // side panel's selection changes reseeds the canvas — and doing that
        // while a pointer is down cancels the drag in progress, which is
        // exactly when a node is selected.
        selected: selected === node.slug,
        position: { x: place.x, y: place.y },
        width: place.w,
        height: place.h,
        data,
        draggable: mode === 'edit',
        selectable: true,
        ...(parent ? { parentId: parent, extent: 'parent' as const } : {}),
        // Stacking order, and it matters for more than looks: containers
        // behind, then edges, then the node boxes on top. With edges above the
        // boxes the edge SVG layer sits under the cursor and swallows the
        // pointer, so nothing is draggable and nothing says why.
        ...(isContainer ? { zIndex: 0 } : { zIndex: 2 }),
        style: { width: place.w, height: place.h },
      }
    })
  }, [graph, placements, mode, diff, layout])

  const edges = useMemo<Edge[]>(() => {
    const slugs = new Set(graph.nodes.map((n) => n.slug))
    return graph.edges
      .filter((e) => slugs.has(e.from) && slugs.has(e.to))
      .map((edge) => {
        const data: GraphEdgeData = {
          relation: edge.relation,
          ...(edge.label ? { label: edge.label } : {}),
          ...(edge.confidence !== undefined ? { confidence: edge.confidence } : {}),
          ...(edge.provenance === 'vlm_extracted' ? { unchecked: true } : {}),
          ...(diff?.edges[edge.id] ? { diff: diff.edges[edge.id] } : {}),
        }
        return {
          id: edge.id,
          source: edge.from,
          target: edge.to,
          type: 'relation',
          data,
          zIndex: 1,
        }
      })
  }, [graph, diff])

  /**
   * React Flow's own view of the nodes.
   *
   * Held here rather than derived on every render, because a fully controlled
   * `nodes` prop that ignores React Flow's position changes leaves a dragged
   * node pinned under the cursor without moving — the drag "works" in the
   * sense that it reports a final position, and feels completely broken.
   *
   * Reseeded whenever the computed placements change, which during a drag they
   * do not: the semantic store is not touched, and the pending coordinate is
   * recorded only when the drag ends.
   */
  const [rfNodes, setRfNodes] = useState<Node[]>(computedNodes)
  const dragging = useRef(false)
  useEffect(() => {
    // Never mid-drag: a reseed replaces the node React Flow is moving.
    if (!dragging.current) setRfNodes(computedNodes)
  }, [computedNodes])

  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      // Applied in every mode so selection and measurement work; only edit
      // mode can produce a position change, since nothing else is draggable.
      setRfNodes((current) => applyNodeChanges(changes, current))

      for (const change of changes) {
        if (change.type === 'position') dragging.current = change.dragging === true
      }

      if (mode !== 'edit' || !onNodeMoved) return
      for (const change of changes) {
        // Only the end of a drag is reported upward. Every intermediate frame
        // would be a layout write, and the first one already flipped the
        // diagram to manual.
        if (change.type === 'position' && change.dragging === false && change.position) {
          onNodeMoved(change.id, change.position.x, change.position.y)
        }
      }
    },
    [mode, onNodeMoved],
  )

  /**
   * Canvas shortcuts (F7).
   *
   * Deliberately only the view controls. `f` to fit, `+`/`-` to zoom, `escape`
   * to deselect — the things somebody reaches for while reading a diagram
   * rather than editing one, so the same bindings work in view, edit and diff
   * mode without one of them meaning something destructive in another.
   */
  const flow = useReactFlow()
  const hotkeys = useMemo<Hotkey[]>(
    () => [
      {
        key: 'f',
        run: () => void flow.fitView({ padding: 0.15, duration: 200 }),
        describe: 'Fit the whole diagram',
      },
      { key: '=', run: () => flow.zoomIn({ duration: 120 }), describe: 'Zoom in' },
      { key: '+', run: () => flow.zoomIn({ duration: 120 }) },
      { key: '-', run: () => flow.zoomOut({ duration: 120 }), describe: 'Zoom out' },
      { key: 'escape', run: () => onSelect?.(undefined), describe: 'Deselect' },
    ],
    [flow, onSelect],
  )
  useHotkeys(hotkeys)

  const handleConnect = useCallback<OnConnect>(
    (connection) => {
      if (mode !== 'edit' || !onConnect) return
      if (!connection.source || !connection.target) return
      onConnect(connection.source, connection.target)
    },
    [mode, onConnect],
  )

  return (
    <div className={cn('relative h-full w-full', className)}>
      <EdgeMarkers />
      <ReactFlow
        nodes={rfNodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={handleNodesChange}
        onConnect={handleConnect}
        onNodeClick={(_, node) => onSelect?.(node.id)}
        onPaneClick={() => onSelect?.(undefined)}
        nodesDraggable={mode === 'edit'}
        nodesConnectable={mode === 'edit'}
        elementsSelectable
        edgesFocusable={false}
        fitView
        fitViewOptions={{ padding: 0.15, maxZoom: 1.1 }}
        minZoom={0.1}
        maxZoom={2.5}
        proOptions={{ hideAttribution: true }}
        className="bg-canvas"
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="opacity-40" />
        <Controls
          showInteractive={false}
          className="!border !border-line !bg-surface [&_button]:!border-line [&_button]:!bg-surface [&_button]:!fill-ink-muted hover:[&_button]:!bg-sunken"
        />
        {minimap ? (
          <MiniMap
            pannable
            zoomable
            className="!border !border-line !bg-surface"
            maskColor="color-mix(in oklch, var(--zp-canvas) 70%, transparent)"
            nodeColor={(n) => {
              const data = n.data as GraphNodeData
              return KIND[data.node.kind]?.hue ?? 'var(--zp-line-strong)'
            }}
          />
        ) : null}
      </ReactFlow>
    </div>
  )
}

export function GraphCanvas(props: CanvasProps) {
  return (
    <ReactFlowProvider>
      <Canvas {...props} />
    </ReactFlowProvider>
  )
}
