import { useState, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider, MutationCache } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import * as Tooltip from '@radix-ui/react-tooltip'
import { ApiError } from '@/api/errors'
import { notifyError } from '@/components/notify'
import { Toaster } from '@/components/Toaster'
import { ThemeProvider } from '@/theme/ThemeProvider'

function makeClient(): QueryClient {
  return new QueryClient({
    // Every failed write reports itself through the one error surface, unless
    // the caller opts out by handling it. A mutation that fails silently is
    // how a user learns not to trust the save button.
    mutationCache: new MutationCache({
      onError: (error, _vars, _ctx, mutation) => {
        if (mutation.options.onError) return
        notifyError(error)
      },
    }),
    defaultOptions: {
      queries: {
        // SSE drives freshness (frontend-notes §5), so polling would be a
        // second, worse copy of the same signal.
        refetchOnWindowFocus: false,
        staleTime: 15_000,
        retry: (count, error) => {
          // A 4xx is an answer. Retrying a 403 produces three denials and the
          // same outcome.
          if (error instanceof ApiError && error.status < 500) return false
          return count < 2
        },
      },
    },
  })
}

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(makeClient)

  // Development only: lets a check seed the cache directly, which is how the
  // transcript is tested at 500+ messages without posting 500 of them.
  if (import.meta.env.DEV) {
    Object.assign(window, { __zpQueryClient: queryClient })
  }

  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <Tooltip.Provider delayDuration={300} skipDelayDuration={200}>
          <BrowserRouter>
            {children}
            <Toaster />
          </BrowserRouter>
        </Tooltip.Provider>
      </QueryClientProvider>
    </ThemeProvider>
  )
}
