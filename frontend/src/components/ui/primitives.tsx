import {
  forwardRef,
  type HTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react'
import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

// ── surfaces ─────────────────────────────────────────────────────────────────

export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-card border border-line bg-surface', className)}
      {...rest}
    />
  )
}

export function CardHeader({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('flex items-center justify-between gap-3 border-b border-line px-4 py-2.5', className)}
      {...rest}
    />
  )
}

export function CardTitle({ className, ...rest }: HTMLAttributes<HTMLHeadingElement>) {
  return <h2 className={cn('text-sm font-semibold text-ink', className)} {...rest} />
}

/** A section label above a group of fields. */
export function FieldLabel({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('text-[11px] font-semibold tracking-wide text-ink-faint uppercase', className)}
      {...rest}
    />
  )
}

// ── inputs ───────────────────────────────────────────────────────────────────

const FIELD =
  'w-full rounded-md border border-line-strong bg-surface px-2.5 py-1.5 text-ink ' +
  'placeholder:text-ink-faint disabled:cursor-not-allowed disabled:bg-sunken disabled:text-ink-muted ' +
  'focus:border-accent focus:outline-none focus:ring-1 focus:ring-accent'

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className, ...rest }, ref) {
    return <input ref={ref} className={cn(FIELD, 'h-8', className)} {...rest} />
  },
)

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function Textarea({ className, ...rest }, ref) {
    return <textarea ref={ref} className={cn(FIELD, 'resize-y', className)} {...rest} />
  },
)

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  function Select({ className, ...rest }, ref) {
    // No vertical padding: a native select centres its own text against the
    // element height, and adding padding on top of that clips the descenders
    // at the small sizes this UI uses.
    return <select ref={ref} className={cn(FIELD, 'h-8 py-0 pr-7', className)} {...rest} />
  },
)

// ── feedback ─────────────────────────────────────────────────────────────────

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn('h-4 w-4 animate-spin text-ink-faint', className)} />
}

/**
 * The loading state for a whole panel.
 *
 * Named rather than a bare spinner: FRONTEND F7 asks for no unstyled loading
 * state on any primary screen, and "Loading" with no subject is the version
 * people read as "stuck".
 */
export function Loading({ what, className }: { what: string; className?: string }) {
  return (
    <div
      className={cn('flex items-center justify-center gap-2 px-4 py-10 text-ink-faint', className)}
      role="status"
    >
      <Spinner />
      <span>Loading {what}…</span>
    </div>
  )
}

export function EmptyState({
  title,
  body,
  action,
  icon,
  className,
}: {
  title: string
  body?: ReactNode
  action?: ReactNode
  icon?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center gap-2 px-6 py-12 text-center', className)}>
      {icon ? <div className="text-ink-faint">{icon}</div> : null}
      <p className="font-medium text-ink">{title}</p>
      {body ? <div className="max-w-md text-ink-muted">{body}</div> : null}
      {action ? <div className="pt-1">{action}</div> : null}
    </div>
  )
}

// ── badges ───────────────────────────────────────────────────────────────────

export type Tone = 'neutral' | 'accent' | 'good' | 'warn' | 'danger' | 'info' | 'outline'

const TONES: Record<Tone, string> = {
  neutral: 'bg-sunken text-ink-muted border-line',
  accent: 'bg-accent-soft text-accent-ink border-accent-line',
  good: 'bg-good-soft text-good-ink border-good/25',
  warn: 'bg-warn-soft text-warn-ink border-warn/25',
  danger: 'bg-danger-soft text-danger-ink border-danger/25',
  info: 'bg-info-soft text-info-ink border-info/25',
  outline: 'bg-transparent text-ink-muted border-line-strong',
}

export function Badge({
  tone = 'neutral',
  className,
  ...rest
}: HTMLAttributes<HTMLSpanElement> & { tone?: Tone }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] leading-4 font-medium whitespace-nowrap',
        TONES[tone],
        className,
      )}
      {...rest}
    />
  )
}

// ── layout helpers ───────────────────────────────────────────────────────────

export function Separator({ className }: { className?: string }) {
  return <div className={cn('h-px bg-line', className)} role="separator" />
}

/** A key/value row, the shape most of the detail panels are made of. */
export function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] items-start gap-3 py-1.5">
      <div className="pt-0.5 text-ink-faint">{label}</div>
      <div className="min-w-0 text-ink">{children}</div>
    </div>
  )
}
