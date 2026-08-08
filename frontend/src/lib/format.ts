/**
 * Dates and times, kept apart on purpose.
 *
 * frontend-notes §10: `start_date` and `due_date` are calendar days —
 * `YYYY-MM-DD`, not instants. Putting one through a `Date` and back out is how
 * a due date moves a day for everyone not sitting on the server. So the
 * calendar-day helpers below never construct a `Date` from the string; they
 * parse the three numbers and format them.
 *
 * Timestamps (`created_at`, `at`, `decided_at`) *are* instants, RFC3339, and
 * are rendered in the viewer's zone.
 */

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** Splits `YYYY-MM-DD` without going near a Date. */
function parts(day: string): [number, number, number] | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3])]
}

/** `2026-08-08` → `8 Aug 2026`. */
export function formatDay(day: string | null | undefined): string {
  if (!day) return '—'
  const p = parts(day)
  if (!p) return day
  const [y, mo, d] = p
  return `${d} ${MONTHS[mo - 1] ?? mo} ${y}`
}

/** `2026-08-08` → `8 Aug`, dropping the year when it is the current one. */
export function formatDayShort(day: string | null | undefined): string {
  if (!day) return '—'
  const p = parts(day)
  if (!p) return day
  const [y, mo, d] = p
  const thisYear = todayISO().slice(0, 4)
  return y === Number(thisYear) ? `${d} ${MONTHS[mo - 1] ?? mo}` : `${d} ${MONTHS[mo - 1] ?? mo} ${y}`
}

/** Today as a calendar day in the viewer's zone. */
export function todayISO(): string {
  const now = new Date()
  const y = now.getFullYear()
  const m = String(now.getMonth() + 1).padStart(2, '0')
  const d = String(now.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/**
 * Whole days from today to a calendar day; negative when it is past.
 * Computed on the calendar, not on elapsed milliseconds, so a daylight-saving
 * boundary cannot make "due tomorrow" read as "due today".
 */
export function daysUntil(day: string): number | null {
  const p = parts(day)
  if (!p) return null
  const t = parts(todayISO())
  if (!t) return null
  const a = Date.UTC(p[0], p[1] - 1, p[2])
  const b = Date.UTC(t[0], t[1] - 1, t[2])
  return Math.round((a - b) / 86_400_000)
}

export function isOverdue(day: string | null | undefined): boolean {
  if (!day) return false
  const n = daysUntil(day)
  return n !== null && n < 0
}

// ── instants ─────────────────────────────────────────────────────────────────

export function formatTimestamp(ts: string | null | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleString(undefined, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function formatTime(ts: string | null | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

/** "3 minutes ago", for a transcript where exact seconds do not matter. */
export function formatRelative(ts: string | null | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const secs = Math.round((Date.now() - d.getTime()) / 1000)
  if (secs < 45) return 'just now'
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  const table: [Intl.RelativeTimeFormatUnit, number][] = [
    ['second', 60],
    ['minute', 60],
    ['hour', 24],
    ['day', 7],
    ['week', 4.348],
    ['month', 12],
    ['year', Infinity],
  ]
  let value = secs
  for (const [unit, size] of table) {
    if (Math.abs(value) < size) return rtf.format(-Math.round(value), unit)
    value /= size
  }
  return formatTimestamp(ts)
}

/** `4d 6h` — how the audit view reads a transition's dwell time. */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

/** Turns `in_progress` into `In progress` for a label. */
export function humanise(value: string): string {
  const s = value.replace(/_/g, ' ')
  return s.charAt(0).toUpperCase() + s.slice(1)
}

export function formatTokens(n: number | null | undefined): string {
  if (!n) return '0'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}
