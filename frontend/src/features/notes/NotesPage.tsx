import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import {
  CircleSlash,
  Copy,
  FileStack,
  GitMerge,
  HelpCircle,
  Layers,
  Link2Off,
  Check,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { useNotes, useSetNoteStatus, type NoteView } from './queries'
import { NOTE_STATUS_VALUES, type NoteKind, type NoteStatus } from '@/api/vocabulary'
import { Badge, Card, EmptyState, Loading, type Tone } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { formatRelative } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * Notes — the clarifications nobody merged away.
 *
 * §3.4: entity resolution is **not automatic**. Where a name plausibly matches
 * something that already exists, the backend records a question instead of
 * making the call; an accepted ingest records one for every arrow that ran off
 * a page and found nothing, and one for every disagreement between pages.
 *
 * Every one of those is a decision deferred to a person. That only works if the
 * person can find them, which is what this screen is for.
 */

interface KindSpec {
  label: string
  Icon: LucideIcon
  means: string
}

const KIND: Record<NoteKind, KindSpec> = {
  overlap: {
    label: 'Possible duplicate',
    Icon: Copy,
    means: 'Two names that may be the same thing. Never merged automatically — a wrong merge is invisible afterwards.',
  },
  conflict: {
    label: 'Disagreement',
    Icon: GitMerge,
    means: 'Two sources said different things. Recorded rather than settled, because only somebody who knows the system can say.',
  },
  missing: {
    label: 'Something missing',
    Icon: Link2Off,
    means: 'A reference with nothing at the far end — most often an arrow read off a drawing that pointed at a box nobody drew.',
  },
  clarification: {
    label: 'Worth checking',
    Icon: HelpCircle,
    means: 'Accepted as it stands, and flagged. Usually a low-confidence reading nobody has verified.',
  },
}

const STATUS_TONE: Record<NoteStatus, Tone> = {
  open: 'warn',
  resolved: 'good',
  dismissed: 'neutral',
}

type Filter = NoteStatus | 'all'

export function NotesPage() {
  const { projectId = '' } = useParams()
  // Deep-linkable, like the plan filters (F2): "the seven open questions on
  // this project" is a link somebody wants to send.
  const [params, setParams] = useSearchParams()
  const raw = params.get('status') ?? 'open'
  const filter: Filter = (NOTE_STATUS_VALUES as readonly string[]).includes(raw)
    ? (raw as NoteStatus)
    : 'all'

  const notes = useNotes(projectId, filter)
  const setStatus = useSetNoteStatus(projectId)
  const [busy, setBusy] = useState<string | undefined>()

  const setFilter = (next: Filter) => {
    const p = new URLSearchParams(params)
    p.set('status', next)
    setParams(p, { replace: true })
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <FileStack className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Notes</h1>
      </div>
      <p className="mt-0.5 text-ink-muted">
        Questions the system refused to answer on its own. A name that plausibly matches an existing
        one is recorded here rather than merged, and an arrow with nothing at its far end is kept
        here rather than dropped — a merge you did not make is recoverable, and a dependency nobody
        noticed is not.
      </p>

      <div className="mt-4 flex items-center gap-1">
        {(['open', 'resolved', 'dismissed', 'all'] as const).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => setFilter(f)}
            className={cn(
              'rounded-md px-2.5 py-1 transition-colors',
              filter === f
                ? 'bg-accent-soft font-medium text-accent-ink'
                : 'text-ink-muted hover:bg-sunken hover:text-ink',
            )}
          >
            {f === 'all' ? 'All' : f[0]?.toUpperCase() + f.slice(1)}
          </button>
        ))}
      </div>

      {notes.isPending ? (
        <Loading what="notes" />
      ) : notes.error ? (
        <p className="py-4 text-ink-muted">{notes.error.message}</p>
      ) : notes.data.length === 0 ? (
        <EmptyState
          className="pt-16"
          icon={<FileStack className="h-7 w-7" />}
          title={filter === 'open' ? 'Nothing is waiting on you' : `No ${filter} notes`}
          body={
            filter === 'open'
              ? 'Notes appear when the system declines to make a judgement for you — accepting a drawing, or an agent proposing something that looks like a duplicate.'
              : undefined
          }
        />
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {notes.data.map((n) => (
            <NoteCard
              key={n.id}
              projectId={projectId}
              note={n}
              busy={busy === n.id && setStatus.isPending}
              onStatus={(status) => {
                setBusy(n.id)
                setStatus.mutate({ id: n.id, status })
              }}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function NoteCard({
  projectId,
  note,
  busy,
  onStatus,
}: {
  projectId: string
  note: NoteView
  busy: boolean
  onStatus: (status: NoteStatus) => void
}) {
  const spec = KIND[note.kind]
  const open = note.status === 'open'

  return (
    <Card className={cn('px-4 py-3', !open && 'opacity-70')}>
      <div className="flex items-start gap-2.5">
        <spec.Icon className="mt-0.5 h-4 w-4 shrink-0 text-ink-faint" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-medium text-ink">{spec.label}</span>
            <Badge tone={STATUS_TONE[note.status]}>{note.status}</Badge>
            <Subject projectId={projectId} note={note} />
            <span className="text-[11px] text-ink-faint">{formatRelative(note.created_at)}</span>
          </div>

          {/* The backend's sentence. It was written to be read by a person and
              by a model, and it names the pages, the values and the reason —
              a paraphrase would throw all three away. */}
          <p className="mt-1 wrap-break-word text-ink">{note.body}</p>

          <p className="mt-1 text-[11px] leading-snug text-ink-faint">{spec.means}</p>
        </div>

        {open ? (
          <div className="flex shrink-0 flex-col gap-1">
            {/* A verb, not the state's name. The filter above reads
                "Resolved"; a button that also reads "Resolved" is a state and
                an action wearing the same word, and the one that gets clicked
                by accident is the one that writes. */}
            <Button
              size="sm"
              variant="secondary"
              loading={busy}
              onClick={() => onStatus('resolved')}
              title="Somebody dealt with this."
            >
              <Check className="h-3.5 w-3.5" />
              Resolve
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => onStatus('dismissed')}
              title="This did not need dealing with. Kept, and distinct from resolved."
            >
              <CircleSlash className="h-3.5 w-3.5" />
              Dismiss
            </Button>
          </div>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onStatus('open')}>
            Reopen
          </Button>
        )}
      </div>
    </Card>
  )
}

/**
 * What the note is about, as a link where one can be made.
 *
 * `subject_slug` is a catalog entity for anything ingest raised, and the entity
 * page is where somebody actually settles the question.
 */
function Subject({ projectId, note }: { projectId: string; note: NoteView }) {
  if (!note.subject_slug && !note.subject_id) return null

  const label = note.subject_slug ?? note.subject_kind
  if (note.subject_kind === 'catalog_entity' && note.subject_id) {
    return (
      <Link
        to={`/p/${projectId}/diagrams`}
        className="inline-flex items-center gap-1 rounded border border-line-strong px-1.5 py-0.5 font-mono text-[11px] text-ink-muted hover:bg-sunken hover:text-ink"
        title={`${note.subject_kind} · ${label}`}
      >
        <Layers className="h-3 w-3" />
        {label}
      </Link>
    )
  }
  return (
    <span className="rounded border border-line-strong px-1.5 py-0.5 font-mono text-[11px] text-ink-muted">
      {label}
    </span>
  )
}
