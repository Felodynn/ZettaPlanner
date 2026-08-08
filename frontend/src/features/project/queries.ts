import { useQuery } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'

export const projectKeys = {
  all: ['projects'] as const,
  list: () => [...projectKeys.all, 'list'] as const,
  detail: (id: string) => [...projectKeys.all, id] as const,
  members: (id: string) => [...projectKeys.all, id, 'members'] as const,
  ownership: (id: string) => [...projectKeys.all, id, 'field-ownership'] as const,
}

export function useProjects() {
  return useQuery({
    queryKey: projectKeys.list(),
    queryFn: async () => list((await unwrap(client.GET('/api/v1/projects', {}))).projects),
  })
}

export function useProject(projectId: string) {
  return useQuery({
    queryKey: projectKeys.detail(projectId),
    queryFn: () =>
      unwrap(client.GET('/api/v1/projects/{pid}', { params: { path: { pid: projectId } } })),
  })
}

export function useMembers(projectId: string) {
  return useQuery({
    queryKey: projectKeys.members(projectId),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/members', { params: { path: { pid: projectId } } }),
          )
        ).members,
      ),
  })
}

/**
 * The field-ownership table as this caller sees it (§4.2).
 *
 * Fetched once per project and cached hard: it is computed from the caller's
 * role and does not move while they are looking at it. frontend-notes §4 is
 * emphatic that it is a hint for rendering and never a control — the write is
 * checked again on the server, and a disabled button is a courtesy.
 */
export function useFieldOwnership(projectId: string) {
  return useQuery({
    queryKey: projectKeys.ownership(projectId),
    queryFn: () =>
      unwrap(
        client.GET('/api/v1/projects/{pid}/field-ownership', {
          params: { path: { pid: projectId } },
        }),
      ),
    staleTime: 5 * 60_000,
  })
}
