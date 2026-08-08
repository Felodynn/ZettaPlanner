import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Archive, GitBranch, Settings as SettingsIcon, UserMinus, UserPlus, Users } from 'lucide-react'
import { useMembers, useProject, projectKeys } from './queries'
import { useOwnership } from '@/features/plan/ownership'
import { useRemoveMember, useSetMember, useUsers } from '@/features/admin/queries'
import { useSession } from '@/auth/session'
import { ROLE_VALUES, type Role } from '@/api/vocabulary'
import { client, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import {
  Badge,
  Card,
  CardHeader,
  CardTitle,
  Input,
  Loading,
  Row,
  Select,
  Separator,
  Textarea,
} from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { RoleBadge } from '@/layout/RoleBadge'
import { notifySuccess } from '@/components/notify'

type Project = components['schemas']['ProjectSummary']
type Patch = components['schemas']['ProjectPatchBody']

/**
 * Project settings.
 *
 * Deliberately small. Almost everything about a project lives somewhere it can
 * be seen in context — dates on the plan, membership on the people who hold it,
 * the diagram vocabulary in the schema — and a settings page that duplicated
 * any of that would become a second place for it to be wrong.
 *
 * What is here is what has nowhere else to be: the name, the description the
 * planner reads, the git projection switch, and the archive control.
 */
export function SettingsPage() {
  const { projectId = '' } = useParams()
  const project = useProject(projectId)
  const members = useMembers(projectId)
  const qc = useQueryClient()
  // Project settings are manager-owned (§4.2). The nav already hides this from
  // a developer, but a link is a link and the check belongs on the screen.
  const perm = useOwnership(projectId).field('project', '*')

  // Membership is an administrator's to change (§7.1), which is a different
  // gate from the manager-owned project fields above — so it is asked
  // separately rather than folded into `readOnly`.
  const { user } = useSession()
  const isAdmin = user.role === 'admin'
  const allUsers = useUsers()
  const setMember = useSetMember(projectId)
  const removeMember = useRemoveMember(projectId)
  const [addUserId, setAddUserId] = useState('')
  const [addRole, setAddRole] = useState<Role>('developer')

  const save = useMutation<Project, Error, Patch>({
    mutationFn: (body) =>
      unwrap(client.PATCH('/api/v1/projects/{pid}', { params: { path: { pid: projectId } }, body })),
    onSuccess: (updated) => {
      qc.setQueryData(projectKeys.detail(projectId), updated)
      void qc.invalidateQueries({ queryKey: projectKeys.list() })
      notifySuccess('Saved.')
    },
  })

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  // Seeded from the server, and reseeded if somebody else changes it while this
  // is open. Not a controlled mirror of the query: that would overwrite what
  // the person is typing on every refetch.
  useEffect(() => {
    if (!project.data) return
    setName(project.data.name)
    setDescription(project.data.description ?? '')
  }, [project.data?.id, project.data?.name, project.data?.description])

  if (project.isPending) return <Loading what="this project" />
  if (project.error) throw project.error

  const memberIds = new Set((members.data ?? []).map((m) => m.user_id))
  const addable = (allUsers.data ?? []).filter((u) => !memberIds.has(u.id) && u.is_active)

  const p = project.data
  const dirty = name.trim() !== p.name || description !== (p.description ?? '')
  const readOnly = !perm.writable

  return (
    <div className="mx-auto w-full max-w-2xl px-6 py-6">
      <div className="flex items-center gap-2">
        <SettingsIcon className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Project settings</h1>
      </div>

      {readOnly ? (
        <p className="mt-2 rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-warn-ink">
          {perm.why}
        </p>
      ) : null}

      {/* ── identity ── */}
      <Card className="mt-4">
        <CardHeader>
          <CardTitle>Identity</CardTitle>
          <Badge tone="outline" title="The prefix on every work item ref in this project">
            {p.key}
          </Badge>
        </CardHeader>
        <div className="flex flex-col gap-3 px-4 py-3">
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
              Name
            </span>
            <Input value={name} onChange={(e) => setName(e.target.value)} disabled={readOnly} />
          </label>

          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
              Description
            </span>
            <Textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={readOnly}
              placeholder="What this project is. The planner reads this on every turn, so it is worth writing properly."
            />
            <span className="text-[11px] text-ink-faint">
              This goes into the assembled context, so it is a prompt as much as a label.
            </span>
          </label>

          <Row label="Key">
            <span className="font-mono text-ink">{p.key}</span>
            <span className="ml-2 text-[11px] text-ink-faint">
              Fixed. Refs are issued from it and never reissued, so changing it would orphan every
              {' '}{p.key}-1 anybody has written down.
            </span>
          </Row>

          <div className="flex justify-end">
            <Button
              variant="primary"
              loading={save.isPending}
              disabled={readOnly || !dirty || !name.trim() || save.isPending}
              onClick={() =>
                save.mutate({ name: name.trim(), description: description.trim() })
              }
            >
              Save
            </Button>
          </div>
        </div>
      </Card>

      {/* ── the git projection ── */}
      <Card className="mt-3">
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            <GitBranch className="h-3.5 w-3.5 text-ink-faint" />
            Git projection
          </CardTitle>
          <Badge tone={p.git_enabled ? 'good' : 'neutral'}>{p.git_enabled ? 'on' : 'off'}</Badge>
        </CardHeader>
        <div className="flex items-start gap-3 px-4 py-3">
          <p className="min-w-0 flex-1 text-ink-muted">
            {/* D10, said plainly: the projection is one-way and regenerable,
                which is exactly what makes it safe to have. */}
            Writes the canonical serialization out as files, one way. Postgres stays the store of
            record — the projection is allowed to be stale and can be regenerated from the database
            at any time, which is what keeps it from becoming a second source of truth. It buys
            diff and blame, and nothing structural.
          </p>
          <Button
            variant={p.git_enabled ? 'secondary' : 'primary'}
            disabled={readOnly || save.isPending}
            loading={save.isPending}
            onClick={() => save.mutate({ git_enabled: !p.git_enabled })}
          >
            {p.git_enabled ? 'Turn off' : 'Turn on'}
          </Button>
        </div>
      </Card>

      {/* ── membership ── */}
      <Card className="mt-3">
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            <Users className="h-3.5 w-3.5 text-ink-faint" />
            Members
          </CardTitle>
          <span className="text-[11px] text-ink-faint">{(members.data ?? []).length} people</span>
        </CardHeader>
        <div className="flex flex-col">
          {(members.data ?? []).map((m) => (
            <div
              key={m.user_id}
              className="flex items-center gap-2 border-b border-line px-4 py-2 last:border-b-0"
            >
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium text-ink">{m.display_name}</span>
                <span className="block truncate text-[11px] text-ink-faint">{m.email}</span>
              </span>
              {!m.is_active ? <Badge tone="danger">deactivated</Badge> : null}
              {m.project_role !== m.global_role ? (
                <Badge tone="outline" title="Overridden for this project">
                  override
                </Badge>
              ) : null}

              {isAdmin ? (
                <>
                  <Select
                    className="w-32"
                    value={m.project_role}
                    disabled={setMember.isPending}
                    aria-label={`Role for ${m.display_name}`}
                    onChange={(e) =>
                      setMember.mutate(
                        { userId: m.user_id, role: e.target.value as Role },
                        { onSuccess: () => notifySuccess(`${m.display_name} is now a ${e.target.value} here.`) },
                      )
                    }
                  >
                    {ROLE_VALUES.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </Select>
                  <Button
                    size="icon"
                    variant="ghost"
                    disabled={removeMember.isPending}
                    aria-label={`Remove ${m.display_name}`}
                    title="Remove from this project. Nothing they wrote is removed — the audit trail keeps resolving."
                    onClick={() =>
                      removeMember.mutate(m.user_id, {
                        onSuccess: () =>
                          notifySuccess(`${m.display_name} no longer has access to this project.`),
                      })
                    }
                  >
                    <UserMinus className="h-3.5 w-3.5" />
                  </Button>
                </>
              ) : (
                <RoleBadge role={m.effective_role} />
              )}
            </div>
          ))}
          {(members.data ?? []).length === 0 ? (
            <p className="px-4 py-3 text-ink-muted">
              Nobody is a member yet. Until somebody is, only administrators can see this project.
            </p>
          ) : null}
        </div>

        {isAdmin ? (
          <div className="flex items-end gap-2 border-t border-line px-4 py-3">
            <label className="flex min-w-0 flex-1 flex-col gap-1">
              <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                Add somebody
              </span>
              <Select
                value={addUserId}
                onChange={(e) => setAddUserId(e.target.value)}
                aria-label="Person to add"
              >
                <option value="">Choose a person…</option>
                {addable.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name} ({u.email})
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex w-32 flex-col gap-1">
              <span className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                As
              </span>
              <Select value={addRole} onChange={(e) => setAddRole(e.target.value as Role)}>
                {ROLE_VALUES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </label>
            <Button
              variant="primary"
              loading={setMember.isPending}
              disabled={!addUserId || setMember.isPending}
              onClick={() =>
                setMember.mutate(
                  { userId: addUserId, role: addRole },
                  {
                    onSuccess: () => {
                      notifySuccess('Added. They can see this project and its whole history now.')
                      setAddUserId('')
                    },
                  },
                )
              }
            >
              <UserPlus className="h-3.5 w-3.5" />
              Add
            </Button>
          </div>
        ) : null}

        <p className="border-t border-line px-4 py-2 text-[11px] text-ink-faint">
          Membership is what grants access to a project, including its history — role decides what
          you may write, not what you may see.
          {isAdmin
            ? ' The role set here overrides the account-wide one, for this project only.'
            : ' Adding and removing people is an administrator action.'}
        </p>
      </Card>

      {/* ── archive ── */}
      <Card className="mt-3">
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5">
            <Archive className="h-3.5 w-3.5 text-ink-faint" />
            Lifecycle
          </CardTitle>
          <Badge tone={p.status === 'active' ? 'good' : 'neutral'}>{p.status}</Badge>
        </CardHeader>
        <div className="flex items-start gap-3 px-4 py-3">
          <p className="min-w-0 flex-1 text-ink-muted">
            Archiving takes the project out of the way without removing anything. Nothing in this
            product is hard-deleted — the audit trail has to keep resolving — so this is the honest
            version of "we are done with it".
          </p>
          <Button
            variant="secondary"
            disabled={readOnly || save.isPending}
            onClick={() =>
              save.mutate({ status: p.status === 'active' ? 'archived' : 'active' })
            }
          >
            {p.status === 'active' ? 'Archive' : 'Reactivate'}
          </Button>
        </div>
      </Card>

      <Separator className="my-4" />
      <p className="text-[11px] text-ink-faint">
        There is no CSV export here. The audit record is complete from the first commit; the export
        shape is a design question best answered with real data in front of it, and shipping half of
        it now would set an expectation about its columns that the real one would have to break.
      </p>
    </div>
  )
}
