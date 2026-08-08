import { useState } from 'react'
import { Bot, ChevronRight, Info, User, Wrench } from 'lucide-react'
import type { Message } from './queries'
import { Badge } from '@/components/ui/primitives'
import { formatTime, formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/cn'

/**
 * One message, always attributed.
 *
 * FRONTEND F1: every message shows who sent it — a human name or an agent
 * name. In a globally visible thread where managers read developers' turns, an
 * unattributed message is unusable, so there is no branch here that renders a
 * body without a name attached to it.
 */

function Avatar({ name, kind }: { name: string; kind: 'user' | 'assistant' }) {
  return (
    <span
      className={cn(
        'flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[10px] font-bold',
        kind === 'assistant' ? 'bg-accent-soft text-accent-ink' : 'bg-sunken text-ink-muted',
      )}
      title={name}
    >
      {kind === 'assistant' ? (
        <Bot className="h-3.5 w-3.5" />
      ) : (
        (name.slice(0, 1).toUpperCase() ?? <User className="h-3.5 w-3.5" />)
      )}
    </span>
  )
}

function ToolMessage({ message }: { message: Message }) {
  const [open, setOpen] = useState(false)
  const calls = message.tool_calls ?? []

  return (
    <div className="px-4 py-1">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left transition-colors hover:bg-sunken"
      >
        <ChevronRight
          className={cn('h-3 w-3 shrink-0 text-ink-faint transition-transform', open && 'rotate-90')}
        />
        <Wrench className="h-3 w-3 shrink-0 text-ink-faint" />
        <span className="flex flex-wrap items-center gap-1.5 text-xs text-ink-muted">
          {calls.length === 0 ? (
            <span className="font-mono">tool result</span>
          ) : (
            calls.map((c, i) => (
              <span key={`${c.name}-${i}`} className="inline-flex items-center gap-1">
                <span className="font-mono text-ink-muted">{c.name}</span>
                {c.outcome ? (
                  <Badge
                    tone={
                      c.outcome === 'ok'
                        ? 'good'
                        : c.outcome === 'denied'
                          ? 'warn'
                          : c.outcome === 'error'
                            ? 'danger'
                            : 'neutral'
                    }
                  >
                    {c.outcome}
                  </Badge>
                ) : null}
              </span>
            ))
          )}
        </span>
      </button>
      {open ? (
        <pre className="mt-1 ml-6 max-h-80 overflow-auto rounded border border-line bg-sunken px-2.5 py-2 font-mono text-[11px] whitespace-pre-wrap text-ink-muted zp-scroll">
          {message.content}
        </pre>
      ) : null}
    </div>
  )
}

export function MessageRow({ message }: { message: Message }) {
  if (message.role === 'tool') return <ToolMessage message={message} />

  if (message.role === 'system') {
    return (
      <div className="px-4 py-1.5">
        <div className="flex items-start gap-2 rounded-md border border-line bg-sunken px-2.5 py-1.5 text-ink-muted">
          <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-faint" />
          <span className="min-w-0 flex-1 whitespace-pre-wrap">{message.content}</span>
          <span className="shrink-0 text-[11px] text-ink-faint" title={message.created_at}>
            {formatTime(message.created_at)}
          </span>
        </div>
      </div>
    )
  }

  const isAgent = message.role === 'assistant'
  const name = message.actor_name ?? (isAgent ? 'Planner' : 'Unknown actor')

  return (
    <div className="flex gap-2.5 px-4 py-2">
      <Avatar name={name} kind={isAgent ? 'assistant' : 'user'} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="font-semibold text-ink">{name}</span>
          {isAgent ? (
            <span className="text-[11px] text-ink-faint">agent</span>
          ) : null}
          <span className="text-[11px] text-ink-faint" title={formatTimestamp(message.created_at)}>
            {formatTime(message.created_at)}
          </span>
        </div>
        <div className="mt-0.5 wrap-break-word whitespace-pre-wrap text-ink">{message.content}</div>
      </div>
    </div>
  )
}

/** The assistant's answer while it is still arriving, before any message exists. */
export function StreamingRow({ text, agent }: { text: string; agent: string }) {
  return (
    <div className="flex gap-2.5 px-4 py-2">
      <Avatar name={agent} kind="assistant" />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="font-semibold text-ink">{agent}</span>
          <span className="text-[11px] text-ink-faint">writing…</span>
        </div>
        <div className="mt-0.5 wrap-break-word whitespace-pre-wrap text-ink">
          {text}
          <span className="ml-0.5 inline-block h-3.5 w-1.5 translate-y-0.5 animate-pulse bg-accent" />
        </div>
      </div>
    </div>
  )
}
