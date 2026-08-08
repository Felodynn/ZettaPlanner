import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { AcceptResponse, IngestView, Resolution } from './model'
import { isTerminal, isWorking } from './model'

/**
 * The ingest queries.
 *
 * Keyed under the project so the SSE handler's coarse
 * `invalidateQueries(['ingests', projectId])` reaches the detail as well as
 * the list.
 */
export const ingestKeys = {
  root: ['ingests'] as const,
  list: (pid: string) => ['ingests', pid] as const,
  detail: (pid: string, id: string) => ['ingests', pid, 'detail', id] as const,
}

/**
 * How often to ask, given that nobody is looking.
 *
 * React Query pauses a `refetchInterval` while the document is hidden unless
 * told otherwise, and this app turns focus refetching off globally because SSE
 * is the freshness signal everywhere else. Ingest has no such signal, and an
 * extraction runs for minutes — which is exactly long enough for somebody to
 * switch tabs. Left alone, they come back to a screen that still says "reading
 * the pages" about an ingest that finished four minutes ago.
 *
 * So the poll continues in the background, slowly. A hidden tab is not being
 * read, so the only thing a fast poll buys there is load.
 */
function pollEvery(visibleMs: number): number {
  return typeof document !== 'undefined' && document.visibilityState === 'hidden'
    ? 15_000
    : visibleMs
}

export function useIngests(pid: string) {
  return useQuery({
    queryKey: ingestKeys.list(pid),
    queryFn: async () =>
      list(
        (await unwrap(client.GET('/api/v1/projects/{pid}/ingests', { params: { path: { pid } } })))
          .ingests,
      ),
    // Extraction is minutes of model time and produces no event this client can
    // see (see useIngest). While anything is still running, the list refreshes;
    // once nothing is, it stops entirely.
    refetchInterval: (q) => (q.state.data?.some((i) => isWorking(i.status)) ? pollEvery(4000) : false),
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: true,
  })
}

/**
 * One ingest, polled while it is working.
 *
 * CONTRACT §7.2 lists an `ingest.progress` event and the backend does not emit
 * one — its own documentation says to poll `GET /ingests/{id}` for progress,
 * and no ingest path publishes to the event hub. So progress is polled here,
 * deliberately and only while there is progress to see. The SSE handler
 * already invalidates `['ingests', pid]` on every `ingest.*` name, so if the
 * event lands later this becomes a redundant safety net rather than a rewrite.
 *
 * Two seconds: a page takes tens of seconds to read, so this is about the
 * *page counter* moving visibly rather than about latency. It stops dead on a
 * terminal status.
 */
export function useIngest(pid: string, id: string) {
  return useQuery({
    queryKey: ingestKeys.detail(pid, id),
    queryFn: () => unwrap(client.GET('/api/v1/ingests/{id}', { params: { path: { id } } })),
    enabled: Boolean(id),
    refetchInterval: (q) => {
      const status = q.state.data?.status
      return status && isWorking(status) ? pollEvery(2000) : false
    },
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: true,
  })
}

/**
 * The upload.
 *
 * One ingest is one document: a PDF and loose photographs in the same upload
 * would be merged across drawings that were never on the same desk, and the
 * backend refuses it. The refusal is mirrored in the picker so nobody
 * assembles a batch that cannot be sent.
 */
export function useUploadIngest(pid: string) {
  const qc = useQueryClient()
  return useMutation<IngestView, Error, File[]>({
    mutationFn: async (files) =>
      unwrap(
        client.POST('/api/v1/projects/{pid}/ingests', {
          params: { path: { pid } },
          // openapi-typescript renders a binary multipart part as `string`,
          // which no browser file is. The cast is at this one seam and the
          // FormData below is what actually goes on the wire.
          body: { files: files as unknown as string[] },
          bodySerializer: (body?: { files: string[] }) => {
            const form = new FormData()
            for (const f of (body?.files ?? []) as unknown as File[]) form.append('files', f, f.name)
            return form
          },
          // Unset the client-wide JSON content type so the browser writes the
          // multipart boundary itself. A hand-written multipart header has no
          // boundary in it and the request arrives as an unparseable body.
          headers: { 'Content-Type': null },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ingestKeys.list(pid) })
    },
  })
}

/**
 * Re-read one page.
 *
 * Per page because failure is per page (§3.4a). Re-running the whole ingest to
 * fix the one page that was out of focus costs a model call for every page and
 * throws away extractions somebody has already reviewed — and it does it
 * without re-uploading anything, which is the point.
 */
export function useRetryPage(pid: string, id: string) {
  const qc = useQueryClient()
  return useMutation<IngestView, Error, number>({
    mutationFn: (idx) =>
      unwrap(
        client.POST('/api/v1/ingests/{id}/pages/{idx}/retry', { params: { path: { id, idx } } }),
      ),
    onSuccess: (fresh) => {
      qc.setQueryData(ingestKeys.detail(pid, id), fresh)
      void qc.invalidateQueries({ queryKey: ingestKeys.list(pid) })
    },
  })
}

export interface AcceptArgs {
  diagramId?: string
  name?: string
  slug?: string
  level?: 'context' | 'container' | 'component'
  summary?: string
  resolutions: Resolution[]
}

/** The write. Everything before it was evidence gathering. */
export function useAcceptIngest(pid: string, id: string) {
  const qc = useQueryClient()
  return useMutation<AcceptResponse, Error, AcceptArgs>({
    mutationFn: (args) =>
      unwrap(
        client.POST('/api/v1/ingests/{id}/accept', {
          params: { path: { id } },
          body: {
            ...(args.diagramId ? { diagram_id: args.diagramId } : {}),
            ...(args.name ? { name: args.name } : {}),
            ...(args.slug ? { slug: args.slug } : {}),
            ...(args.level ? { level: args.level } : {}),
            ...(args.summary ? { summary: args.summary } : {}),
            resolutions: args.resolutions,
          },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ingestKeys.list(pid) })
      void qc.invalidateQueries({ queryKey: ingestKeys.detail(pid, id) })
      // Accept is additive against a diagram, and it raises notes.
      void qc.invalidateQueries({ queryKey: ['diagrams'] })
      void qc.invalidateQueries({ queryKey: ['notes', pid] })
    },
  })
}

/** The page image the model was actually shown. */
export function pageImageUrl(id: string, idx: number): string {
  return `/api/v1/ingests/${id}/pages/${idx}/image`
}

export { isTerminal, isWorking }
