import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Card, Input } from '@/components/ui/primitives'
import { AppearanceMenu } from '@/theme/AppearanceMenu'
import { ApiError } from '@/api/errors'
import { login, meQueryKey } from './session'

export function LoginPage({ reason }: { reason?: string }) {
  const qc = useQueryClient()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const signIn = useMutation({
    mutationFn: () => login(email, password),
    onSuccess: async (data) => {
      qc.setQueryData(meQueryKey, data)
      await qc.invalidateQueries()
    },
  })

  const error = signIn.error
  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    signIn.mutate()
  }

  return (
    <div className="flex min-h-full flex-col bg-canvas">
      <div className="flex justify-end p-3">
        <AppearanceMenu />
      </div>

      <div className="flex flex-1 items-start justify-center px-4 pt-[8vh] pb-16">
        <div className="w-full max-w-sm">
          <div className="mb-6 flex flex-col items-center gap-2 text-center">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent text-on-accent">
              <span className="text-lg font-bold">Z</span>
            </div>
            <h1 className="text-lg font-semibold text-ink">ZettaPlanner</h1>
            <p className="text-ink-muted">An AI-native technical planning substrate.</p>
          </div>

          <Card className="p-5">
            {reason ? (
              <p className="mb-4 rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-warn-ink">
                {reason}
              </p>
            ) : null}

            <form onSubmit={onSubmit} className="flex flex-col gap-3">
              <label className="flex flex-col gap-1">
                <span className="font-medium text-ink">Email</span>
                <Input
                  type="email"
                  autoComplete="username"
                  required
                  autoFocus
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                />
              </label>

              <label className="flex flex-col gap-1">
                <span className="font-medium text-ink">Password</span>
                <Input
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>

              {error ? (
                <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2">
                  {/* The backend's own words, unedited (frontend-notes §3). */}
                  <p className="text-danger-ink">{error.message}</p>
                  {error instanceof ApiError ? (
                    <p className="mt-1 font-mono text-[11px] text-ink-faint">{error.requestId}</p>
                  ) : null}
                </div>
              ) : null}

              <Button type="submit" variant="primary" size="lg" loading={signIn.isPending}>
                {signIn.isPending ? null : <KeyRound className="h-4 w-4" />}
                Sign in
              </Button>
            </form>
          </Card>

          <p className="mt-4 text-center text-xs text-ink-faint">
            Accounts are created by an administrator. There is no sign-up.
          </p>
        </div>
      </div>
    </div>
  )
}
