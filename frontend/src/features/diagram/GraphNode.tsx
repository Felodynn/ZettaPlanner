import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { CircleHelp, MapPin } from 'lucide-react'
import type { NodeView, DiffMark, CanvasMode } from './model'
import { isLowConfidence, isUnchecked } from './model'
import { KIND } from './vocabulary'
import { cn } from '@/lib/cn'

export interface GraphNodeData extends Record<string, unknown> {
  node: NodeView
  mode: CanvasMode
  diff?: DiffMark
  unplaced?: boolean
}

const DIFF_RING: Record<DiffMark, string> = {
  added: 'ring-2 ring-good',
  removed: 'ring-2 ring-danger opacity-60',
  changed: 'ring-2 ring-warn',
  unchanged: '',
}

/**
 * The diff mark as a glyph, not only as a colour.
 *
 * The same rule the node kinds follow (F3): colour is the third signal, never
 * the first. It matters more here than there, because the ring competes with
 * the border colour that already carries provenance — an amber "unchecked"
 * border beside an amber "changed" ring is two different facts wearing the
 * same paint, and the reviewer deciding whether to approve a deletion is
 * exactly the person who must not have to guess.
 */
const DIFF_GLYPH: Record<DiffMark, { mark: string; cls: string; title: string } | null> = {
  added: {
    mark: '+',
    cls: 'bg-good text-white',
    title: 'This proposal adds this element. It is not on the diagram yet.',
  },
  changed: {
    mark: '~',
    cls: 'bg-warn text-white',
    title: 'This proposal changes this element. Select it to see which fields.',
  },
  removed: {
    mark: '−',
    cls: 'bg-danger text-white',
    title: 'This proposal removes this element. It is still drawn — you cannot judge a deletion you cannot see.',
  },
  unchanged: null,
}

/**
 * One node, in whatever mode the canvas is in.
 *
 * The diff treatment is a ring rather than a fill, so an added `datastore`
 * still reads as a datastore. Recolouring the body would make diff mode a
 * fourth vocabulary that has to be learned separately (§1.1 rule 1).
 */
