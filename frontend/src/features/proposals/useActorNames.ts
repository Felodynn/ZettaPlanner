import { useCallback } from 'react'
import { useMembers } from '@/features/project/queries'

/**
 * User id → display name, for the places the API sends an id.
 *
 * The members list is already fetched and cached per project, so this costs
 * nothing extra. It returns undefined for an id it does not know, which the
 * callers render as the id — an unresolvable attribution is still an
 * attribution, and inventing a name for it would be worse than showing the id.
 */
export function useActorNames(projectId: string): (userId: string) => string | undefined {
  const members = useMembers(projectId)
  const list = members.data
  return useCallback(
    (userId: string) => list?.find((m) => m.user_id === userId)?.display_name,
    [list],
  )
}
