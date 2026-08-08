import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { WORK_ITEM_STATUS_VALUES, type WorkItemStatus } from '@/api/vocabulary'

export type WorkItem = components['schemas']['WorkItemSummary']
export type WorkItemDetail = components['schemas']['WorkItemDetail']
export type Milestone = components['schemas']['MilestoneSummary']
export type SubTask = components['schemas']['SubTaskSummary']
export type ChecklistItem = components['schemas']['ChecklistItemSummary']
export type Worklog = components['schemas']['WorklogSummary']
export type Deviation = components['schemas']['DeviationSummary']
export type Warning = components['schemas']['WarningSummary']
export type Dashboard = components['schemas']['DashboardBody']

export type WorkItemPatch = components['schemas']['WorkItemPatchBody']

export interface ItemFilters {
  assignee?: string
  /** Typed, not a string: the filter goes straight into the query the backend
   *  validates, and a deep link is user input. */
  status?: WorkItemStatus
  milestone?: string
}

/**
 * Reads the filters out of a URL.
 *
 * The status is checked against the generated vocabulary rather than passed
 * through: a filtered view is a link (F2 wants them deep-linkable), links get
 * edited by hand, and an invalid value would reach the backend as a 422 with
 * nothing on screen to explain it.
 */
export function filtersFromParams(params: URLSearchParams): ItemFilters {
  const status = params.get('status')
  const assignee = params.get('assignee')
  const milestone = params.get('milestone')
  return {
    ...(assignee ? { assignee } : {}),
    ...(milestone ? { milestone } : {}),
    ...(status && (WORK_ITEM_STATUS_VALUES as readonly string[]).includes(status)
      ? { status: status as WorkItemStatus }
      : {}),
  }
}

export const planKeys = {
  root: (pid: string) => ['plan', pid] as const,
  milestones: (pid: string) => ['plan', pid, 'milestones'] as const,
  items: (pid: string, f: ItemFilters) => ['plan', pid, 'items', f] as const,
  item: (id: string) => ['plan-item', id] as const,
  worklogs: (pid: string, item?: string) => ['plan', pid, 'worklogs', item ?? 'all'] as const,
  deviations: (pid: string, item?: string) => ['plan', pid, 'deviations', item ?? 'all'] as const,
  warnings: (pid: string) => ['warnings', pid] as const,
  dashboard: (pid: string) => ['dashboard', pid] as const,
}

// ── reads ────────────────────────────────────────────────────────────────────

export function useMilestones(pid: string) {
  return useQuery({
    queryKey: planKeys.milestones(pid),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/milestones', { params: { path: { pid } } }),
          )
        ).milestones,
      ),
  })
}

export function useWorkItems(pid: string, filters: ItemFilters) {
  return useQuery({
    queryKey: planKeys.items(pid, filters),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/work-items', {
              params: {
                path: { pid },
                query: {
                  ...(filters.assignee ? { assignee: filters.assignee } : {}),
                  ...(filters.status ? { status: filters.status } : {}),
                  ...(filters.milestone ? { milestone: filters.milestone } : {}),
                },
              },
            }),
          )
        ).work_items,
      ),
  })
}

export function useWorkItem(id: string) {
  return useQuery({
    queryKey: planKeys.item(id),
    queryFn: () => unwrap(client.GET('/api/v1/work-items/{id}', { params: { path: { id } } })),
    enabled: Boolean(id),
  })
}

export function useWarnings(pid: string) {
  return useQuery({
    queryKey: planKeys.warnings(pid),
    queryFn: async () =>
      list(
        (await unwrap(client.GET('/api/v1/projects/{pid}/warnings', { params: { path: { pid } } })))
          .warnings,
      ),
  })
}

/**
 * The project view in one call.
 *
 * frontend-notes §11: it exists so the view does not render six moments. A
 * screen assembled from six requests shows six different instants, and the one
 * that matters — is anything overdue — arrives last.
 */
