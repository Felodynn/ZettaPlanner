import { Construction } from 'lucide-react'
import { EmptyState } from './ui/primitives'

/**
 * A screen that does not exist yet, saying so.
 *
 * The build is phase-gated and the shell is navigable from F0, so these routes
 * are reachable before their phase lands. Naming the phase is more useful than
 * a blank panel, and more honest than a spinner that never resolves.
 */
export function Placeholder({ phase, what }: { phase: string; what: string }) {
  return (
    <EmptyState
      className="pt-24"
      icon={<Construction className="h-7 w-7" />}
      title={`${what} arrives in phase ${phase}.`}
      body="The route exists so the shell is navigable; the screen behind it is not built yet."
    />
  )
}
