import { Link } from 'react-router-dom'
import { ArrowRight, FolderOpen } from 'lucide-react'
import { useSession } from '@/auth/session'
import { Card, EmptyState } from '@/components/ui/primitives'
import { RoleBadge } from '@/layout/RoleBadge'

export function ProjectPicker() {
  const { projects, user } = useSession()

  if (projects.length === 0) {
    return (
      <EmptyState
        className="pt-24"
        icon={<FolderOpen className="h-7 w-7" />}
        title="No projects yet"
        body={
          user.role === 'admin'
            ? 'As an administrator you can create one. Members are added per project.'
            : 'You are not a member of any project. An administrator adds members.'
        }
      />
    )
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-10">
      <h1 className="text-base font-semibold text-ink">Projects</h1>
      <p className="mt-0.5 text-ink-muted">
        One deployment, one organisation, many projects (D3).
      </p>

      <div className="mt-5 flex flex-col gap-2">
        {projects.map((p) => (
          <Link key={p.project_id} to={`/p/${p.project_id}`} className="group">
            <Card className="flex items-center gap-3 px-4 py-3 transition-colors group-hover:border-accent-line group-hover:bg-accent-soft/40">
              <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-sunken font-mono text-[11px] font-semibold text-ink-muted">
                {p.project_key}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium text-ink">{p.project_name}</span>
              </span>
              <RoleBadge role={p.effective_role} overridden={p.override_role !== undefined} />
              <ArrowRight className="h-4 w-4 shrink-0 text-ink-faint transition-transform group-hover:translate-x-0.5 group-hover:text-accent-ink" />
            </Card>
          </Link>
        ))}
      </div>
    </div>
  )
}