export const GraphNode = memo(function GraphNode({ data, selected }: NodeProps) {
  const { node, mode, diff, unplaced } = data as GraphNodeData
  const spec = KIND[node.kind]
  const unchecked = isUnchecked(node)
  const lowConfidence = isLowConfidence(node.confidence)
  const connectable = mode === 'edit' && node.kind !== 'note'

  return (
    <div
      // The kind, on the element rather than only in the caption: a `note`
      // renders no caption, and a check that reads the vocabulary off the
      // rendered text cannot see one.
      data-kind={node.kind}
      data-provenance={node.provenance}
      className={cn(
        'group relative flex h-full w-full items-center gap-2 bg-surface px-2.5 text-ink',
        'transition-shadow',
        spec.shape,
        diff && diff !== 'unchanged' ? DIFF_RING[diff] : '',
        selected ? 'zp-shadow-pop ring-2 ring-accent' : 'zp-shadow',
        // An element a model read off a photograph that nobody has checked
        // (frontend-notes §7). Marked in every mode, not only ingest review:
        // a hand-corrected diagram still contains extracted elements.
        unchecked ? 'border-dashed' : '',
        node.kind === 'note' ? 'items-start pt-2 text-xs leading-snug' : '',
      )}
      style={{
        borderColor: unchecked ? 'var(--zp-warn)' : spec.hue,
        ...(spec.clip ? { clipPath: spec.clip } : {}),
        ...(node.kind === 'note' ? { background: 'var(--zp-warn-soft)' } : {}),
      }}
      title={node.summary ?? spec.means}
    >
      {node.kind !== 'note' ? (
        <spec.Icon className="h-4 w-4 shrink-0" style={{ color: spec.hue }} aria-hidden />
      ) : null}

      <div className="min-w-0 flex-1">
        <div
          className={cn(
            'truncate font-medium',
            node.kind === 'note' ? 'text-xs font-normal whitespace-normal' : 'text-[13px]',
          )}
        >
          {node.name}
        </div>
        {node.kind !== 'note' ? (
          <div className="truncate text-[10px] text-ink-faint">{spec.label.toLowerCase()}</div>
        ) : null}
      </div>

      {/* The diff mark, on the opposite corner from the confidence badge so
          the two never overlap on an extracted element somebody has proposed
          changing. */}
      {diff && DIFF_GLYPH[diff] ? (
        <span
          className={cn(
            'zp-num absolute -top-2 -left-1.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[11px] font-bold',
            DIFF_GLYPH[diff]!.cls,
          )}
          title={DIFF_GLYPH[diff]!.title}
        >
          {DIFF_GLYPH[diff]!.mark}
        </span>
      ) : null}

      {/* Badges sit outside the body so a clip-path cannot cut them off. */}
      <div className="absolute -top-2 -right-1.5 flex items-center gap-1">
        {unplaced ? (
          <span
            className="flex h-4 w-4 items-center justify-center rounded-full bg-info text-white"
            title="Placed automatically near its neighbours because it arrived without coordinates. The flag clears when you move it."
          >
            <MapPin className="h-2.5 w-2.5" />
          </span>
        ) : null}
        {lowConfidence ? (
          <span
            className="zp-num flex h-4 items-center rounded-full bg-warn px-1 text-[9px] font-bold text-white"
            title={`The vision model was ${Math.round((node.confidence ?? 0) * 100)}% confident reading this. A low reading is stored deliberately rather than replaced with a plausible guess.`}
          >
            {Math.round((node.confidence ?? 0) * 100)}
          </span>
        ) : unchecked ? (
          <span
            className="flex h-4 w-4 items-center justify-center rounded-full bg-warn text-white"
            title="Read off a drawing by the vision model. Nobody has checked it."
          >
            <CircleHelp className="h-2.5 w-2.5" />
          </span>
        ) : null}
      </div>

      {/* Notes participate in no relations, so they offer no handles at all. */}
      {node.kind !== 'note' ? (
        <>
          <Handle
            type="target"
            position={Position.Left}
            isConnectable={connectable}
            className={cn('!h-2 !w-2 !border-0 !bg-ink-faint', !connectable && '!opacity-0')}
          />
          <Handle
            type="source"
            position={Position.Right}
            isConnectable={connectable}
            className={cn('!h-2 !w-2 !border-0 !bg-ink-faint', !connectable && '!opacity-0')}
          />
        </>
      ) : null}
    </div>
  )
})

/**
 * A boundary or system that contains other nodes.
 *
 * Drawn behind its children with the label in the header strip, so a click
 * anywhere in the empty area selects the container rather than falling through
 * to the canvas.
 */
export const GraphContainer = memo(function GraphContainer({ data, selected }: NodeProps) {
  const { node, mode, diff } = data as GraphNodeData
  const spec = KIND[node.kind]
  const connectable = mode === 'edit'

  return (
    <div
      data-kind={node.kind}
      data-provenance={node.provenance}
      data-container="true"
      className={cn(
        'h-full w-full',
        spec.shape,
        diff && diff !== 'unchanged' ? DIFF_RING[diff] : '',
        selected ? 'ring-2 ring-accent' : '',
      )}
      style={{
        borderColor: spec.hue,
        background: 'color-mix(in oklch, var(--zp-surface) 60%, transparent)',
      }}
      title={node.summary ?? spec.means}
    >
      <div className="flex items-center gap-1.5 px-2.5 pt-1.5">
        <spec.Icon className="h-3.5 w-3.5 shrink-0" style={{ color: spec.hue }} aria-hidden />
        <span className="truncate text-[12px] font-semibold" style={{ color: spec.hue }}>
          {node.name}
        </span>
        <span className="text-[10px] text-ink-faint">{spec.label.toLowerCase()}</span>
      </div>

      <Handle
        type="target"
        position={Position.Left}
        isConnectable={connectable}
        className={cn('!h-2 !w-2 !border-0 !bg-ink-faint', !connectable && '!opacity-0')}
      />
      <Handle
        type="source"
        position={Position.Right}
        isConnectable={connectable}
        className={cn('!h-2 !w-2 !border-0 !bg-ink-faint', !connectable && '!opacity-0')}
      />
    </div>
  )
})
