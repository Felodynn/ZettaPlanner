import { useState } from 'react'
import { KeyRound, ShieldCheck, UserPlus, Users } from 'lucide-react'
import {
  useCreateUser,
  useDeactivateUser,
  useUpdateUser,
  useUsers,
  type UserSummary,
} from './queries'
import { ROLE_VALUES, type Role } from '@/api/vocabulary'
import { useSession } from '@/auth/session'
import { Badge, Card, CardHeader, CardTitle, EmptyState, Input, Loading, Select } from '@/components/ui/primitives'
import { Dialog, Field } from '@/components/ui/Dialog'
import { Button } from '@/components/ui/Button'
import { RoleBadge } from '@/layout/RoleBadge'
import { notifySuccess } from '@/components/notify'

/** §7.1. Length, not character classes — the backend's rule, mirrored. */
const MIN_PASSWORD = 12

/**
 * Accounts.
 *
 * There is no sign-up: an administrator creates every account, which makes this
 * the only door into the product. Without it a deployment has exactly the users
 * its seed script made.
 */
export function AdminPage() {
  const { user } = useSession()
  const users = useUsers()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<UserSummary | null>(null)

  if (user.role !== 'admin') {
    return (
      <EmptyState
        className="pt-24"
        icon={<ShieldCheck className="h-7 w-7" />}
        title="Accounts are an administrator's to manage"
        body="Your role on this deployment is not admin. Roles decide what you may write; this is one of the things they decide."
      />
    )
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <Users className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Accounts</h1>
        <div className="flex-1" />
        <Button variant="primary" onClick={() => setCreating(true)}>
          <UserPlus className="h-3.5 w-3.5" />
          New account
        </Button>
      </div>
      <p className="mt-0.5 text-ink-muted">
        There is no sign-up. Every account is created here, and the role set here is the
        deployment-wide default — a project may override it for that project alone.
      </p>

      {users.isPending ? (
        <Loading what="accounts" />
      ) : users.error ? (
        <p className="py-4 text-ink-muted">{users.error.message}</p>
      ) : (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle>{users.data.length} accounts</CardTitle>
          </CardHeader>
          <div className="flex flex-col">
            {users.data.map((u) => (
              <div
                key={u.id}
                className="flex items-center gap-2 border-b border-line px-4 py-2 last:border-b-0"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-ink">{u.display_name}</span>
                  <span className="block truncate text-[11px] text-ink-faint">{u.email}</span>
                </span>
                {!u.is_active ? <Badge tone="danger">deactivated</Badge> : null}
                {u.id === user.id ? <Badge tone="outline">you</Badge> : null}
                <RoleBadge role={u.role} />
                <Button size="sm" variant="ghost" onClick={() => setEditing(u)}>
                  Edit
                </Button>
              </div>
            ))}
          </div>
        </Card>
      )}

      {creating ? <CreateUserDialog onClose={() => setCreating(false)} /> : null}
      {editing ? (
        <EditUserDialog user={editing} isSelf={editing.id === user.id} onClose={() => setEditing(null)} />
      ) : null}
    </div>
  )
}

function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateUser()
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('developer')

  const short = password.length < MIN_PASSWORD
  const ready = displayName.trim() && email.trim() && !short

  return (
    <Dialog
      title="New account"
      description="The person can sign in as soon as this is saved. Tell them the password out of band — it is not shown again."
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={create.isPending}
            disabled={!ready || create.isPending}
            onClick={() =>
              create.mutate(
                {
                  display_name: displayName.trim(),
                  email: email.trim(),
                  password,
                  role,
                },
                {
                  onSuccess: (u) => {
                    notifySuccess(`${u.display_name} can sign in now.`, 'Account created')
                    onClose()
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
      <Field label="Display name">
        <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
      </Field>
      <Field label="Email" hint="What they sign in with.">
        <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <Field
        label="Password"
        hint={
          short
            ? `${MIN_PASSWORD - password.length} more characters. The rule is length, not character classes — a long passphrase beats a short one with a symbol in it.`
            : 'Long enough. Hand it over out of band; it is not shown again.'
        }
      >
        <Input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
        />
      </Field>
      <Field
        label="Role"
        hint="The deployment-wide default. A project can override it for that project alone."
      >
        <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLE_VALUES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </Select>
      </Field>
    </Dialog>
  )
}

function EditUserDialog({
  user,
  isSelf,
  onClose,
}: {
  user: UserSummary
  isSelf: boolean
  onClose: () => void
}) {
  const update = useUpdateUser()
  const deactivate = useDeactivateUser()
  const [displayName, setDisplayName] = useState(user.display_name)
  const [role, setRole] = useState<Role>(user.role)
  const [password, setPassword] = useState('')

  const roleChanged = role !== user.role
  const passwordSet = password.length > 0
  const passwordShort = passwordSet && password.length < MIN_PASSWORD
  const dirty = displayName.trim() !== user.display_name || roleChanged || passwordSet

  return (
    <Dialog
      title={`Edit ${user.display_name}`}
      onClose={onClose}
      footer={
        <>
          <Button
            variant="danger"
            loading={deactivate.isPending}
            disabled={isSelf || !user.is_active || deactivate.isPending}
            title={
              isSelf
                ? 'You cannot deactivate the account you are signed in with.'
                : 'Signs them out everywhere and refuses future sign-ins. Nothing they wrote is removed.'
            }
            onClick={() =>
              deactivate.mutate(user.id, {
                onSuccess: () => {
                  notifySuccess(`${user.display_name} is deactivated and signed out.`, 'Done')
                  onClose()
                },
              })
            }
          >
            Deactivate
          </Button>
          <div className="flex-1" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={update.isPending}
            disabled={!dirty || passwordShort || update.isPending}
            onClick={() =>
              update.mutate(
                {
                  id: user.id,
                  body: {
                    ...(displayName.trim() !== user.display_name
                      ? { display_name: displayName.trim() }
                      : {}),
                    ...(roleChanged ? { role } : {}),
                    ...(passwordSet ? { password } : {}),
                  },
                },
                {
                  onSuccess: () => {
                    notifySuccess('Saved.')
                    onClose()
                  },
                },
              )
            }
          >
            Save
          </Button>
        </>
      }
    >
      {/* §7.1: a session must not outlast the authority it was issued under.
          Both of these revoke every live session the account holds, which is
          correct and surprising — so it is said before the button. */}
      {(roleChanged || passwordSet) && !isSelf ? (
        <p className="mb-3 rounded-md border border-warn/30 bg-warn-soft px-3 py-2 text-warn-ink">
          This signs {user.display_name} out everywhere, immediately. A session must not outlast the
          authority it was issued under, so changing a role or a password revokes every live one.
        </p>
      ) : null}
      {(roleChanged || passwordSet) && isSelf ? (
        <p className="mb-3 rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-danger-ink">
          This is your own account, so saving will sign <em>you</em> out immediately and you will
          need to sign in again.
        </p>
      ) : null}

      <Field label="Display name">
        <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
      </Field>
      <Field label="Email" hint="Fixed. It is the identity the audit trail is written against.">
        <Input value={user.email} disabled />
      </Field>
      <Field label="Role">
        <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLE_VALUES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </Select>
      </Field>
      <Field
        label="Reset password"
        hint={
          passwordShort
            ? `${MIN_PASSWORD - password.length} more characters.`
            : 'Leave blank to keep the current one.'
        }
      >
        <div className="flex items-center gap-2">
          <KeyRound className="h-3.5 w-3.5 shrink-0 text-ink-faint" />
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            placeholder="Unchanged"
          />
        </div>
      </Field>
    </Dialog>
  )
}