export function useDashboard(pid: string, horizonDays = 14) {
  return useQuery({
    queryKey: [...planKeys.dashboard(pid), horizonDays],
    queryFn: () =>
      unwrap(
        client.GET('/api/v1/projects/{pid}/dashboard', {
          params: { path: { pid }, query: { horizon_days: horizonDays } },
        }),
      ),
  })
}

export function useWorklogs(pid: string, item?: string) {
  return useQuery({
    queryKey: planKeys.worklogs(pid, item),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/worklogs', {
              params: { path: { pid }, query: { ...(item ? { item } : {}), limit: 200 } },
            }),
          )
        ).worklogs,
      ),
  })
}

export function useDeviations(pid: string, item?: string) {
  return useQuery({
    queryKey: planKeys.deviations(pid, item),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/deviations', {
              params: { path: { pid }, query: { ...(item ? { item } : {}) } },
            }),
          )
        ).deviations,
      ),
  })
}

// ── writes ───────────────────────────────────────────────────────────────────

/**
 * Invalidates everything a plan write can move.
 *
 * Coarse on purpose: a status change moves the item, the dashboard rollups,
 * and — because warnings are computed on read (§3.5) — possibly the warnings
 * too. Working out which of those actually changed would be a second copy of
 * the backend's rules.
 */
function usePlanInvalidation(pid: string, itemId?: string) {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: planKeys.root(pid) })
    void qc.invalidateQueries({ queryKey: planKeys.dashboard(pid) })
    void qc.invalidateQueries({ queryKey: planKeys.warnings(pid) })
    if (itemId) void qc.invalidateQueries({ queryKey: planKeys.item(itemId) })
  }
}

export function usePatchWorkItem(pid: string, id: string) {
  const invalidate = usePlanInvalidation(pid, id)
  return useMutation({
    mutationFn: (body: WorkItemPatch) =>
      unwrap(client.PATCH('/api/v1/work-items/{id}', { params: { path: { id } }, body })),
    onSuccess: invalidate,
  })
}

export function usePatchMilestone(pid: string, id: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: (body: components['schemas']['MilestonePatchBody']) =>
      unwrap(client.PATCH('/api/v1/milestones/{id}', { params: { path: { id } }, body })),
    onSuccess: invalidate,
  })
}

export function useAddSubTask(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: (title: string) =>
      unwrap(
        client.POST('/api/v1/work-items/{id}/sub-tasks', {
          params: { path: { id: itemId } },
          body: { title, status: 'todo' },
        }),
      ),
    onSuccess: invalidate,
  })
}

export function usePatchSubTask(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string
      body: components['schemas']['SubTaskPatchBody']
    }) => unwrap(client.PATCH('/api/v1/sub-tasks/{id}', { params: { path: { id } }, body })),
    onSuccess: invalidate,
  })
}

export function useDeleteSubTask(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: (id: string) =>
      client.DELETE('/api/v1/sub-tasks/{id}', { params: { path: { id } } }),
    onSuccess: invalidate,
  })
}

export function useAddChecklistItem(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: (text: string) =>
      unwrap(
        client.POST('/api/v1/work-items/{id}/checklist', {
          params: { path: { id: itemId } },
          body: { text },
        }),
      ),
    onSuccess: invalidate,
  })
}

export function usePatchChecklistItem(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string
      body: components['schemas']['ChecklistPatchBody']
    }) => unwrap(client.PATCH('/api/v1/checklist/{id}', { params: { path: { id } }, body })),
    onSuccess: invalidate,
  })
}

/**
 * Adds a worklog.
 *
 * There is deliberately no edit and no delete (§7.1, frontend-notes §9): a
 * worklog is corrected by adding another, which is what makes the log evidence
 * rather than a current-state field.
 */
