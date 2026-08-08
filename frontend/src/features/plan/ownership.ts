import { useMemo } from 'react'
import { useFieldOwnership } from '@/features/project/queries'
import type { OwnershipScope, Role } from '@/api/vocabulary'

/**
 * Field ownership, decided once (§4.2, frontend-notes §4).
 *
 * `GET /field-ownership` publishes the table the write path enforces, read
 * from the same function. This wraps it so no screen keeps a second copy of
 * the rule — the nastiest case being the work item, whose `status` and
 * `progress_pct` belong to its assignee while everything else belongs to a
 * manager. That makes the detail view *two* editable regions with different
 * owners, and which one is yours changes per item.
 *
 * **It is a hint and never a control.** `scope` is the reason: `project` means
 * the role is enough, but `assigned`, `author` and `reporter` depend on the
 * specific row, and only the view knows whether this item is the caller's.
 * Every write is checked again on the server, and the same endpoints are
 * called by an agent that never saw this UI — so a disabled control is a
 * courtesy, and every write path still handles a 403.
 */

/** What the view knows about the caller's relationship to this particular row. */
export interface Relationship {
  /** The item is assigned to the caller. */
  assigned?: boolean
  /** The caller wrote this worklog. */
  author?: boolean
  /** The caller reported this deviation. */
  reporter?: boolean
}

export interface FieldPermission {
  writable: boolean
  owner: Role
  scope: OwnershipScope
  /**
   * Why it is read-only, phrased the way the backend phrases a denial: the
   * route first, then the reason (§7.3). Undefined when writable.
   */
  why?: string
  /** True when the role is fine and only the relationship is missing. */
  needsRelationship: boolean
}

const WRITABLE: FieldPermission = {
  writable: true,
  owner: 'developer',
  scope: 'project',
  needsRelationship: false,
}

function reasonFor(
  field: string,
  owner: Role,
  scope: OwnershipScope,
  effectiveRole: Role,
  roleIsEnough: boolean,
): string {
  if (!roleIsEnough) {
    // Same shape as the backend's sentence: the action first, because a
    // refusal that leads with the action gets the action (§7.3, LumixAgents
    // D34).
    return `To change ${field}, raise a proposal or ask a ${owner}. It is ${owner}-owned, and your effective role on this project is ${effectiveRole}.`
  }
  switch (scope) {
    case 'assigned':
      return `${field} belongs to whoever this item is assigned to. Ask them, or ask a manager to reassign it.`
    case 'author':
      return `${field} belongs to whoever wrote it.`
    case 'reporter':
      return `${field} belongs to whoever reported it.`
    case 'project':
      return `${field} is ${owner}-owned.`
  }
}

export interface OwnershipHelper {
  ready: boolean
  effectiveRole: Role | undefined
  /** The caller's standing on one field of one row. */
  field: (objectKind: string, field: string, rel?: Relationship) => FieldPermission
  /** Whether the caller may create or delete this kind of object at all. */
  lifecycle: (objectKind: string, rel?: Relationship) => FieldPermission
}

export function useOwnership(projectId: string): OwnershipHelper {
  const query = useFieldOwnership(projectId)

  return useMemo<OwnershipHelper>(() => {
    const data = query.data
    if (!data) {
      // Before the table arrives, claim nothing. Rendering a control as
      // writable and then taking it away is worse than a moment of read-only,
      // and rendering everything read-only would flash the whole form grey.
      return {
        ready: false,
        effectiveRole: undefined,
        field: () => WRITABLE,
        lifecycle: () => WRITABLE,
      }
    }

    const effectiveRole = data.effective_role
    const byKind = new Map((data.objects ?? []).map((o) => [o.kind, o]))

    const resolve = (objectKind: string, fieldName: string, rel: Relationship): FieldPermission => {
      const object = byKind.get(objectKind)
      if (!object) return WRITABLE

      const rule =
        (object.fields ?? []).find((f) => f.field === fieldName) ?? object.all_fields ?? undefined
      if (!rule) return WRITABLE

      const roleIsEnough = rule.writable_by_role
      const hasRelationship =
        !rule.needs_relationship ||
        (rule.scope === 'assigned' && rel.assigned === true) ||
        (rule.scope === 'author' && rel.author === true) ||
        (rule.scope === 'reporter' && rel.reporter === true) ||
        rule.scope === 'project'

      const writable = roleIsEnough && hasRelationship
      return {
        writable,
        owner: rule.owner,
        scope: rule.scope,
        needsRelationship: rule.needs_relationship,
        ...(writable
          ? {}
          : { why: reasonFor(fieldName, rule.owner, rule.scope, effectiveRole, roleIsEnough) }),
      }
    }

    return {
      ready: true,
      effectiveRole,
      field: (kind, name, rel = {}) => resolve(kind, name, rel),
      lifecycle: (kind, rel = {}) => resolve(kind, '*', rel),
    }
  }, [query.data])
}
