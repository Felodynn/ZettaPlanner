import { Link, useNavigate, useParams } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, FileText, ImageIcon, Upload } from 'lucide-react'
import { Uploader } from './Uploader'
import { useIngests, useUploadIngest } from './queries'
import type { IngestSummary } from './model'
import { Badge, Card, Loading, type Tone } from '@/components/ui/primitives'
import { Spinner } from '@/components/ui/primitives'
import { notifyError } from '@/components/notify'
import { formatRelative } from '@/lib/format'

const STATUS_TONE: Record<IngestSummary['status'], Tone> = {
  uploaded: 'info',
  extracting: 'info',
  merging: 'info',
  review: 'warn',
  accepted: 'good',
  failed: 'danger',
}

const STATUS_WORD: Record<IngestSummary['status'], string> = {
  uploaded: 'queued',
  extracting: 'reading pages',
  merging: 'merging pages',
  review: 'waiting for review',
  accepted: 'accepted',
  failed: 'failed',
}

export function IngestList() {
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const ingests = useIngests(projectId)
  const upload = useUploadIngest(projectId)

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <Upload className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Ingest</h1>
      </div>
      <p className="mt-0.5 text-ink-muted">
        {/* §3.4a. The sentence a reviewer needs to have read before they start. */}
        A drawing is evidence, not a statement. Nothing here writes to a diagram — reading a page
        produces something to review, and accepting it is the write. Accept is additive: a
        photograph says these things exist, never that everything else does not.
      </p>

      <div className="mt-4">
        <Uploader
          uploading={upload.isPending}
          onUpload={(files) => {
            upload.mutate(files, {
              onSuccess: (created) => navigate(`/p/${projectId}/ingest/${created.id}`),
              onError: (e) => notifyError(e),
            })
          }}
        />
      </div>

      <h2 className="mt-6 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
        Earlier uploads
      </h2>

      {ingests.isPending ? (
        <Loading what="earlier uploads" />
      ) : ingests.error ? (
        <p className="py-4 text-ink-muted">{ingests.error.message}</p>
      ) : ingests.data.length === 0 ? (
        <p className="py-4 text-ink-muted">
          Nothing has been ingested into this project yet. The uploaded bytes of anything you send
          are kept unmodified and forever, so an extraction that turns out wrong can always be
          checked against what was actually on the paper.
        </p>
      ) : (
        <div className="mt-2 flex flex-col gap-2">
          {ingests.data.map((i) => (
            <Link key={i.id} to={`/p/${projectId}/ingest/${i.id}`}>
              <Card className="flex items-center gap-3 px-4 py-3 transition-colors hover:border-accent-line hover:bg-accent-soft/40">
                {i.source_kind === 'pdf' ? (
                  <FileText className="h-4 w-4 shrink-0 text-ink-faint" />
                ) : (
                  <ImageIcon className="h-4 w-4 shrink-0 text-ink-faint" />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-ink">
                    {i.source_filename || 'untitled drawing'}
                  </span>
                  <span className="block text-[11px] text-ink-faint">
                    {i.page_count} page{i.page_count === 1 ? '' : 's'} ·{' '}
                    {formatRelative(i.created_at)}
                  </span>
                </span>

                {i.status === 'accepted' ? (
                  <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-good" />
                ) : i.status === 'failed' ? (
                  <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-danger" />
                ) : i.status !== 'review' ? (
                  <Spinner className="h-3.5 w-3.5" />
                ) : null}
                <Badge tone={STATUS_TONE[i.status]}>{STATUS_WORD[i.status]}</Badge>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
