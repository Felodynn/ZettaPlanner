import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import type { NoteStatus } from '@/api/vocabulary'

export type NoteView = components['schemas']['NoteView']

export const noteKeys = {
  root: (pid: string) => ['notes', pid] as const,
  list: (pid: string, status: NoteStatus | 'all') => ['notes', pid, status] as const,
}

/**
 * Notes: the clarification channel (§3.4).
 *
 * Entity resolution is never automatic. When a name plausibly matches
 * something that already exists, the backend records a note rather than
 * merging — and an accepted ingest records one for every arrow it could not
 * place and every disagreement between pages. They are questions somebody has
 * to answer, and until this screen existed they were written and never read.
 */
export function useNotes(pid: string, status: NoteStatus | 'all') {
  return useQuery({
    queryKey: noteKeys.list(pid, status),
    queryFn: async () =>
      list(
        (
          await unwrap(
            client.GET('/api/v1/projects/{pid}/notes', {
              params: {
                path: { pid },
                ...(status === 'all' ? {} : { query: { status } }),
              },
            }),
          )
        ).notes,
      ),
  })
}

/**
 * Resolve or dismiss.
 *
 * Two different statements, and the difference is worth keeping: `resolved`
 * means somebody dealt with it, `dismissed` means somebody decided it did not
 * need dealing with. Collapsing them into "done" loses the second, which is
 * the one you want when the same question comes back.
 */
export function useSetNoteStatus(pid: string) {
  const qc = useQueryClient()
  return useMutation<NoteView, Error, { id: string; status: NoteStatus }>({
    mutationFn: ({ id, status }) =>
      unwrap(
        client.PATCH('/api/v1/notes/{id}', { params: { path: { id } }, body: { status } }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: noteKeys.root(pid) })
    },
  })
}