export function useAddWorklog(pid: string, itemId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ body, kind }: { body: string; kind: 'progress' | 'deviation' | 'note' }) =>
      unwrap(
        client.POST('/api/v1/work-items/{id}/worklogs', {
          params: { path: { id: itemId } },
          body: { body, kind },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: planKeys.root(pid) })
    },
  })
}

export function useRaiseDeviation(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: (body: components['schemas']['Raise-deviationRequest']) =>
      unwrap(
        client.POST('/api/v1/projects/{pid}/deviations', { params: { path: { pid } }, body }),
      ),
    onSuccess: invalidate,
  })
}

export function usePatchDeviation(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string
      body: components['schemas']['DeviationPatchBody']
    }) => unwrap(client.PATCH('/api/v1/deviations/{id}', { params: { path: { id } }, body })),
    onSuccess: invalidate,
  })
}

// ── creating and removing plan objects ───────────────────────────────────────
//
// Manager-owned in full (§4.2). The forms that call these are gated on the
// ownership table like every other write, and the backend checks again.

export function useCreateMilestone(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: (body: components['schemas']['Create-milestoneRequest']) =>
      unwrap(client.POST('/api/v1/projects/{pid}/milestones', { params: { path: { pid } }, body })),
    onSuccess: invalidate,
  })
}

export function useCreateWorkItem(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: (body: components['schemas']['Create-work-itemRequest']) =>
      unwrap(client.POST('/api/v1/projects/{pid}/work-items', { params: { path: { pid } }, body })),
    onSuccess: invalidate,
  })
}

/**
 * Deleting a work item.
 *
 * Soft, always (§6.5): the row keeps its `deleted_at` and its ref is never
 * reissued, so a worklog or an audit entry mentioning ZP-14 keeps resolving.
 * `dropped` is the *other* thing — the decision not to do something, kept
 * because it is worth keeping — and the two are not interchangeable.
 */
export function useDeleteWorkItem(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await client.DELETE('/api/v1/work-items/{id}', { params: { path: { id } } })
      if (res.error) throw res.error
    },
    onSuccess: invalidate,
  })
}

/** Soft as well; its work items survive, detached from any milestone. */
export function useDeleteMilestone(pid: string) {
  const invalidate = usePlanInvalidation(pid)
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await client.DELETE('/api/v1/milestones/{id}', { params: { path: { id } } })
      if (res.error) throw res.error
    },
    onSuccess: invalidate,
  })
}

/**
 * Dependencies, set as a whole (§3.5).
 *
 * `blocks` cycles are rejected at write time with the path named, which is why
 * this is a PUT of the full set rather than an add/remove pair: the check is
 * over the graph, not over one edge.
 */
export function useSetDependencies(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: (body: components['schemas']['Set-dependenciesRequest']) =>
      unwrap(
        client.PUT('/api/v1/work-items/{id}/dependencies', {
          params: { path: { id: itemId } },
          body,
        }),
      ),
    onSuccess: invalidate,
  })
}

/**
 * Which architecture entities this item implements.
 *
 * Set by a human or an agent, **never derived** from the diagram (§3.5). They
 * exist so context assembly can pull the relevant architecture slice when the
 * item is discussed.
 */
export function useSetEntityRefs(pid: string, itemId: string) {
  const invalidate = usePlanInvalidation(pid, itemId)
  return useMutation({
    mutationFn: (implementsSlugs: string[]) =>
      unwrap(
        client.PUT('/api/v1/work-items/{id}/entity-refs', {
          params: { path: { id: itemId } },
          body: { implements: implementsSlugs },
        }),
      ),
    onSuccess: invalidate,
  })
}

/** The project's catalog entities, for the entity-ref picker. */
export function useEntities(pid: string) {
  return useQuery({
    queryKey: ['entities', pid],
    queryFn: async () =>
      list(
        (await unwrap(client.GET('/api/v1/projects/{pid}/entities', { params: { path: { pid } } })))
          .entities,
      ),
  })
}
