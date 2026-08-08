import { useEffect, useState } from 'react'
import { AlertTriangle, Check, Copy, Info, X, XCircle } from 'lucide-react'
import { useNotices, type Notice, type NoticeKind } from './notify'
import { Button } from './ui/Button'
import { cn } from '@/lib/cn'

const KIND: Record<NoticeKind, { ring: string; tint: string; Icon: typeof Info; ink: string }> = {
  error: { ring: 'border-danger/40', tint: 'bg-danger-soft', Icon: XCircle, ink: 'text-danger' },
  warn: { ring: 'border-warn/40', tint: 'bg-warn-soft', Icon: AlertTriangle, ink: 'text-warn' },
  success: { ring: 'border-good/40', tint: 'bg-good-soft', Icon: Check, ink: 'text-good' },
  info: { ring: 'border-info/40', tint: 'bg-info-soft', Icon: Info, ink: 'text-info' },
}

function RequestId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard?.writeText(id)
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1500)
      }}
      title="Copy the request id"
      className={cn(
        'group inline-flex max-w-full items-center gap-1 rounded px-1 py-0.5 font-mono text-[11px]',
        'text-ink-faint transition-colors hover:bg-sunken hover:text-ink-muted',
      )}
    >
      {copied ? <Check className="h-3 w-3 shrink-0" /> : <Copy className="h-3 w-3 shrink-0" />}
      <span className="truncate">{id}</span>
    </button>
  )
}

function Item({ notice }: { notice: Notice }) {
  const dismiss = useNotices((s) => s.dismiss)
  const { ring, tint, Icon, ink } = KIND[notice.kind]

  useEffect(() => {
    if (notice.timeout === null) return
    const t = window.setTimeout(() => dismiss(notice.id), notice.timeout)
    return () => window.clearTimeout(t)
  }, [notice.id, notice.timeout, dismiss])

  return (
    <div
      role={notice.kind === 'error' ? 'alert' : 'status'}
      className={cn(
        'zp-shadow-pop pointer-events-auto w-[26rem] max-w-[calc(100vw-2rem)] overflow-hidden',
        'rounded-lg border bg-raised',
        ring,
      )}
    >
      <div className={cn('flex items-start gap-2.5 px-3 py-2.5', tint)}>
        <Icon className={cn('mt-0.5 h-4 w-4 shrink-0', ink)} />
        <div className="min-w-0 flex-1">
          <p className="font-semibold text-ink">{notice.title}</p>
          {/* Verbatim. See notify.ts — this sentence was written to be read. */}
          <p className="mt-0.5 wrap-break-word whitespace-pre-line text-ink">{notice.message}</p>
        </div>
        <button
          type="button"
          onClick={() => dismiss(notice.id)}
          className="-mr-1 rounded p-1 text-ink-faint transition-colors hover:bg-surface/60 hover:text-ink"
          aria-label="Dismiss"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      {notice.requestId || notice.action ? (
        <div className="flex items-center justify-between gap-2 border-t border-line px-3 py-1.5">
          {notice.requestId ? <RequestId id={notice.requestId} /> : <span />}
          {notice.action ? (
            <Button
              size="sm"
              variant="primary"
              onClick={() => {
                notice.action?.onClick()
                dismiss(notice.id)
              }}
            >
              {notice.action.label}
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/**
 * The single error surface (FRONTEND F0).
 *
 * One place renders the contract's envelope, so there is one answer to "what
 * does a 403 look like" rather than one per screen.
 */
export function Toaster() {
  const notices = useNotices((s) => s.notices)
  if (notices.length === 0) return null
  return (
    <div className="pointer-events-none fixed right-4 bottom-4 z-[100] flex flex-col-reverse gap-2">
      {notices.map((n) => (
        <Item key={n.id} notice={n} />
      ))}
    </div>
  )
}
