import { useParams } from 'react-router-dom'
import { GitBranch, Users } from 'lucide-react'
import { useSession } from '@/auth/session'
import { Badge, Card, CardHeader, CardTitle, Loading, Row } from '@/components/ui/primitives'
import { RoleBadge } from '@/layout/RoleBadge'
import { useMembers, useProject } from './queries'

export function ProjectOverview() {
  const { projectId = '' } = useParams()
  const project = useProject(projectId)
  const members = useMembers(projectId)
  const { roleOn } = useSession()

  if (project.isPending) return <Loading what="this project" />
  if (project.error) throw project.error

  const p = project.data

  return (
    <div className="mx-auto w-full max-w-4xl px-6 py-6">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-lg font-semibold text-ink">{p.name}</h1>
            <span className="rounded bg-sunken px-1.5 py-0.5 font-mono text-[11px] text-ink-muted">
              {p.key}
            </span>
            <Badge tone={p.status === 'active' ? 'good' : 'neutral'}>{p.status}</Badge>
          </div>
          {p.description ? (
            <p className="mt-1 text-ink-muted">{p.description}</p>
          ) : (
            <p className="mt-1 text-ink-faint">No description.</p>
          )}
        </div>
        <RoleBadge role={roleOn(projectId) ?? 'developer'} />
      </div>

      <div className="mt-6 grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Project</CardTitle>
          </CardHeader>
          <div className="px-4 py-2">
            <Row label="Item ref prefix">
              <span className="font-mono">{p.key}-1, {p.key}-2, …</span>
            </Row>
            <Row label="Git projection">
              <span className="flex items-start gap-1.5">
                <GitBranch className="mt-1 h-3.5 w-3.5 shrink-0 text-ink-faint" />
                {p.git_enabled ? (
                  <span>
                    On — a one-way projection. Postgres remains the store of record (D10).
                  </span>
                ) : (
                  <span className="text-ink-muted">Off. Enabled by an operator, not from here.</span>
                )}
              </span>
            </Row>
          </div>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-1.5">
              <Users className="h-3.5 w-3.5 text-ink-faint" />
              Members
            </CardTitle>
            <span className="text-xs text-ink-faint">
              {members.data ? `${members.data.length}` : ''}
            </span>
          </CardHeader>
          <div className="px-4 py-2">
            {members.isPending ? (
              <Loading what="members" className="py-6" />
            ) : members.error ? (
              <p className="py-4 text-ink-muted">{members.error.message}</p>
            ) : (
              <ul className="divide-y divide-line">
                {members.data.map((m) => (
                  <li key={m.user_id} className="flex items-center gap-2 py-1.5">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-ink">{m.display_name}</span>
                      <span className="block truncate text-xs text-ink-faint">{m.email}</span>
                    </span>
                    <RoleBadge
                      role={m.effective_role}
                      overridden={m.project_role !== m.global_role}
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>
        </Card>
      </div>
    </div>
  )
}
