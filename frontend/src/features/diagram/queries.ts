import { useQuery } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'

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

export function useDiagram(id: string) {
  return useQuery({
    queryKey: diagramKeys.detail(id),
    queryFn: () => unwrap(client.GET('/api/v1/diagrams/{id}', { params: { path: { id } } })),
    enabled: Boolean(id),
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
