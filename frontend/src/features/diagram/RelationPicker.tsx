import { ArrowRight } from 'lucide-react'
import { RELATION_VALUES, type Relation } from '@/api/vocabulary'
import { RELATION } from './vocabulary'
import { Button } from '@/components/ui/Button'

/**
 * Which relation, asked at the moment the line is drawn.
 *
 * Not defaulted. `data_flow` exists precisely for "nothing more specific is
 * true", so quietly picking it for every dragged line would fill a diagram
 * with the one relation that means the drawer did not say — and a graph of
 * `data_flow` edges answers none of the questions the typed graph exists for.
 */
export function RelationPicker({
  from,
  to,
  onPick,
  onCancel,
}: {
  from: string
  to: string
  onPick: (relation: Relation) => void
  onCancel: () => void
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-canvas/70 p-6">
      <div className="zp-shadow-pop w-full max-w-md rounded-card border border-line bg-raised">
        <div className="border-b border-line px-4 py-3">
          <p className="font-semibold text-ink">What is this relation?</p>
          <p className="mt-1 flex flex-wrap items-center gap-1.5 text-ink-muted">
            <span className="font-medium text-ink">{from}</span>
            <ArrowRight className="h-3.5 w-3.5" />
            <span className="font-medium text-ink">{to}</span>
          </p>
        </div>

        <ul className="max-h-96 overflow-y-auto p-2 zp-scroll">
          {RELATION_VALUES.map((relation) => {
            const spec = RELATION[relation]
            return (
              <li key={relation}>
                <button
                  type="button"
                  onClick={() => onPick(relation)}
                  className="flex w-full items-start gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-sunken"
                >
                  <svg width="34" height="14" className="mt-1 shrink-0" aria-hidden>
                    <line
                      x1="1"
                      y1="7"
                      x2="26"
                      y2="7"
                      stroke={spec.hue}
                      strokeWidth={spec.width}
                      {...(spec.dash ? { strokeDasharray: spec.dash } : {})}
                    />
                    {spec.head === 'filled' ? (
                      <path d="M 26 3 L 33 7 L 26 11 z" fill={spec.hue} />
                    ) : spec.head === 'open' ? (
                      <path d="M 27 3 L 33 7 L 27 11" fill="none" stroke={spec.hue} strokeWidth="1.5" />
                    ) : null}
                  </svg>
                  <span className="min-w-0">
                    <span className="block font-medium text-ink">{spec.label}</span>
                    <span className="block text-[11px] leading-snug text-ink-faint">
                      {spec.means}
                    </span>
                  </span>
                </button>
              </li>
            )
          })}
        </ul>

        <div className="flex justify-end border-t border-line px-4 py-2.5">
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </div>
    </div>
  )
}
