import { Component, type ErrorInfo, type ReactNode } from 'react'
import { ApiError } from '@/api/errors'
import { Button } from './ui/Button'

interface Props {
  children: ReactNode
  /** Names what failed, so the message is not "an error occurred". */
  what?: string
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  override state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    // eslint-disable-next-line no-console
    console.error('render failed', error, info.componentStack)
  }

  override render(): ReactNode {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div className="flex min-h-64 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
        <p className="font-semibold text-ink">
          {this.props.what ? `${this.props.what} could not be displayed.` : 'Something broke here.'}
        </p>
        <p className="max-w-lg wrap-break-word text-ink-muted">{error.message}</p>
        {error instanceof ApiError ? (
          <p className="font-mono text-[11px] text-ink-faint">{error.requestId}</p>
        ) : null}
        <div className="flex gap-2 pt-1">
          <Button onClick={() => this.setState({ error: null })}>Try again</Button>
          <Button variant="ghost" onClick={() => window.location.reload()}>
            Reload the page
          </Button>
        </div>
      </div>
    )
  }
}
