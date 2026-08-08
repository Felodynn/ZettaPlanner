import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronsUpDown, FolderOpen } from 'lucide-react'
import { useNavigate, useParams } from 'react-router-dom'
import { useSession } from '@/auth/session'
import { cn } from '@/lib/cn'
import { RoleBadge } from './RoleBadge'

export function ProjectSwitcher() {
  const { projects } = useSession()
  const { projectId } = useParams()
  const navigate = useNavigate()
  const current = projects.find((p) => p.project_id === projectId)

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        className={cn(
          'flex h-8 min-w-0 items-center gap-2 rounded-md border border-line px-2',
          'bg-surface text-ink transition-colors hover:bg-sunken',
          'data-[state=open]:bg-sunken',
        )}
      >
        <FolderOpen className="h-3.5 w-3.5 shrink-0 text-ink-faint" />
        <span className="truncate font-medium">{current ? current.project_name : 'Projects'}</span>
        {current ? (
          <span className="shrink-0 rounded bg-sunken px-1 font-mono text-[10px] text-ink-faint">
            {current.project_key}
          </span>
        ) : null}
        <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-ink-faint" />
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={6}
          className="zp-shadow-pop z-50 max-h-[70vh] w-72 overflow-y-auto rounded-lg border border-line bg-raised p-1.5"
        >
          <DropdownMenu.Label className="px-2 py-1.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            Projects
          </DropdownMenu.Label>

          {projects.length === 0 ? (
            <p className="px-2 py-3 text-ink-muted">
              You are not a member of any project yet. An administrator adds members.
            </p>
          ) : null}

          {projects.map((p) => (
            <DropdownMenu.Item
              key={p.project_id}
              onSelect={() => navigate(`/p/${p.project_id}`)}
              className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 outline-none data-[highlighted]:bg-sunken"
            >
              <span className="min-w-0 flex-1">
                <span className="block truncate leading-tight">{p.project_name}</span>
                <span className="block font-mono text-[11px] leading-tight text-ink-faint">
                  {p.project_key}
                </span>
              </span>
              {/* The role is shown per project because it varies per project:
                  the override wins over the global role (§4.1), and which one
                  applies is exactly what decides whether a field is editable. */}
              <RoleBadge role={p.effective_role} overridden={p.override_role !== undefined} />
              {p.project_id === projectId ? (
                <Check className="h-4 w-4 shrink-0 text-accent-ink" />
              ) : null}
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
