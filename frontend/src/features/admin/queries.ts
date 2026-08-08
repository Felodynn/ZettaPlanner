import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { client, list, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { projectKeys } from '@/features/project/queries'
import { meQueryKey } from '@/auth/session'

export type UserSummary = components['schemas']['UserSummary']
export type CreateUser = components['schemas']['Create-userRequest']
export type UpdateUser = components['schemas']['Update-userRequest']
export type ProjectSummary = components['schemas']['ProjectSummary']

export const adminKeys = {
  users: ['admin', 'users'] as const,
}

/**
 * Accounts.
 *
 * There is no sign-up, by design — an administrator creates every account —
 * which means this screen is not a convenience. Without it a deployment has
 * exactly the users its seed script made, and adding a colleague is a database
 * task.
 */
export function useUsers() {
  return useQuery({
    queryKey: adminKeys.users,
    queryFn: async () => list((await unwrap(client.GET('/api/v1/admin/users', {}))).users),
  })
}

export function useCreateUser() {
  const qc = useQueryClient()
  return useMutation<UserSummary, Error, CreateUser>({
    mutationFn: (body) => unwrap(client.POST('/api/v1/admin/users', { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: adminKeys.users }),
  })
}

/**
 * Changing an account.
 *
 * A role change or a password reset **revokes every live session that account
 * holds** (§7.1) — the person is signed out wherever they are, immediately.
 * That is the right behaviour and a surprising one, so the screen says it
 * before the button rather than after.
 */
export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation<UserSummary, Error, { id: string; body: UpdateUser }>({
    mutationFn: ({ id, body }) =>
      unwrap(client.PATCH('/api/v1/admin/users/{id}', { params: { path: { id } }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: adminKeys.users }),
  })
}

export function useDeactivateUser() {
  const qc = useQueryClient()
  return useMutation<void, Error, string>({
    mutationFn: async (id) => {
      const res = await client.DELETE('/api/v1/admin/users/{id}', { params: { path: { id } } })
      if (res.error) throw res.error
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: adminKeys.users }),
  })
}

/**
 * Creating a project.
 *
 * The `key` is the one field here that cannot be changed afterwards: every work
 * item ref is issued from it and a ref is never reissued, so a project created
 * as `ZP` has `ZP-14` written in worklogs, audit entries and people's notes
 * forever.
 */
export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation<ProjectSummary, Error, components['schemas']['Create-projectRequest']>({
    mutationFn: (body) => unwrap(client.POST('/api/v1/projects', { body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: projectKeys.all })
      // The session carries the caller's projects and their role on each, and
      // a project they have just created is one of them. Keyed off the
      // exported constant rather than a literal: the two drifted once already.
      void qc.invalidateQueries({ queryKey: meQueryKey })
    },
  })
}

/** Add somebody to a project, or change the role they hold on it. */
export function useSetMember(projectId: string) {
  const qc = useQueryClient()
  return useMutation<void, Error, { userId: string; role: 'admin' | 'manager' | 'developer' }>({
    mutationFn: async ({ userId, role }) => {
      const res = await client.PUT('/api/v1/projects/{pid}/members/{uid}', {
        params: { path: { pid: projectId, uid: userId } },
        body: { role },
      })
      if (res.error) throw res.error
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: projectKeys.members(projectId) })
      void qc.invalidateQueries({ queryKey: projectKeys.ownership(projectId) })
      // If the person changed was the caller, their own standing just moved.
      void qc.invalidateQueries({ queryKey: meQueryKey })
    },
  })
}

export function useRemoveMember(projectId: string) {
  const qc = useQueryClient()
  return useMutation<void, Error, string>({
    mutationFn: async (userId) => {
      const res = await client.DELETE('/api/v1/projects/{pid}/members/{uid}', {
        params: { path: { pid: projectId, uid: userId } },
      })
      if (res.error) throw res.error
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: projectKeys.members(projectId) })
      void qc.invalidateQueries({ queryKey: meQueryKey })
    },
  })
}
