import { useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { ArrowRight, Bot, Cog, ScrollText, User, X } from 'lucide-react'
import { useAudit, type AuditFilter } from './queries'
import {
  actorLine,
  changedFields,
  groupByOperation,
  humanObject,
  splitAction,
  transitionOf,
  SUBJECT_KINDS,
  type AuditEntry,
} from './model'
import { useMembers } from '@/features/project/queries'
import { Badge, Card, EmptyState, Input, Loading, Select } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { formatTimestamp, humanise } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * The audit view.
 *
 * F7's exit criterion is a number: **who/what/when/on-whose-behalf in three
 * clicks.** That is the whole design brief. Filters are in the URL so the
 * answer is a link somebody can send; entries sharing a request id are grouped,
 * because fourteen rows that are one proposal must not read as an afternoon of
 * edits; and a transition renders as a transition rather than as two JSONB
 * blobs a reader has to diff themselves (§6.5).
 *
 * Deleted objects still appear here, on purpose. Nothing is hard-deleted
 * precisely so a deleted work item's trail stays reachable — that is the whole
 * reason for the invariant, and a view that hid them would spend it.
 */
export function AuditPage() {
  const { projectId = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const members = useMembers(projectId)

  const filter: AuditFilter = useMemo(
    () => ({
      ...(params.get('subject') ? { subject: params.get('subject')! } : {}),
      ...(params.get('subject_id') ? { subjectId: params.get('subject_id')! } : {}),
      ...(params.get('actor') ? { actor: params.get('actor')! } : {}),
      ...(params.get('action') ? { action: params.get('action')! } : {}),
      ...(params.get('since') ? { since: dayStart(params.get('since')!) } : {}),
      ...(params.get('until') ? { until: dayEnd(params.get('until')!) } : {}),
    }),
    [params],
  )

  const audit = useAudit(projectId, filter)
  const groups = useMemo(() => groupByOperation(audit.data ?? []), [audit.data])

  const set = (key: string, value: string) => {
    const p = new URLSearchParams(params)
    if (value) p.set(key, value)
    else p.delete(key)
    setParams(p, { replace: true })
  }

  const active = ['subject', 'subject_id', 'actor', 'action', 'since', 'until'].filter((k) =>
    params.get(k),
  )

  return (
    <div className="mx-auto w-full max-w-4xl px-6 py-6">
      <div className="flex items-center gap-2">
        <ScrollText className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Audit</h1>
      </div>
      <p className="mt-0.5 text-ink-muted">
        Every write, kept forever. Nothing here is pruned, rotated or archived, and nothing in the
        product is hard-deleted — so a deleted item's history stays answerable, which is the whole
        reason for the rule.
      </p>

      {/* ── filters, in the URL ── */}
      <Card className="mt-4 flex flex-wrap items-end gap-2 px-3 py-3">
        <label className="flex min-w-40 flex-1 flex-col gap-1">
          <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            Subject
          </span>
          <Select value={params.get('subject') ?? ''} onChange={(e) => set('subject', e.target.value)}>
            <option value="">Anything</option>
            {SUBJECT_KINDS.map((k) => (
              <option key={k} value={k}>
                {humanObject(k)}
              </option>
            ))}
          </Select>
        </label>

        <label className="flex min-w-40 flex-1 flex-col gap-1">
          <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            Actor
          </span>
          {/* Actor ids, not user ids — an agent has its own (D11), so the list
              is a convenience for the humans and the box still takes an id. */}
          <Select value={params.get('actor') ?? ''} onChange={(e) => set('actor', e.target.value)}>
            <option value="">Anybody</option>
            {(members.data ?? []).map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.display_name}
              </option>
            ))}
          </Select>
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            From
          </span>
          <Input
            type="date"
            value={params.get('since') ?? ''}
            onChange={(e) => set('since', e.target.value)}
            className="w-36"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            To
          </span>
          <Input
            type="date"
            value={params.get('until') ?? ''}
            onChange={(e) => set('until', e.target.value)}
            className="w-36"
          />
        </label>

        {active.length > 0 ? (
          <Button variant="ghost" onClick={() => setParams(new URLSearchParams(), { replace: true })}>
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        ) : null}
      </Card>

      {params.get('subject_id') ? (
        <p className="mt-2 flex items-center gap-1.5 text-ink-muted">
          Showing one thing's whole history.
          <button
            type="button"
            className="text-accent-ink underline-offset-2 hover:underline"
            onClick={() => set('subject_id', '')}
          >
            Show everything
          </button>
        </p>
      ) : null}

      {audit.isPending ? (
        <Loading what="the audit trail" />
      ) : audit.error ? (
        <p className="py-4 text-ink-muted">{audit.error.message}</p>
      ) : groups.length === 0 ? (
        <EmptyState
          className="pt-16"
          icon={<ScrollText className="h-7 w-7" />}
          title="Nothing matches those filters"
          body="The log is never empty for a project that has been used — widen the range or clear the filters."
        />
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {groups.map((g) => (
            <Operation
              key={g.requestId + g.at}
              entries={g.entries}
              requestId={g.requestId}
              onFilterSubject={(id) => set('subject_id', id)}
            />
          ))}
          <p className="py-2 text-center text-[11px] text-ink-faint">
            {audit.data.length} entries. The log is bounded per request, not paged — narrow the
            range to see further back.
          </p>
        </div>
      )}
    </div>
  )
}

