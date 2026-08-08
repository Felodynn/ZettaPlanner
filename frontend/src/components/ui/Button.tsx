import { forwardRef, type ButtonHTMLAttributes } from 'react'
import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'subtle'
type Size = 'sm' | 'md' | 'lg' | 'icon'

/**
 * Hover has to move *towards* the eye in both modes.
 *
 * `secondary` used to hover to `sunken`, which is a light-mode assumption: on
 * white, sunken is a step down and reads as pressed, but the dark palette is
 * built the other way up — surface is 13.5% lightness and sunken is 7% — so on
 * black the hover made the button recede into the page and the affordance
 * vanished. Hovering to `accent-soft` moves in the same direction in both
 * palettes, and it picks up whichever accent the person chose rather than
 * hard-coding a colour that fights half of them.
 *
 * `disabled:hover:` is not needed: a disabled button receives no hover in any
 * browser that honours `pointer-events`, and the variants below keep their
 * disabled treatment last so it wins the cascade.
 */
const VARIANTS: Record<Variant, string> = {
  // `on-accent` rather than white: the accent is user-chosen and gold is the
  // default, so a hard-coded white label would be unreadable out of the box.
  primary: 'bg-accent text-on-accent hover:brightness-[0.94] disabled:opacity-50',
  secondary:
    'bg-surface text-ink border border-line-strong ' +
    'hover:border-accent-line hover:bg-accent-soft hover:text-accent-ink ' +
    'disabled:border-line disabled:bg-surface disabled:text-ink-faint',
  ghost: 'text-ink-muted hover:bg-accent-soft hover:text-accent-ink',
  danger: 'bg-danger text-white hover:brightness-95 disabled:opacity-50',
  subtle: 'bg-sunken text-ink border border-line hover:border-accent-line hover:bg-accent-soft hover:text-accent-ink',
}

const SIZES: Record<Size, string> = {
  sm: 'h-7 px-2.5 text-xs gap-1.5',
  md: 'h-8 px-3 text-sm gap-2',
  lg: 'h-10 px-4 text-sm gap-2',
  icon: 'h-8 w-8 justify-center',
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant = 'secondary', size = 'md', loading, disabled, children, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      disabled={disabled ?? loading}
      className={cn(
        'inline-flex items-center rounded-md font-medium transition-colors',
        'disabled:cursor-not-allowed disabled:opacity-70',
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
      {...rest}
    >
      {loading ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" /> : null}
      {children}
    </button>
  )
})
