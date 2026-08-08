import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

export const diagramKeys = {
  root: ['diagrams'] as const,
  list: (pid: string) => ['diagrams', pid] as const,
  detail: (id: string) => ['diagrams', 'detail', id] as const,
  yaml: (id: string) => ['diagrams', 'yaml', id] as const,
  revisions: (id: string) => ['diagrams', 'revisions', id] as const,
  entity: (id: string) => ['entity', id] as const,
}

export function useDiagrams(pid: string) {
  return useQuery({
    queryKey: diagramKeys.list(pid),
    queryFn: async () =>
      list(
        (await unwrap(client.GET('/api/v1/projects/{pid}/diagrams', { params: { path: { pid } } })))
          .diagrams,
      ),
  })
}

/**
 * One diagram.
 *
 * `fresh` forces a refetch on mount, and the editor uses it. A working copy
 * has to be seeded from **the state the lease was taken against** — arriving
 * from the viewer means the cached copy is whatever was true when the viewer
 * loaded, and seeding from that hands you a stale `base_revision` and a 409 on
 * the first save, before you have typed anything.
 */
export function useDiagram(id: string, fresh = false) {
  return useQuery({
    queryKey: diagramKeys.detail(id),
    queryFn: () => unwrap(client.GET('/api/v1/diagrams/{id}', { params: { path: { id } } })),
    enabled: Boolean(id),
    ...(fresh ? { refetchOnMount: 'always' as const, staleTime: 0 } : {}),
  })
}

/**
 * The canonical serialization (§6.2).
 *
 * The same bytes the model context and the git projection are built from, so
 * a reader can see exactly what a model would. Read-only: it has no
 * coordinates in it, structurally (D5), and there is nothing here to edit.
 */
export function useDiagramYaml(id: string, enabled: boolean) {
  return useQuery({
    queryKey: diagramKeys.yaml(id),
    queryFn: async () => {
      const res = await client.GET('/api/v1/diagrams/{id}/yaml', {
        params: { path: { id } },
        parseAs: 'text',
      })
      if (res.error) throw new Error('Could not read the serialization')
      return String(res.data)
    },
    enabled: enabled && Boolean(id),
  })
}

export function useRevisions(id: string, enabled: boolean) {
  return useQuery({
    queryKey: diagramKeys.revisions(id),
    queryFn: async () =>
      list(
        (await unwrap(client.GET('/api/v1/diagrams/{id}/revisions', { params: { path: { id } } })))
          .revisions,
      ),
    enabled: enabled && Boolean(id),
  })
}

export function useEntity(id: string | undefined) {
  return useQuery({
    queryKey: diagramKeys.entity(id ?? ''),
    queryFn: () =>
      unwrap(client.GET('/api/v1/entities/{id}', { params: { path: { id: id ?? '' } } })),
    enabled: Boolean(id),
  })
}

// ── writes ───────────────────────────────────────────────────────────────────

/**
 * A new, empty diagram.
 *
 * `level` is required and has no sensible default: a diagram drawn at the wrong
 * altitude is mislabelled permanently, and the three levels answer genuinely
 * different questions. The slug is derived from the name once and never
 * follows it afterwards, so a rename is not a delete plus an add.
 */
export function useCreateDiagram(pid: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: components['schemas']['Create-diagramRequest']) =>
      unwrap(client.POST('/api/v1/projects/{pid}/diagrams', { params: { path: { pid } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: diagramKeys.list(pid) }),
  })
}

/**
 * Discard every hand-set coordinate and go back to computed layout.
 *
 * Destructive and irreversible: positions are not versioned, so the
 * arrangement somebody spent an afternoon on is gone. The backend requires
 * `confirm: true` for exactly that reason, and the UI asks in words rather
 * than passing the flag on a single click.
 */
export function useResetLayout(diagramId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const res = await client.POST('/api/v1/diagrams/{id}/layout/reset', {
        params: { path: { id: diagramId } },
        body: { confirm: true },
      })
      if (res.error) throw res.error
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: diagramKeys.detail(diagramId) })
      void qc.invalidateQueries({ queryKey: diagramKeys.root })
    },
  })
}
