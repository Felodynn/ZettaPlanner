import { useQuery } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'

export interface AuditFilter {
  subject?: string
  subjectId?: string
  actor?: string
  action?: string
  since?: string
  until?: string
  limit?: number
}

export const auditKeys = {
  root: (pid: string) => ['audit', pid] as const,
  list: (pid: string, f: AuditFilter) => ['audit', pid, f] as const,
}

/**
 * The log, filtered.
 *
 * Bounded by `limit` rather than paged: audit is one of the few endpoints the
 * backend bounds at all (frontend-notes §12), and a view that silently fetched
 * everything would be fine on this project and fatal on a year-old one.
 *
 * `since`/`until` are RFC3339 instants — timestamps, not calendar days — so the
 * caller converts a picked day into a zoned instant rather than passing the day
 * through and hoping.
 */
export function useAudit(pid: string, filter: AuditFilter) {
  return useQuery({
    queryKey: auditKeys.list(pid, filter),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/audit', {
              params: {
                path: { pid },
                query: {
                  ...(filter.subject ? { subject: filter.subject } : {}),
                  ...(filter.subjectId ? { subject_id: filter.subjectId } : {}),
                  ...(filter.actor ? { actor: filter.actor } : {}),
                  ...(filter.action ? { action: filter.action } : {}),
                  ...(filter.since ? { since: filter.since } : {}),
                  ...(filter.until ? { until: filter.until } : {}),
                  limit: filter.limit ?? 200,
                },
              },
            }),
          )
        ).entries,
      ),
  })
}
