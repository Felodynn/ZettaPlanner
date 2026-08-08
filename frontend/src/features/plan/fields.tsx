import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Lock, Pencil } from 'lucide-react'
import type { FieldPermission } from './ownership'
import { Input, Select, Textarea } from '@/components/ui/primitives'
import { formatDay } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'

/**
 * Editable-or-not, decided by ownership before anything is typed.
 *
 * F2: a field the current user does not own is rendered read-only with a hint
 * naming the owning role and offering a route. Letting a developer type into a
 * due-date field and then rejecting the save is the worst of both — they have
 * already decided what they wanted, and the refusal arrives after the effort.
 *
 * The lock is never the only signal: the value stays plainly readable and the
 * control simply is not there, because a greyed-out input still reads as
 * "try harder".
 */

function Locked({ why, children }: { why: string; children: ReactNode }) {
  return (
    <div className="group/locked flex items-start gap-1.5">
      <div className="min-w-0 flex-1">{children}</div>
      <span
        title={why}
        className="mt-0.5 shrink-0 cursor-help text-ink-faint opacity-0 transition-opacity group-hover/locked:opacity-100"
      >
        <Lock className="h-3 w-3" />
      </span>
    </div>
  )
}

export function FieldRow({
  label,
  perm,
  children,
  hint,
}: {
  label: string
  perm?: FieldPermission
  children: ReactNode
  hint?: string
}) {
  return (
    <div className="grid grid-cols-[7.5rem_1fr] items-start gap-3 py-1">
      <div className="flex items-center gap-1 pt-1 text-ink-faint">
        <span>{label}</span>
      </div>
      <div className="min-w-0">
        {perm && !perm.writable && perm.why ? <Locked why={perm.why}>{children}</Locked> : children}
        {hint ? <p className="mt-0.5 text-[11px] text-ink-faint">{hint}</p> : null}
      </div>
    </div>
  )
}

/** Text that becomes an input on click, and saves on blur. */
export function EditableText({
  value,
  perm,
  onSave,
  multiline,
  placeholder,
  className,
}: {
  value: string | undefined
  perm: FieldPermission
  onSave: (next: string) => void
  multiline?: boolean
  placeholder?: string
  className?: string
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value ?? '')
  const ref = useRef<HTMLInputElement & HTMLTextAreaElement>(null)

  useEffect(() => setDraft(value ?? ''), [value])
  useEffect(() => {
    if (editing) ref.current?.focus()
  }, [editing])

  const commit = () => {
    setEditing(false)
    const next = draft.trim()
    if (next !== (value ?? '').trim()) onSave(next)
  }

  if (!perm.writable) {
    return (
      <div className={cn('wrap-break-word whitespace-pre-wrap text-ink', className)}>
        {value || <span className="text-ink-faint">{placeholder ?? '—'}</span>}
      </div>
    )
  }

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => setEditing(true)}
        className={cn(
          'group -mx-1 flex w-full items-start gap-1.5 rounded px-1 py-0.5 text-left',
          'transition-colors hover:bg-sunken',
          className,
        )}
      >
        <span className="min-w-0 flex-1 wrap-break-word whitespace-pre-wrap text-ink">
          {value || <span className="text-ink-faint">{placeholder ?? 'Not set'}</span>}
        </span>
        <Pencil className="mt-0.5 h-3 w-3 shrink-0 text-ink-faint opacity-0 transition-opacity group-hover:opacity-100" />
      </button>
    )
  }

  const Field = multiline ? Textarea : Input
  return (
    <Field
      ref={ref}
      value={draft}
      rows={multiline ? 4 : undefined}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          setDraft(value ?? '')
          setEditing(false)
        }
        if (e.key === 'Enter' && !multiline) commit()
      }}
      placeholder={placeholder}
    />
  )
}

export function EditableSelect<T extends string>({
  value,
  options,
  perm,
  onSave,
  allowEmpty,
  emptyLabel = 'Not set',
  render = (v) => v.replace(/_/g, ' '),
  children,
}: {
  value: T | undefined
  options: readonly T[]
  perm: FieldPermission
  onSave: (next: string) => void
  allowEmpty?: boolean
  emptyLabel?: string
  render?: (v: T) => string
  /** What to show when read-only — usually a badge rather than raw text. */
  children?: ReactNode
}) {
  if (!perm.writable) {
    return <>{children ?? <span className="text-ink">{value ? render(value) : emptyLabel}</span>}</>
  }
  return (
    <Select
      value={value ?? ''}
      onChange={(e) => onSave(e.target.value)}
      className="h-7 w-auto min-w-32 text-xs"
    >
      {allowEmpty ? <option value="">{emptyLabel}</option> : null}
      {options.map((o) => (
        <option key={o} value={o}>
          {render(o)}
        </option>
      ))}
    </Select>
  )
}

/**
 * A calendar day.
 *
 * `<input type="date">` emits `YYYY-MM-DD`, which is exactly what the API
 * wants, so the value goes through untouched. frontend-notes §10: these are
 * not instants, and putting one through a `Date` and back out is how a due
 * date moves a day for everyone not sitting on the server.
 */
export function EditableDate({
  value,
  perm,
  onSave,
  className,
}: {
  value: string | undefined
  perm: FieldPermission
  onSave: (next: string) => void
  className?: string
}) {
  if (!perm.writable) {
    // Formatted for reading, but the underlying string is never parsed into a
    // Date and back — that is how a due date moves a day for everyone not
    // sitting on the server (frontend-notes §10).
    return (
      <span className={cn('zp-num text-ink', className)} title={value}>
        {value ? formatDay(value) : <span className="text-ink-faint">—</span>}
      </span>
    )
  }
  return (
    <div className="flex items-center gap-1">
      <Input
        type="date"
        value={value ?? ''}
        onChange={(e) => onSave(e.target.value)}
        className={cn('zp-num h-7 w-auto text-xs', className)}
      />
      {value ? (
        <Button
          size="sm"
          variant="ghost"
          className="h-7 px-1.5"
          onClick={() => onSave('')}
          title="Clear this date"
        >
          ×
        </Button>
      ) : null}
    </div>
  )
}
