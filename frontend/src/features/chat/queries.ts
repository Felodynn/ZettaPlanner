import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

export type Message = components['schemas']['MessageSummary']
export type Turn = components['schemas']['TurnSummary']
export type Thread = components['schemas']['ThreadSummary']

export const threadKeys = {
  root: ['threads'] as const,
  list: (projectId: string) => ['threads', projectId] as const,
  /** Every message list, for the coarse invalidation the events drive. */
  allMessages: () => ['messages'] as const,
  messages: (threadId: string) => ['messages', threadId] as const,
  turns: (projectId: string) => ['turns', projectId] as const,
}

export function useThreads(projectId: string) {
  return useQuery({
    queryKey: threadKeys.list(projectId),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/threads', { params: { path: { pid: projectId } } }),
          )
        ).threads,
      ),
  })
}

/**
 * The project thread, opened on first use.
 *
 * Idempotent by contract (§3.6): there is exactly one thread per scope, and
 * asking twice returns the same one. So this is safe to call on every mount
 * rather than guarded behind "does it exist yet".
 */
export function useProjectThread(projectId: string) {
  return useQuery({
    queryKey: [...threadKeys.list(projectId), 'project-scope'],
    queryFn: () =>
      unwrap(
        client.POST('/api/v1/projects/{pid}/threads', {
          params: { path: { pid: projectId } },
          body: { scope_kind: 'project' },
        }),
      ),
    staleTime: Infinity,
  })
}

export function useMessages(threadId: string | undefined) {
  return useQuery({
    queryKey: threadKeys.messages(threadId ?? ''),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/threads/{id}/messages', {
              params: { path: { id: threadId ?? '' }, query: { limit: 500 } },
            }),
          )
        ).messages,
      ),
    enabled: Boolean(threadId),
  })
}

export function useTurns(projectId: string, limit = 20) {
  return useQuery({
    queryKey: threadKeys.turns(projectId),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/turns', {
              params: { path: { pid: projectId }, query: { limit } },
            }),
          )
        ).turns,
      ),
  })
}

/**
 * Posts a message.
 *
 * The message always lands (§5.3): posting and running are separate, so this
 * resolves as soon as the message is written and the turn is queued. The
 * response carries the queue position, which the composer shows — a send
 * button that appears to do nothing is the worst version of a busy queue.
 */
export function usePostMessage(threadId: string | undefined) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (content: string) =>
      unwrap(
        client.POST('/api/v1/threads/{id}/messages', {
          params: { path: { id: threadId ?? '' } },
          body: { content },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: threadKeys.messages(threadId ?? '') })
    },
  })
}

export function useCancelTurn() {
  return useMutation({
    mutationFn: (turnId: string) =>
      client.POST('/api/v1/turns/{id}/cancel', { params: { path: { id: turnId } } }),
  })
}