/**
 * One logical operation.
 *
 * Rendered as a unit when it carries several entries, because that is what it
 * is: one proposal applying fourteen changes shares one request id, and showing
 * fourteen separate rows would answer "who did this" fourteen times with the
 * same name and lose the fact that it was one decision.
 */
function Operation({
  entries,
  requestId,
  onFilterSubject,
}: {
  entries: AuditEntry[]
  requestId: string
  onFilterSubject: (subjectId: string) => void
}) {
  const first = entries[0]
  if (!first) return null
  const many = entries.length > 1

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
        <ActorIcon kind={first.actor_kind} />
        <span className="font-medium text-ink">{actorLine(first)}</span>
        {first.actor_model ? (
          <span className="text-[11px] text-ink-faint">on {first.actor_model}</span>
        ) : null}
        <span className="text-ink-faint">·</span>
        <span className="text-ink-muted">{formatTimestamp(first.at)}</span>
        {many ? (
          <Badge tone="info" title="These share one request id, so they are one operation.">
            {entries.length} changes, one operation
          </Badge>
        ) : null}
        <span className="ml-auto font-mono text-[11px] text-ink-faint" title="Request id">
          {requestId}
        </span>
      </div>

      <div className="flex flex-col">
        {entries.map((e) => (
          <Row key={e.id} entry={e} onFilterSubject={onFilterSubject} />
        ))}
      </div>
    </Card>
  )
}

function Row({
  entry,
  onFilterSubject,
}: {
  entry: AuditEntry
  onFilterSubject: (subjectId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const { verb } = splitAction(entry.action)
  const transition = transitionOf(entry)
  const fields = transition ? [] : changedFields(entry)

  return (
    <div className="border-b border-line px-3 py-2 last:border-b-0">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-ink-faint">{humanObject(entry.subject_kind)}</span>

        {/* A transition, as a transition. This is the line §6.5 constraint 2
            exists to make possible. */}
        {transition ? (
          <span className="flex items-center gap-1.5">
            <span className="text-ink-faint">{transition.field}</span>
            <code className="rounded bg-sunken px-1 py-0.5 text-[11px] text-ink-muted">
              {transition.from}
            </code>
            <ArrowRight className="h-3 w-3 text-ink-faint" />
            <code className="rounded bg-accent-soft px-1 py-0.5 text-[11px] text-accent-ink">
              {transition.to}
            </code>
          </span>
        ) : (
          <span className="text-ink">{humanise(verb || entry.action)}</span>
        )}

        {entry.subject_id ? (
          <button
            type="button"
            onClick={() => onFilterSubject(entry.subject_id!)}
            className="font-mono text-[11px] text-ink-faint underline-offset-2 hover:text-accent-ink hover:underline"
            title={`${entry.subject_id} — show this one thing's whole history`}
          >
            {shortId(entry.subject_id)}
          </button>
        ) : null}

        {fields.length > 0 ? (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="text-[11px] text-ink-faint underline-offset-2 hover:text-ink hover:underline"
          >
            {open ? 'hide' : `${fields.length} field${fields.length === 1 ? '' : 's'}`}
          </button>
        ) : null}
      </div>

      {open && fields.length > 0 ? (
        <table className="mt-1.5 w-full">
          <tbody>
            {fields.map((f) => (
              <tr key={f.field} className="border-b border-line last:border-b-0">
                <td className="w-40 py-1 align-top text-ink-faint">{f.field}</td>
                <td className="py-1 align-top">
                  <code className="text-[11px] text-ink-faint line-through">{render(f.before)}</code>
                  <span className="mx-1.5 text-ink-faint">→</span>
                  <code className="text-[11px] text-ink">{render(f.after)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </div>
  )
}

/**
 * The distinguishing end of an id, not the leading end.
 *
 * These are ULIDs: time-ordered, so objects written in the same transaction
 * share a long prefix. Ten notes created by one ingest accept all begin
 * `019fe246`, and a leading truncation renders them as ten copies of the same
 * thing — which is the exact opposite of what an identifier is for.
 */
function shortId(id: string): string {
  return id.length <= 8 ? id : `…${id.slice(-6)}`
}

function render(v: unknown): string {
  if (v === undefined || v === null || v === '') return 'unset'
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}

function ActorIcon({ kind }: { kind: AuditEntry['actor_kind'] }) {
  const Icon = kind === 'agent' ? Bot : kind === 'system' ? Cog : User
  return <Icon className={cn('h-3.5 w-3.5 shrink-0 text-ink-faint')} aria-label={kind} />
}

/**
 * A picked calendar day, as the instant the filter needs.
 *
 * `since`/`until` are RFC3339 instants while the picker emits `YYYY-MM-DD`
 * (frontend-notes §10). Converting here, at the edge, is the honest place: the
 * day boundary a person means is midnight *where they are*, and constructing it
 * from the parts is how it stays that way.
 */
function dayStart(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  if (!y || !m || !d) return day
  return new Date(y, m - 1, d, 0, 0, 0, 0).toISOString()
}

function dayEnd(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  if (!y || !m || !d) return day
  return new Date(y, m - 1, d, 23, 59, 59, 999).toISOString()
}
