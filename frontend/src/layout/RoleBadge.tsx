import type { Role } from '@/api/vocabulary'
import { Badge, type Tone } from '@/components/ui/primitives'

const TONE: Record<Role, Tone> = {
  admin: 'accent',
  manager: 'info',
  developer: 'neutral',
}

export function RoleBadge({ role, overridden }: { role: Role; overridden?: boolean }) {
  return (
    <Badge
      tone={TONE[role]}
      title={
        overridden
          ? `${role} on this project — a per-project override of your deployment-wide role`
          : `${role}`
      }
    >
      {role}
      {overridden ? <span className="opacity-60">·</span> : null}
    </Badge>
  )
}
