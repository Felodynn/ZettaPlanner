import { NavLink, Outlet, useParams } from 'react-router-dom'
import {
  Activity,
  CircleUser,
  FileStack,
  GitPullRequestArrow,
  LayoutDashboard,
  ListChecks,
  MessagesSquare,
  Network,
  ScrollText,
  Settings,
  Upload,
} from 'lucide-react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useQueryClient } from '@tanstack/react-query'
import { logout, useSession } from '@/auth/session'
import { AppearanceMenu } from '@/theme/AppearanceMenu'
import { ProjectSwitcher } from './ProjectSwitcher'
import { RoleBadge } from './RoleBadge'
import { cn } from '@/lib/cn'
import type { Role } from '@/api/vocabulary'

interface NavItem {
  to: string
  label: string
  Icon: typeof Activity
  /** Omitted means every project member sees it. */
  roles?: Role[]
  end?: boolean
}

const NAV: NavItem[] = [
  { to: '', label: 'Overview', Icon: LayoutDashboard, end: true },
  { to: 'thread', label: 'Thread', Icon: MessagesSquare },
  { to: 'plan', label: 'Plan', Icon: ListChecks },
  { to: 'mine', label: 'My tasks', Icon: CircleUser },
  { to: 'diagrams', label: 'Diagrams', Icon: Network },
  { to: 'ingest', label: 'Ingest', Icon: Upload },
  { to: 'proposals', label: 'Proposals', Icon: GitPullRequestArrow },
  { to: 'notes', label: 'Notes', Icon: FileStack },
  { to: 'audit', label: 'Audit', Icon: ScrollText },
  // Project settings are manager-owned (§4.2). A developer is not shown a
  // screen whose every control would be read-only.
  { to: 'settings', label: 'Settings', Icon: Settings, roles: ['admin', 'manager'] },
]

function UserMenu() {
  const { user } = useSession()
  const qc = useQueryClient()

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        className="flex h-8 items-center gap-2 rounded-md px-2 text-ink-muted transition-colors hover:bg-sunken hover:text-ink data-[state=open]:bg-sunken"
        title={user.email}
      >
        <span className="flex h-5 w-5 items-center justify-center rounded-full bg-accent text-[10px] font-bold text-on-accent">
          {user.display_name.slice(0, 1).toUpperCase()}
        </span>
        <span className="hidden max-w-32 truncate sm:block">{user.display_name}</span>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="zp-shadow-pop z-50 w-60 rounded-lg border border-line bg-raised p-1.5"
        >
          <div className="px-2 py-1.5">
            <p className="truncate font-medium text-ink">{user.display_name}</p>
            <p className="truncate text-xs text-ink-faint">{user.email}</p>
            <div className="mt-1.5">
              <RoleBadge role={user.role} />
            </div>
          </div>
          <DropdownMenu.Separator className="my-1.5 h-px bg-line" />
          <DropdownMenu.Item
            onSelect={() => {
              void logout().finally(() => {
                qc.clear()
                window.location.assign('/')
              })
            }}
            className="cursor-pointer rounded-md px-2 py-1.5 outline-none data-[highlighted]:bg-sunken"
          >
            Sign out
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

export function AppShell() {
  const { projectId } = useParams()
  const { roleOn, user } = useSession()
  const role = projectId ? (roleOn(projectId) ?? user.role) : user.role
  const items = NAV.filter((i) => !i.roles || i.roles.includes(role))

  return (
    <div className="flex h-full flex-col bg-canvas">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-3">
        <NavLink to="/" className="flex items-center gap-2" title="ZettaPlanner">
          <span className="flex h-6 w-6 items-center justify-center rounded bg-accent text-xs font-bold text-on-accent">
            Z
          </span>
        </NavLink>
        <ProjectSwitcher />
        <div className="flex-1" />
        <AppearanceMenu />
        <UserMenu />
      </header>

      <div className="flex min-h-0 flex-1">
        {projectId ? (
          <nav className="flex w-44 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-line bg-surface p-2 zp-scroll">
            {items.map(({ to, label, Icon, end }) => (
              <NavLink
                key={to}
                to={to === '' ? `/p/${projectId}` : `/p/${projectId}/${to}`}
                end={end}
                className={({ isActive }) =>
                  cn(
                    'flex items-center gap-2.5 rounded-md px-2.5 py-1.5 transition-colors',
                    isActive
                      ? 'bg-accent-soft font-medium text-accent-ink'
                      : 'text-ink-muted hover:bg-sunken hover:text-ink',
                  )
                }
              >
                <Icon className="h-4 w-4 shrink-0" />
                {label}
              </NavLink>
            ))}
          </nav>
        ) : null}

        <main className="min-w-0 flex-1 overflow-auto zp-scroll">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
