import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { Button } from './Button'
import { cn } from '@/lib/cn'

/**
 * A modal, with the three behaviours people expect and hand-rolled overlays
 * forget: Escape closes it, the backdrop closes it, and the first field takes
 * focus so a form is typeable without reaching for the mouse.
 *
 * Deliberately not Radix's Dialog, which is already a dependency: this project
 * has four hand-rolled overlays that predate it and one shared primitive is
 * enough. Introducing a second modal system to fix a focus bug would leave the
 * app with three.
 */
export function Dialog({
  title,
  description,
  onClose,
  footer,
  children,
  wide,
}: {
  title: string
  description?: ReactNode
  onClose: () => void
  footer?: ReactNode
  children: ReactNode
  wide?: boolean
}) {
  const panel = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    // Capture, so Escape closes the dialog rather than whatever hotkey the
    // screen underneath bound it to.
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])

  useEffect(() => {
    const first = panel.current?.querySelector<HTMLElement>(
      'input:not([type=hidden]), textarea, select',
    )
    first?.focus()
  }, [])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-canvas/70 p-6"
      onMouseDown={(e) => {
        // Only a click that both starts and ends on the backdrop closes. A
        // drag that began inside the panel — selecting text, then releasing
        // outside — must not throw the form away.
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          'zp-shadow-pop flex max-h-[85vh] w-full flex-col rounded-card border border-line bg-raised',
          wide ? 'max-w-3xl' : 'max-w-lg',
        )}
      >
        <div className="flex items-start gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1">
            <p className="font-semibold text-ink">{title}</p>
            {description ? <div className="mt-0.5 text-ink-muted">{description}</div> : null}
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="min-h-0 flex-1 overflow-auto px-4 py-3 zp-scroll">{children}</div>

        {footer ? (
          <div className="flex items-center justify-end gap-2 border-t border-line px-4 py-3">
            {footer}
          </div>
        ) : null}
      </div>
    </div>
  )
}

/** A labelled field, the shape every form in here is made of. */
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: ReactNode
  children: ReactNode
}) {
  return (
    <label className="mb-3 flex flex-col gap-1 last:mb-0">
      <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
        {label}
      </span>
      {children}
      {hint ? <span className="text-[11px] leading-snug text-ink-faint">{hint}</span> : null}
    </label>
  )
}
