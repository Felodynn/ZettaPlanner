import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import type { Message } from './queries'
import { MessageRow, StreamingRow } from './MessageRow'
import { Button } from '@/components/ui/Button'

/**
 * The transcript.
 *
 * Virtualized because F1 requires 500+ messages to scroll without jank, and a
 * long agent turn adds a message per tool call — a busy project reaches four
 * figures inside a week.
 *
 * Rows are measured rather than estimated: a tool result and a one-line
 * question differ by an order of magnitude in height, and a fixed estimate
 * makes the scrollbar lie about where you are.
 */

interface Props {
  messages: Message[]
  /** Assistant text still arriving, keyed by turn id. */
  streams: Record<string, string>
  /** Turns whose text is complete and whose canonical message is being fetched. */
  settling: Record<string, true>
  agentName: string
}

/** How close to the bottom still counts as "following the conversation". */
const STICK_PX = 120

export function MessageList({ messages, streams, settling, agentName }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)

  // In-flight assistant text renders as a trailing row of its own, and stops
  // the moment `message.complete` arrives — `settling` is set then, before the
  // refetch that brings the canonical message.
  //
  // Keying the overlay off "does this turn have a message yet" would be wrong
  // twice over: the prompt that started the turn already carries its id, and a
  // turn writes one assistant message per step, so every step after the first
  // would stream invisibly.
  const pendingStreams = Object.entries(streams).filter(
    ([turnId, text]) => text.length > 0 && !settling[turnId],
  )

  const rowCount = messages.length + pendingStreams.length

  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 72,
    overscan: 12,
    getItemKey: (index) => {
      const m = messages[index]
      if (m) return m.id
      const pending = pendingStreams[index - messages.length]
      return pending ? `stream:${pending[0]}` : `row:${index}`
    },
  })

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
  }, [])

  const onScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    setAtBottom(distance < STICK_PX)
  }, [])

  // Follow the conversation only while the reader is already at the bottom.
  // Yanking someone back down while they are reading history is worse than
  // missing a message, and the button below makes catching up one click.
  const lastKey = `${messages.length}:${messages.at(-1)?.id ?? ''}:${Object.values(streams).join('').length}`
  useLayoutEffect(() => {
    if (atBottom) scrollToBottom()
  }, [lastKey, atBottom, scrollToBottom])

  useEffect(() => {
    scrollToBottom()
    // Only on first paint of a thread.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const items = virtualizer.getVirtualItems()

  return (
    <div className="relative min-h-0 flex-1">
      <div ref={scrollRef} onScroll={onScroll} className="h-full overflow-y-auto zp-scroll">
        <div className="relative w-full" style={{ height: `${virtualizer.getTotalSize()}px` }}>
          {items.map((item) => {
            const message = messages[item.index]
            const pending = pendingStreams[item.index - messages.length]

            return (
              <div
                key={item.key}
                ref={virtualizer.measureElement}
                data-index={item.index}
                className="absolute top-0 left-0 w-full"
                style={{ transform: `translateY(${item.start}px)` }}
              >
                {message ? (
                  <MessageRow message={message} />
                ) : pending ? (
                  <StreamingRow text={pending[1]} agent={agentName} />
                ) : null}
              </div>
            )
          })}
        </div>
      </div>

      {!atBottom ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
          <Button
            variant="secondary"
            size="sm"
            className="pointer-events-auto zp-shadow-pop"
            onClick={() => {
              setAtBottom(true)
              scrollToBottom()
            }}
          >
            <ArrowDown className="h-3.5 w-3.5" />
            Latest
          </Button>
        </div>
      ) : null}
    </div>
  )
}
