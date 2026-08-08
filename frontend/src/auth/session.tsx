import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { client, list, onSessionLost, unwrap } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'
import type { Role } from '@/api/vocabulary'

export type User = components['schemas']['UserSummary']
export type ProjectRole = components['schemas']['ProjectRoleSummary']

interface SessionValue {
  user: User
  projects: ProjectRole[]
  /** The caller's effective role on a project: the override if set, else global. */
  roleOn: (projectId: string) => Role | undefined
  isMemberOf: (projectId: string) => boolean
}

const SessionContext = createContext<SessionValue | null>(null)

export const meQueryKey = ['auth', 'me'] as const

export function useMeQuery(): UseQueryResult<components['schemas']['MeBody'], Error> {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: () => unwrap(client.GET('/api/v1/auth/me', {})),
    // A 401 here is the answer — "nobody is signed in" — not a failure to
    // retry. Retrying it just delays the login page.
    retry: (count, error) => !(error instanceof ApiError && error.isAuth) && count < 2,
    staleTime: 30_000,
  })
}

export function SessionProvider({ me, children }: { me: components['schemas']['MeBody']; children: ReactNode }) {
  const projects = useMemo(() => list(me.projects), [me.projects])

  const value = useMemo<SessionValue>(() => {
    const byId = new Map(projects.map((p) => [p.project_id, p]))
    return {
      user: me.user,
      projects,
      roleOn: (id) => byId.get(id)?.effective_role,
      isMemberOf: (id) => byId.has(id),
    }
  }, [me.user, projects])

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession(): SessionValue {
  const ctx = useContext(SessionContext)
  if (!ctx) throw new Error('useSession must be used inside a SessionProvider')
  return ctx
}

/**
 * Turns a mid-session 401 into a signed-out state, once.
 *
 * frontend-notes §2: changing a role or a password, or deactivating an
 * account, revokes every live session it holds — a session must not outlast
 * the authority it was issued under. So this clears the cache and lets the
 * router fall through to the login page rather than retrying.
 */
export function useSessionExpiry(): void {
  const qc = useQueryClient()
  useEffect(
    () =>
      onSessionLost(() => {
        qc.setQueryData(meQueryKey, undefined)
        void qc.invalidateQueries({ queryKey: meQueryKey })
      }),
    [qc],
  )
}

export async function login(email: string, password: string) {
  return unwrap(client.POST('/api/v1/auth/login', { body: { email, password } }))
}

export async function logout() {
  await client.POST('/api/v1/auth/logout', {})
}
