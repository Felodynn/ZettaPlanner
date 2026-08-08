import type { components } from '@/api/schema'

export type AuditEntry = components['schemas']['AuditEntryView']

/**
 * The audit view's job is one question: **who changed this, and why.**
 *
 * §6.5 constraint 2 is what makes it answerable: state transitions are their
 * own action, not a generic `work_item.updated` whose reader has to diff two
 * JSONB blobs to find out what moved. The backend records them distinctly
 * *precisely so this view can render them as transitions* — `blocked →
 * in_progress` is the line a person reads; `work_item.updated` with a diff is
 * the line they skip.
 */

/** `work_item.status_changed` → `{ object: 'work_item', verb: 'status_changed' }`. */
export function splitAction(action: string): { object: string; verb: string } {
  const dot = action.lastIndexOf('.')
  if (dot < 0) return { object: action, verb: '' }
  return { object: action.slice(0, dot), verb: action.slice(dot + 1) }
}

export function humanObject(kind: string): string {
  return kind.replace(/_/g, ' ')
}

/**
 * What a transition moved, when the entry is one.
 *
 * `is_transition` is the backend's own flag; this only reads the two values it
 * promises are there. Nothing is inferred from a generic update — an entry that
 * is not a transition gets the field-level treatment instead, honestly labelled
 * as an edit.
 */
export interface Transition {
  field: string
  from: string
  to: string
}

const TRANSITION_FIELD: Record<string, string> = {
  'work_item.status_changed': 'status',
  'sub_task.status_changed': 'status',
  'milestone.status_changed': 'status',
  'note.status_changed': 'status',
  'proposal.decided': 'status',
  'work_item.assigned': 'assignee',
  'work_item.milestone_changed': 'milestone',
  'checklist.checked': 'checked',
  'deviation.resolved': 'status',
  'user.role_changed': 'role',
  'project_member.role_changed': 'role',
  'diagram.layout_mode_changed': 'layout_mode',
}

export function transitionOf(entry: AuditEntry): Transition | null {
  if (!entry.is_transition) return null
  const field = TRANSITION_FIELD[entry.action]
  if (!field) return null
  const from = readField(entry.before, field)
  const to = readField(entry.after, field)
  if (from === undefined && to === undefined) return null
  return { field, from: from ?? '—', to: to ?? '—' }
}

function readField(blob: unknown, field: string): string | undefined {
  if (typeof blob !== 'object' || blob === null) return undefined
  const v = (blob as Record<string, unknown>)[field]
  if (v === undefined || v === null) return undefined
  if (typeof v === 'string') return v || undefined
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return undefined
}

/** Every field that differs between before and after, for a non-transition edit. */
export function changedFields(entry: AuditEntry): { field: string; before: unknown; after: unknown }[] {
  const before = asRecord(entry.before)
  const after = asRecord(entry.after)
  const keys = new Set([...Object.keys(before), ...Object.keys(after)])
  const out: { field: string; before: unknown; after: unknown }[] = []
  for (const field of [...keys].sort()) {
    const b = before[field]
    const a = after[field]
    if (JSON.stringify(b) === JSON.stringify(a)) continue
    out.push({ field, before: b, after: a })
  }
  return out
}

function asRecord(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {}
}

/**
 * Who did it, on whose behalf.
 *
 * An agent write is attributed to both the agent and the instructing user, and
 * the audit shape carries `on_behalf_of_name` for exactly this — the one place
 * in the API where the name arrives beside the id.
 */
export function actorLine(entry: AuditEntry): string {
  const who = entry.actor_name || entry.actor_kind
  if (!entry.on_behalf_of && !entry.on_behalf_of_name) return who
  return `${who}, for ${entry.on_behalf_of_name || entry.on_behalf_of}`
}

/**
 * Entries sharing a request id are one logical operation (§6.5 constraint 3).
 *
 * Fourteen entries with one request id is a proposal applied in one go, not an
 * afternoon of edits — and that distinction is the whole reason the column is
 * non-null in the schema. Grouping is how the view says it out loud.
 */
export interface OperationGroup {
  requestId: string
  entries: AuditEntry[]
  at: string
}

export function groupByOperation(entries: AuditEntry[]): OperationGroup[] {
  const groups: OperationGroup[] = []
  let current: OperationGroup | null = null
  for (const e of entries) {
    if (!current || current.requestId !== e.request_id) {
      current = { requestId: e.request_id, entries: [], at: e.at }
      groups.push(current)
    }
    current.entries.push(e)
  }
  return groups
}

/** The subject kinds worth offering as a filter, in the order a person thinks. */
export const SUBJECT_KINDS = [
  'work_item',
  'sub_task',
  'milestone',
  'checklist_item',
  'deviation',
  'worklog',
  'diagram',
  'diagram_node',
  'diagram_edge',
  'catalog_entity',
  'ingest',
  'proposal',
  'note',
  'turn',
  'thread',
  'project',
  'user',
] as const
