import { memo } from 'react'
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react'
import type { DiffMark } from './model'
import { RELATION } from './vocabulary'
import type { Relation } from '@/api/vocabulary'

export interface GraphEdgeData extends Record<string, unknown> {
  relation: Relation
  label?: string
  confidence?: number
  unchecked?: boolean
  diff?: DiffMark
}

/**
 * One relation.
 *
 * Every relation differs from its nearest neighbour in more than colour:
 * `reads_from` and `writes_to` share a hue but not a dash pattern or a weight,
 * and `publishes_to`/`subscribes_to` differ in arrowhead as well. Tracing
 * dataflow should not require hovering.
 */
export const GraphEdge = memo(function GraphEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  selected,
}: EdgeProps) {
  const d = (data ?? {}) as GraphEdgeData
  const spec = RELATION[d.relation] ?? RELATION.data_flow

  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 8,
  })

  const removed = d.diff === 'removed'
  const added = d.diff === 'added'
  const changed = d.diff === 'changed'

  const stroke = removed
    ? 'var(--zp-danger)'
    : added
      ? 'var(--zp-good)'
      : changed
        ? 'var(--zp-warn)'
        : spec.hue

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={spec.head === 'none' ? undefined : `url(#zp-arrow-${spec.head}-${d.relation})`}
        style={{
          stroke,
          strokeWidth: selected ? spec.width + 1.2 : spec.width,
          // An unchecked edge is drawn faintly as well as dashed: it is a line
          // a model thought it saw.
          strokeOpacity: removed ? 0.7 : d.unchecked ? 0.55 : 1,
          ...(spec.dash ? { strokeDasharray: spec.dash } : {}),
        }}
      />

      {d.label || changed || removed || added ? (
        <EdgeLabelRenderer>
          <div
            className="pointer-events-none absolute rounded border border-line bg-surface px-1 py-px text-[10px] text-ink-muted"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {d.label ?? spec.label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  )
})

/**
 * Arrowhead definitions, one per relation.
 *
 * React Flow's built-in markers take a single colour for the whole canvas, and
 * a diagram whose arrowheads are all one colour while its lines are nine
 * undoes half the distinction the lines were carrying.
 */
export function EdgeMarkers() {
  return (
    <svg className="absolute h-0 w-0" aria-hidden>
      <defs>
        {Object.entries(RELATION).map(([relation, spec]) =>
          spec.head === 'none' ? null : (
            <marker
              key={relation}
              id={`zp-arrow-${spec.head}-${relation}`}
              viewBox="0 0 12 12"
              refX="10"
              refY="6"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
              markerUnits="userSpaceOnUse"
            >
              {spec.head === 'filled' ? (
                <path d="M 1 1 L 11 6 L 1 11 z" fill={spec.hue} />
              ) : (
                <path
                  d="M 2 1 L 11 6 L 2 11"
                  fill="none"
                  stroke={spec.hue}
                  strokeWidth="1.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              )}
            </marker>
          ),
        )}
      </defs>
    </svg>
  )
}
