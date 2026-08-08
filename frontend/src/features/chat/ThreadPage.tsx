import { useParams } from 'react-router-dom'
import { MessagesSquare, Wifi, WifiOff } from 'lucide-react'
import { useSession } from '@/auth/session'
import { EmptyState, Loading } from '@/components/ui/primitives'
import { useProjectEvents } from '@/events/ProjectEvents'
import { useStream } from '@/events/streamStore'
import { Composer } from './Composer'
import { MessageList } from './MessageList'
import { TurnBanner } from './TurnBanner'
import { useMessages, useProjectThread, useTurns } from './queries'
import { cn } from '@/lib/cn'

function ConnectionDot() {
  const { state, resyncedAt } = useProjectEvents()

  const label =
    state === 'open'
      ? 'Live'
      : state === 'connecting'
        ? 'Connecting…'
        : state === 'reconnecting'
          ? 'Reconnecting…'
          : 'Disconnected'

  return (
    <span
      className="flex items-center gap-1.5 text-[11px] text-ink-faint"
      title={
        state === 'open'
          ? resyncedAt
            ? 'Live. The stream fell behind the server’s replay window once and everything was refetched.'
            : 'Live. Changes from other people arrive as they happen.'
          : 'Changes from other people are not arriving. The view may be stale.'
      }
    >
      {state === 'open' ? (
        <Wifi className="h-3 w-3 text-good" />
      ) : (
        <WifiOff className={cn('h-3 w-3', state === 'closed' ? 'text-danger' : 'text-warn')} />
      )}
      {label}
    </span>
  )
}

export function ThreadPage() {
  const { projectId = '' } = useParams()
  const { user } = useSession()

  const thread = useProjectThread(projectId)
  const threadId = thread.data?.id
  const messages = useMessages(threadId)
  const turns = useTurns(projectId)
  const streams = useStream((s) => s.text)
  const settling = useStream((s) => s.settling)

  // The agent names itself in the transcript; the fallback only shows before
  // it has ever spoken here.
  const agentName =
    [...(messages.data ?? [])].reverse().find((m) => m.role === 'assistant')?.actor_name ??
    'planner'

  // The turn that owns the project's single slot, if any (§5.3).
  const active = turns.data?.find((t) => t.status === 'running' || t.status === 'queued')
  const latest = turns.data?.[0]
  const banner = active ?? latest

  if (thread.isPending) return <Loading what="the thread" />
  if (thread.error) throw thread.error

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-4">
        <MessagesSquare className="h-4 w-4 text-ink-faint" />
        <h1 className="font-semibold text-ink">Project thread</h1>
        <span
          className="text-[11px] text-ink-faint"
          title="One thread per project, readable by every member — including managers reading developers' turns. Scope controls context assembly and placement, never visibility (§3.6)."
        >
          visible to every member
        </span>
        <div className="flex-1" />
        <ConnectionDot />
      </header>

      {messages.isPending ? (
        <Loading what="the transcript" />
      ) : messages.error ? (
        <EmptyState title="The transcript could not be loaded" body={messages.error.message} />
      ) : messages.data.length === 0 ? (
        <div className="flex min-h-0 flex-1 items-center">
          <EmptyState
            className="w-full"
            icon={<MessagesSquare className="h-7 w-7" />}
            title="Nothing here yet"
            body="Ask the planner about the project, or write something the rest of the team should see. Every message is attributed and everybody on the project can read it."
          />
        </div>
      ) : (
        <MessageList
          messages={messages.data}
          streams={streams}
          settling={settling}
          agentName={agentName}
        />
      )}

      {banner ? (
        <TurnBanner turn={banner} isMine={banner.requested_by === user.display_name} />
      ) : null}

      <Composer threadId={threadId} active={active} />
    </div>
  )
}
