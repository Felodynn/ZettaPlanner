import { useRef, useState, type KeyboardEvent } from 'react'
import { SendHorizonal } from 'lucide-react'
import type { Turn } from './queries'
import { usePostMessage } from './queries'
import { Button } from '@/components/ui/Button'
import { Textarea } from '@/components/ui/primitives'
import { notifyError } from '@/components/notify'

/**
 * The composer.
 *
 * The send button is never disabled because a turn is running. §5.3 is
 * explicit: **the message always lands.** Posting and running are separate —
 * the message is written immediately and the turn queues behind whatever else
 * is going. A user whose message is refused because an agent is busy learns to
 * wait before typing, which is the opposite of a transcript.
 *
 * What changes instead is what the button says, so the queue is visible rather
 * than merely real.
 */
export function Composer({
  threadId,
  active,
}: {
  threadId: string | undefined
  /** The turn currently running or queued on this project, if any. */
  active: Turn | undefined
}) {
  const [text, setText] = useState('')
  const ref = useRef<HTMLTextAreaElement>(null)
  const post = usePostMessage(threadId)

  const busy = active?.status === 'running' || active?.status === 'queued'

  const send = () => {
    const content = text.trim()
    if (!content || post.isPending) return
    post.mutate(content, {
      onSuccess: () => {
        setText('')
        ref.current?.focus()
      },
      onError: (e) => notifyError(e),
    })
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className="border-t border-line bg-surface px-4 py-3">
      <div className="flex items-end gap-2">
        <Textarea
          ref={ref}
          rows={2}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={!threadId}
          placeholder={
            busy
              ? 'Type — your message lands now and its turn queues behind the one running.'
              : 'Ask the planner, or tell the project something. Enter to send, Shift+Enter for a new line.'
          }
          className="max-h-48 min-h-16"
        />
        <Button
          variant="primary"
          size="lg"
          onClick={send}
          loading={post.isPending}
          disabled={!threadId || text.trim().length === 0}
          title={
            busy
              ? 'Your message is written immediately; its turn waits for the one running.'
              : 'Send'
          }
        >
          {post.isPending ? null : <SendHorizonal className="h-4 w-4" />}
          {busy ? 'Queue' : 'Send'}
        </Button>
      </div>

      <p className="mt-1.5 text-[11px] text-ink-faint">
        {/* D8, stated where it changes what someone types. */}
        This thread is a transcript, not the agent's memory. Anything that has to last is written
        into a work item, a note, or the diagram.
      </p>
    </div>
  )
}
