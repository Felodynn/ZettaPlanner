import { X } from 'lucide-react'
import { KIND, RELATION } from './vocabulary'
import { NODE_KIND_VALUES, RELATION_VALUES } from '@/api/vocabulary'
import { Button } from '@/components/ui/Button'

/**
 * The closed vocabulary, drawn.
 *
 * Worth a panel rather than a docs page: the vocabulary is fixed (D6), so this
 * is a complete reference rather than a sample, and "which of these is a
 * queue" is the question a new reader actually has.
 *
 * The lists come from the generated vocabulary, so a kind added to the backend
 * appears here without anyone remembering to add it.
 */
export function Legend({ onClose }: { onClose: () => void }) {
  return (
    <div className="zp-shadow-pop absolute top-3 right-3 bottom-3 z-10 flex w-72 flex-col overflow-hidden rounded-card border border-line bg-surface">
      <div className="flex items-center justify-between border-b border-line px-3 py-2">
        <p className="font-semibold text-ink">Vocabulary</p>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close the legend">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2 zp-scroll">
        <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
          Nodes ({NODE_KIND_VALUES.length})
        </p>
        <ul className="mt-1.5 flex flex-col gap-1.5">
          {NODE_KIND_VALUES.map((kind) => {
            const spec = KIND[kind]
            return (
              <li key={kind} className="flex items-start gap-2">
                <span
                  className="mt-0.5 flex h-6 w-9 shrink-0 items-center justify-center bg-surface"
                  style={{
                    borderColor: spec.hue,
                    ...(spec.clip ? { clipPath: spec.clip } : {}),
                  }}
                >
                  <span
                    className={`flex h-full w-full items-center justify-center ${spec.shape}`}
                    style={{ borderColor: spec.hue }}
                  >
                    <spec.Icon className="h-3 w-3" style={{ color: spec.hue }} />
                  </span>
                </span>
                <span className="min-w-0">
                  <span className="block text-xs font-medium text-ink">{spec.label}</span>
                  <span className="block text-[11px] leading-snug text-ink-faint">
                    {spec.means}
                  </span>
                </span>
              </li>
            )
          })}
        </ul>

        <p className="mt-4 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
          Relations ({RELATION_VALUES.length})
        </p>
        <ul className="mt-1.5 flex flex-col gap-1.5">
          {RELATION_VALUES.map((relation) => {
            const spec = RELATION[relation]
            return (
              <li key={relation} className="flex items-start gap-2">
                <svg width="36" height="14" className="mt-0.5 shrink-0" aria-hidden>
                  <line
                    x1="2"
                    y1="7"
                    x2="28"
                    y2="7"
                    stroke={spec.hue}
                    strokeWidth={spec.width}
                    {...(spec.dash ? { strokeDasharray: spec.dash } : {})}
                  />
                  {spec.head === 'filled' ? (
                    <path d="M 28 3 L 35 7 L 28 11 z" fill={spec.hue} />
                  ) : spec.head === 'open' ? (
                    <path
                      d="M 29 3 L 35 7 L 29 11"
                      fill="none"
                      stroke={spec.hue}
                      strokeWidth="1.5"
                    />
                  ) : null}
                </svg>
                <span className="min-w-0">
                  <span className="block text-xs font-medium text-ink">{spec.label}</span>
                  <span className="block text-[11px] leading-snug text-ink-faint">
                    {spec.means}
                  </span>
                </span>
              </li>
            )
          })}
        </ul>

        <p className="mt-4 border-t border-line pt-2 text-[11px] leading-snug text-ink-faint">
          This vocabulary is closed (D6). A kind that does not exist here is a schema migration and
          a prompt version bump, not a setting.
        </p>
      </div>
    </div>
  )
}
