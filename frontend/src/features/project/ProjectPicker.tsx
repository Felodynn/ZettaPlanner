import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ArrowRight, FolderOpen, FolderPlus, Users } from 'lucide-react'
import { useSession } from '@/auth/session'
import { Card, EmptyState, Input, Textarea } from '@/components/ui/primitives'
import { Dialog, Field } from '@/components/ui/Dialog'
import { Button } from '@/components/ui/Button'
import { RoleBadge } from '@/layout/RoleBadge'
import { useCreateProject } from '@/features/admin/queries'
import { notifySuccess } from '@/components/notify'

export function ProjectPicker() {
  const { projects, user } = useSession()
  const [creating, setCreating] = useState(false)
  const isAdmin = user.role === 'admin'

  const newProject = isAdmin ? (
    <Button variant="primary" onClick={() => setCreating(true)}>
      <FolderPlus className="h-3.5 w-3.5" />
      New project
    </Button>
  ) : null

  if (projects.length === 0) {
    return (
      <>
        <EmptyState
          className="pt-24"
          icon={<FolderOpen className="h-7 w-7" />}
          title="No projects yet"
          body={
            isAdmin
              ? 'Create one, then add members to it from its settings. Membership is what grants access — role decides what somebody may write, not what they may see.'
              : 'You are not a member of any project. An administrator adds members.'
          }
          action={newProject}
        />
        {creating ? <CreateProjectDialog onClose={() => setCreating(false)} /> : null}
      </>
    )
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-10">
      <div className="flex items-center gap-2">
        <h1 className="text-base font-semibold text-ink">Projects</h1>
        <div className="flex-1" />
        {newProject}
      </div>
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

      {isAdmin ? (
        <p className="mt-5 flex items-center gap-1.5 text-[11px] text-ink-faint">
          <Users className="h-3 w-3" />
          You see every project as an administrator. Other people see the ones they are a member of.
        </p>
      ) : null}

      {creating ? <CreateProjectDialog onClose={() => setCreating(false)} /> : null}
    </div>
  )
}

/** Upper-case letters and digits, as the backend requires. */
const KEY_RE = /^[A-Z][A-Z0-9]{0,9}$/

function CreateProjectDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateProject()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [description, setDescription] = useState('')
  // Suggested, never silently applied: the key is permanent, and a field that
  // fills itself in is a field people do not read.
  const [keyTouched, setKeyTouched] = useState(false)

  const suggested = suggestKey(name)
  const effectiveKey = keyTouched ? key : suggested
  const keyValid = KEY_RE.test(effectiveKey)

  return (
    <Dialog
      title="New project"
      description="A project is the unit of membership, the plan, the diagrams and the thread."
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={create.isPending}
            disabled={!name.trim() || !keyValid || create.isPending}
            onClick={() =>
              create.mutate(
                {
                  name: name.trim(),
                  key: effectiveKey,
                  ...(description.trim() ? { description: description.trim() } : {}),
                },
                {
                  onSuccess: (p) => {
                    notifySuccess(
                      `${p.name} is ready. Add members from its settings — membership is what grants access.`,
                      'Project created',
                    )
                    onClose()
                    navigate(`/p/${p.id}`)
                  },
                },
              )
            }
          >
            Create
          </Button>
        </>
      }
    >
      <Field label="Name">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Auth hardening"
        />
      </Field>

      <Field
        label="Key"
        hint={
          !keyValid && effectiveKey
            ? 'Upper-case letters and digits, starting with a letter, up to ten characters.'
            : 'Permanent. Every work item ref is issued from it — ' +
              `${effectiveKey || 'ZP'}-1, ${effectiveKey || 'ZP'}-2 — and a ref is never reissued, so this cannot be changed later without orphaning every one anybody has written down.`
        }
      >
        <Input
          value={effectiveKey}
          onChange={(e) => {
            setKeyTouched(true)
            setKey(e.target.value.toUpperCase())
          }}
          placeholder="ZP"
          className="font-mono"
        />
      </Field>

      <Field
        label="Description"
        hint="Read by the planner on every turn, so it is a prompt as much as a label. Optional now, worth writing properly later."
      >
        <Textarea
          rows={3}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this project is, and what done looks like."
        />
      </Field>
    </Dialog>
  )
}

/** Initials for a multi-word name, otherwise its first letters. */
function suggestKey(name: string): string {
  const words = name
    .toUpperCase()
    .split(/[^A-Z0-9]+/)
    .filter(Boolean)
  if (words.length === 0) return ''
  if (words.length === 1) return (words[0] ?? '').slice(0, 4)
  return words
    .map((w) => w[0])
    .join('')
    .slice(0, 6)
}
