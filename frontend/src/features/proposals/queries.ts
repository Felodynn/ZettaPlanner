import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { ProposalStatus } from '@/api/vocabulary'
import { notifyError } from '@/components/notify'
import type { Proposal, Reevaluation } from './model'

export const proposalKeys = {
  root: (pid: string) => ['proposals', pid] as const,
  list: (pid: string, status: ProposalStatus | 'all') => ['proposals', pid, status] as const,
  detail: (pid: string, id: string) => ['proposals', pid, 'detail', id] as const,
}

export function useProposals(pid: string, status: ProposalStatus | 'all') {
  return useQuery({
    queryKey: proposalKeys.list(pid, status),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/proposals', {
              params: { path: { pid }, ...(status === 'all' ? {} : { query: { status } }) },
            }),
          )
        ).proposals,
      ),
  })
}

/**
 * One proposal, with its **current** conflicts.
 *
 * frontend-notes §8: conflicts are computed on read, so a proposal that was
 * clean when the queue loaded may not be clean now. `staleTime: 0` and a
 * refetch on mount are the whole point — showing an approve button computed
 * from a cached read is showing a button that is about to 409.
 */
export function useProposal(pid: string, id: string) {
  return useQuery({
    queryKey: proposalKeys.detail(pid, id),
    queryFn: () => unwrap(client.GET('/api/v1/proposals/{id}', { params: { path: { id } } })),
    enabled: Boolean(id),
    staleTime: 0,
    refetchOnMount: 'always',
  })
}

export function useApproveProposal(pid: string) {
  const qc = useQueryClient()
  return useMutation<Proposal, Error, string>({
    mutationFn: (id) =>
      unwrap(client.POST('/api/v1/proposals/{id}/approve', { params: { path: { id } } })),
    onSuccess: (_p, id) => {
      void qc.invalidateQueries({ queryKey: proposalKeys.root(pid) })
      void qc.invalidateQueries({ queryKey: proposalKeys.detail(pid, id) })
      // An approved proposal lands as one revision against a diagram or the plan.
      void qc.invalidateQueries({ queryKey: ['diagrams'] })
      void qc.invalidateQueries({ queryKey: ['plan', pid] })
      void qc.invalidateQueries({ queryKey: ['dashboard', pid] })
      void qc.invalidateQueries({ queryKey: ['warnings', pid] })
      void qc.invalidateQueries({ queryKey: ['notes', pid] })
    },
    onError: (e, id) => {
      // Reported here as well as rendered on the page.
      //
      // Defining `onError` at all opts a mutation out of the global error
      // surface — the mutation cache skips its toast for anything that handles
      // its own errors. Adding this handler for the refetch below therefore
      // silenced every failed approval: the server refused with a 400 naming
      // the field, and the button looked like it did nothing at all.
      notifyError(e)

      // A 409 is terminal for this proposal: the status is now `superseded` and
      // nothing was applied. Refetching is how the screen stops offering to
      // approve something that no longer can be.
      void qc.invalidateQueries({ queryKey: proposalKeys.detail(pid, id) })
      void qc.invalidateQueries({ queryKey: proposalKeys.root(pid) })
    },
  })
}

/**
 * Rejecting.
 *
 * Open to any project member (§5.1): refusing changes nothing, and requiring a
 * manager to be present to record a reason is how the reason stops being
 * recorded.
 */
export function useRejectProposal(pid: string) {
  const qc = useQueryClient()
  return useMutation<Proposal, Error, { id: string; rationale: string }>({
    mutationFn: ({ id, rationale }) =>
      unwrap(
        client.POST('/api/v1/proposals/{id}/reject', {
          params: { path: { id } },
          body: { rationale },
        }),
      ),
    onSuccess: (_p, { id }) => {
      void qc.invalidateQueries({ queryKey: proposalKeys.root(pid) })
      void qc.invalidateQueries({ queryKey: proposalKeys.detail(pid, id) })
    },
  })
}

/**
 * Re-evaluate: ask the vision model to review a hand-edited diagram.
 *
 * **It returns a Proposal, never a write** (§5.4). Synchronous, bounded to a
 * small number of pages, and what it skipped is named in the response — "we
 * re-read it" must never quietly mean "we re-read some of it".
 */
export function useReevaluate(pid: string) {
  const qc = useQueryClient()
  return useMutation<Reevaluation, Error, string>({
    mutationFn: (diagramId) =>
      unwrap(
        client.POST('/api/v1/diagrams/{id}/reevaluate', { params: { path: { id: diagramId } } }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: proposalKeys.root(pid) })
    },
  })
}
