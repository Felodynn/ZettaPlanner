import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  AlertTriangle,
  ArrowLeft,
  Boxes,
  CheckSquare,
  ClipboardList,
  Link2,
  ListTree,
  Plus,
  ScrollText,
  Square,
} from 'lucide-react'
import { useSession } from '@/auth/session'
import { Badge, Card, CardHeader, CardTitle, Input, Loading, Select, Textarea } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { useMembers } from '@/features/project/queries'
import { PRIORITY_VALUES, WORK_ITEM_STATUS_VALUES, WORKLOG_KIND_VALUES } from '@/api/vocabulary'
import { formatRelative, formatTimestamp, isOverdue } from '@/lib/format'
import { PriorityBadge, SeverityBadge, StatusBadge, ProvenanceBadge, OwnershipNote } from './atoms'
import { EditableDate, EditableSelect, EditableText, FieldRow } from './fields'
import { useOwnership, type Relationship } from './ownership'
import {
  useAddChecklistItem,
  useAddSubTask,
  useAddWorklog,
  useDeviations,
  usePatchChecklistItem,
  usePatchDeviation,
  usePatchSubTask,
  usePatchWorkItem,
  useWarnings,
  useWorkItem,
  useWorklogs,
  useMilestones,
} from './queries'
import { cn } from '@/lib/cn'

export function ItemDetail() {
  const { projectId = '', itemId = '' } = useParams()
  const { user } = useSession()

  const item = useWorkItem(itemId)
  const own = useOwnership(projectId)
  const members = useMembers(projectId)
  const milestones = useMilestones(projectId)
  const warnings = useWarnings(projectId)
  const worklogs = useWorklogs(projectId, itemId)
  const deviations = useDeviations(projectId, itemId)
  const patch = usePatchWorkItem(projectId, itemId)

  if (item.isPending) return <Loading what="this item" />
  if (item.error) throw item.error

  const d = item.data
  // The relationship the ownership table needs: `assigned` scopes turn on it,
  // and only this view knows whether the item is the caller's.
  const rel: Relationship = { assigned: d.assignee_id === user.id }
  const f = (field: string) => own.field('work_item', field, rel)

  const itemWarnings = (warnings.data ?? []).filter((w) => w.item_id === itemId)
  const set = (body: Parameters<typeof patch.mutate>[0]) => patch.mutate(body)

  return (
    <div className="mx-auto w-full max-w-5xl px-6 py-5">
      <Link
        to={`/p/${projectId}/plan`}
        className="mb-3 inline-flex items-center gap-1 text-ink-faint hover:text-ink"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Plan
      </Link>

      {d.deleted_at ? (
        <div className="mb-4 rounded-md border border-line bg-sunken px-3 py-2 text-ink-muted">
          {/* frontend-notes §9: a deleted item still resolves, on purpose — a
              worklog or an audit entry mentioning it has to keep working. */}
          This item was deleted on {formatTimestamp(d.deleted_at)}. It is kept so that anything
          referring to {d.ref} still resolves, and its ref is never reissued.
        </div>
      ) : null}

      <div className="flex items-start gap-3">
        <span className="mt-1 font-mono text-sm text-ink-faint">{d.ref}</span>
        <div className="min-w-0 flex-1">
          <EditableText
            value={d.title}
            perm={f('title')}
            onSave={(title) => set({ title })}
            className="text-lg font-semibold"
          />
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <StatusBadge status={d.status} />
            <PriorityBadge priority={d.priority} />
            <ProvenanceBadge provenance={d.provenance} />
            {d.milestone_name ? <Badge tone="neutral">{d.milestone_name}</Badge> : null}
          </div>
        </div>
      </div>

      {itemWarnings.length > 0 ? (
        <div className="mt-4 rounded-card border border-warn/30 bg-warn-soft px-3 py-2">
          {itemWarnings.map((w) => (
            <p key={w.kind + w.related_id} className="flex items-start gap-2 text-warn-ink">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{w.message}</span>
            </p>
          ))}
        </div>
      ) : null}

      <div className="mt-5 grid gap-4 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-4">
          <Card>
            <CardHeader>
              <CardTitle>Intent and acceptance</CardTitle>
            </CardHeader>
            <div className="px-4 py-2">
              <FieldRow label="Intent" perm={f('intent')}>
                <EditableText
                  value={d.intent}
                  perm={f('intent')}
                  onSave={(intent) => set({ intent })}
                  multiline
                  placeholder="Why this exists"
                />
              </FieldRow>
              <FieldRow label="Acceptance" perm={f('acceptance_criteria')}>
                <EditableText
                  value={d.acceptance_criteria}
                  perm={f('acceptance_criteria')}
                  onSave={(acceptance_criteria) => set({ acceptance_criteria })}
                  multiline
                  placeholder="What has to be true to call this done"
                />
              </FieldRow>
            </div>
          </Card>

          <SubTasks projectId={projectId} itemId={itemId} subTasks={d.sub_tasks ?? []} rel={rel} />

          <Checklist
            projectId={projectId}
            itemId={itemId}
            checklist={d.checklist ?? []}
            rel={rel}
          />

          <Deviations
            projectId={projectId}
            deviations={deviations.data ?? []}
            loading={deviations.isPending}
          />

          <Worklogs
            projectId={projectId}
            itemId={itemId}
            worklogs={worklogs.data ?? []}
            loading={worklogs.isPending}
          />
        </div>

        <aside className="flex flex-col gap-4">
          <Card>
            <CardHeader>
              {/* Named for what it holds rather than for whose it is: the
                  same panel is the assignee's to edit and a manager's to read,
                  and "Yours" is a lie on half the screens it renders on. */}
              <CardTitle>Status and progress</CardTitle>
              <span className="text-[11px] text-ink-faint">the assignee's to report</span>
            </CardHeader>
            <div className="px-4 py-2">
              {/* status and progress_pct belong to the assignee; everything
                  else about a work item belongs to a manager (§4.2). Which of
                  the two regions is yours changes per item, which is why they
                  are drawn apart rather than interleaved. */}
              <FieldRow label="Status" perm={f('status')}>
                <EditableSelect
                  value={d.status}
                  options={WORK_ITEM_STATUS_VALUES}
                  perm={f('status')}
                  onSave={(status) => set({ status: status as typeof d.status })}
                >
                  <StatusBadge status={d.status} />
                </EditableSelect>
              </FieldRow>
              <FieldRow label="Progress" perm={f('progress_pct')}>
                {f('progress_pct').writable ? (
                  <div className="flex items-center gap-2">
                    <Input
                      type="number"
                      min={0}
                      max={100}
                      value={d.progress_pct}
                      onChange={(e) => set({ progress_pct: Number(e.target.value) })}
                      className="zp-num h-7 w-20 text-xs"
                    />
                    <span className="text-ink-faint">%</span>
                  </div>
                ) : (
                  <span className="zp-num text-ink">{d.progress_pct}%</span>
                )}
              </FieldRow>
              {!f('status').writable && f('status').why ? (
                <OwnershipNote why={f('status').why!} className="mt-1" />
              ) : null}
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Planning</CardTitle>
              <span className="text-[11px] text-ink-faint">manager-owned</span>
              {/* §4.2: a manager overwriting `status` destroys the developer's
                  report of reality rather than correcting it, which is why the
                  two regions are drawn apart. */}
            </CardHeader>
            <div className="px-4 py-2">
              <FieldRow label="Assignee" perm={f('assignee_id')}>
                <EditableSelect
                  value={d.assignee_id}
                  options={(members.data ?? []).map((m) => m.user_id)}
                  perm={f('assignee_id')}
                  onSave={(assignee_id) => set({ assignee_id })}
                  allowEmpty
                  emptyLabel="Unassigned"
                  render={(id) =>
                    (members.data ?? []).find((m) => m.user_id === id)?.display_name ?? id
                  }
                >
                  <span className="text-ink">{d.assignee_name ?? 'Unassigned'}</span>
                </EditableSelect>
              </FieldRow>

              <FieldRow label="Priority" perm={f('priority')}>
                <EditableSelect
                  value={d.priority}
                  options={PRIORITY_VALUES}
                  perm={f('priority')}
                  onSave={(priority) => set({ priority: priority as typeof d.priority })}
                  allowEmpty
                  emptyLabel="Untriaged"
                >
                  <PriorityBadge priority={d.priority} />
                </EditableSelect>
              </FieldRow>

              <FieldRow label="Milestone" perm={f('milestone_id')}>
                <EditableSelect
                  value={d.milestone_id}
                  options={(milestones.data ?? []).map((m) => m.id)}
                  perm={f('milestone_id')}
                  onSave={(milestone_id) => set({ milestone_id })}
                  allowEmpty
                  emptyLabel="None"
                  render={(id) => (milestones.data ?? []).find((m) => m.id === id)?.name ?? id}
                >
                  <span className="text-ink">{d.milestone_name ?? '—'}</span>
                </EditableSelect>
              </FieldRow>

              <FieldRow label="Starts" perm={f('start_date')}>
                <EditableDate
                  value={d.start_date}
                  perm={f('start_date')}
                  onSave={(start_date) => set({ start_date })}
                />
              </FieldRow>

              <FieldRow label="Due" perm={f('due_date')}>
                <EditableDate
                  value={d.due_date}
                  perm={f('due_date')}
                  onSave={(due_date) => set({ due_date })}
                  className={cn(isOverdue(d.due_date) && d.status !== 'done' && 'text-danger-ink')}
                />
              </FieldRow>

              {!f('due_date').writable && f('due_date').why ? (
                <OwnershipNote why={f('due_date').why!} className="mt-1" />
              ) : null}
            </div>
          </Card>

          <Dependencies projectId={projectId} detail={d} />
          <EntityRefs detail={d} />
        </aside>
      </div>
    </div>
  )
}

// ── sub-tasks ────────────────────────────────────────────────────────────────

function SubTasks({
  projectId,
  itemId,
  subTasks,
  rel,
}: {
  projectId: string
  itemId: string
  subTasks: NonNullable<ReturnType<typeof useWorkItem>['data']>['sub_tasks']
  rel: Relationship
}) {
  const own = useOwnership(projectId)
  const add = useAddSubTask(projectId, itemId)
  const patch = usePatchSubTask(projectId, itemId)
  const [title, setTitle] = useState('')

  const perm = own.field('sub_task', 'title', rel)
  const list = subTasks ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <ListTree className="h-3.5 w-3.5 text-ink-faint" />
          Sub-tasks
        </CardTitle>
        <span className="zp-num text-[11px] text-ink-faint">
          {list.filter((s) => s.status === 'done').length}/{list.length}
        </span>
      </CardHeader>

      <div className="px-4 py-2">
        {list.length === 0 ? (
          <p className="py-2 text-ink-faint">No sub-tasks. They are a leaf; there is no deeper nesting.</p>
        ) : (
          <ul className="divide-y divide-line">
            {list.map((s) => (
              <li key={s.id} className="flex items-center gap-2 py-1.5">
                <span className="min-w-0 flex-1 truncate text-ink">{s.title}</span>
                {perm.writable ? (
                  <Select
                    value={s.status}
                    onChange={(e) => patch.mutate({ id: s.id, body: { status: e.target.value as typeof s.status } })}
                    className="h-6 w-auto text-[11px]"
                  >
                    {WORK_ITEM_STATUS_VALUES.map((v) => (
                      <option key={v} value={v}>
                        {v.replace(/_/g, ' ')}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <StatusBadge status={s.status} />
                )}
              </li>
            ))}
          </ul>
        )}

        {perm.writable ? (
          <form
            className="mt-2 flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (!title.trim()) return
              add.mutate(title.trim(), { onSuccess: () => setTitle('') })
            }}
          >
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Add a sub-task"
              className="h-7 text-xs"
            />
            <Button size="sm" type="submit" loading={add.isPending} disabled={!title.trim()}>
              <Plus className="h-3 w-3" />
              Add
            </Button>
          </form>
        ) : perm.why ? (
          <OwnershipNote why={perm.why} className="mt-2" />
        ) : null}
      </div>
    </Card>
  )
}

// ── checklist ────────────────────────────────────────────────────────────────

function Checklist({
  projectId,
  itemId,
  checklist,
  rel,
}: {
  projectId: string
  itemId: string
  checklist: NonNullable<ReturnType<typeof useWorkItem>['data']>['checklist']
  rel: Relationship
}) {
  const own = useOwnership(projectId)
  const add = useAddChecklistItem(projectId, itemId)
  const patch = usePatchChecklistItem(projectId, itemId)
  const [text, setText] = useState('')

  const perm = own.field('checklist_item', 'checked', rel)
  const list = checklist ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <CheckSquare className="h-3.5 w-3.5 text-ink-faint" />
          Checklist
        </CardTitle>
        <span className="zp-num text-[11px] text-ink-faint">
          {list.filter((c) => c.checked).length}/{list.length}
        </span>
      </CardHeader>

      <div className="px-4 py-2">
        {list.length === 0 ? (
          <p className="py-2 text-ink-faint">Nothing on the checklist.</p>
        ) : (
          <ul>
            {list.map((c) => (
              <li key={c.id} className="flex items-start gap-2 py-1">
                <button
                  type="button"
                  disabled={!perm.writable}
                  onClick={() => patch.mutate({ id: c.id, body: { checked: !c.checked } })}
                  title={perm.writable ? undefined : perm.why}
                  className={cn(
                    'mt-0.5 shrink-0',
                    perm.writable ? 'text-ink-muted hover:text-accent-ink' : 'cursor-default text-ink-faint',
                  )}
                >
                  {c.checked ? (
                    <CheckSquare className="h-4 w-4 text-good" />
                  ) : (
                    <Square className="h-4 w-4" />
                  )}
                </button>
                <span className={cn('min-w-0 flex-1', c.checked ? 'text-ink-muted line-through' : 'text-ink')}>
                  {c.text}
                </span>
                {c.checked_at ? (
                  <span className="shrink-0 text-[11px] text-ink-faint" title={formatTimestamp(c.checked_at)}>
                    {formatRelative(c.checked_at)}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}

        {perm.writable ? (
          <form
            className="mt-2 flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (!text.trim()) return
              add.mutate(text.trim(), { onSuccess: () => setText('') })
            }}
          >
            <Input
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="Add a check"
              className="h-7 text-xs"
            />
            <Button size="sm" type="submit" loading={add.isPending} disabled={!text.trim()}>
              <Plus className="h-3 w-3" />
              Add
            </Button>
          </form>
        ) : perm.why ? (
          <OwnershipNote why={perm.why} className="mt-2" />
        ) : null}
      </div>
    </Card>
  )
}

// ── worklogs ─────────────────────────────────────────────────────────────────

function Worklogs({
  projectId,
  itemId,
  worklogs,
  loading,
}: {
  projectId: string
  itemId: string
  worklogs: ReturnType<typeof useWorklogs>['data'] & object
  loading: boolean
}) {
  const add = useAddWorklog(projectId, itemId)
  const [body, setBody] = useState('')
  const [kind, setKind] = useState<'progress' | 'deviation' | 'note'>('progress')

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <ScrollText className="h-3.5 w-3.5 text-ink-faint" />
          Worklog
        </CardTitle>
        <span className="text-[11px] text-ink-faint">append-only</span>
      </CardHeader>

      <div className="px-4 py-2">
        {loading ? (
          <Loading what="worklogs" className="py-4" />
        ) : worklogs.length === 0 ? (
          <p className="py-2 text-ink-faint">Nothing logged yet.</p>
        ) : (
          <ul className="divide-y divide-line">
            {worklogs.map((w) => (
              <li key={w.id} className="py-2">
                <div className="flex items-baseline gap-2">
                  <span className="font-medium text-ink">{w.actor_name ?? 'Unknown actor'}</span>
                  <Badge tone={w.kind === 'deviation' ? 'warn' : 'neutral'}>{w.kind}</Badge>
                  <span className="text-[11px] text-ink-faint" title={formatTimestamp(w.created_at)}>
                    {formatRelative(w.created_at)}
                  </span>
                </div>
                <p className="mt-0.5 wrap-break-word whitespace-pre-wrap text-ink">{w.body}</p>
              </li>
            ))}
          </ul>
        )}

        <form
          className="mt-3 flex flex-col gap-2 border-t border-line pt-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (!body.trim()) return
            add.mutate({ body: body.trim(), kind }, { onSuccess: () => setBody('') })
          }}
        >
          <Textarea
            rows={2}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="What happened. Corrections are made by adding another entry, never by editing this one."
          />
          <div className="flex items-center gap-2">
            <Select
              value={kind}
              onChange={(e) => setKind(e.target.value as typeof kind)}
              className="h-7 w-auto text-xs"
            >
              {WORKLOG_KIND_VALUES.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </Select>
            <div className="flex-1" />
            <Button size="sm" type="submit" loading={add.isPending} disabled={!body.trim()}>
              Add entry
            </Button>
          </div>
          {/* frontend-notes §9: there is no route to edit or delete a worklog,
              which is what makes the log evidence rather than a field. */}
          <p className="text-[11px] text-ink-faint">
            Worklogs are never edited or deleted. Correct one by adding another.
          </p>
        </form>
      </div>
    </Card>
  )
}

// ── deviations ───────────────────────────────────────────────────────────────

function Deviations({
  projectId,
  deviations,
  loading,
}: {
  projectId: string
  deviations: ReturnType<typeof useDeviations>['data'] & object
  loading: boolean
}) {
  const own = useOwnership(projectId)
  const patch = usePatchDeviation(projectId)
  const [resolving, setResolving] = useState<string | null>(null)
  const [resolution, setResolution] = useState('')

  const resolvePerm = own.field('deviation', 'resolution')

  if (!loading && deviations.length === 0) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <ClipboardList className="h-3.5 w-3.5 text-ink-faint" />
          Deviations
        </CardTitle>
        <span className="zp-num text-[11px] text-ink-faint">
          {deviations.filter((x) => !x.resolved).length} open
        </span>
      </CardHeader>

      <div className="px-4 py-2">
        {loading ? (
          <Loading what="deviations" className="py-4" />
        ) : (
          <ul className="divide-y divide-line">
            {deviations.map((x) => (
              <li key={x.id} className="py-2">
                <div className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="text-ink">{x.summary}</p>
                    <p className="mt-0.5 flex items-center gap-2 text-[11px] text-ink-faint">
                      <SeverityBadge severity={x.severity} />
                      <span title={formatTimestamp(x.created_at)}>
                        {formatRelative(x.created_at)}
                      </span>
                      {x.resolved ? <Badge tone="good">resolved</Badge> : null}
                    </p>
                    {x.resolution ? (
                      <p className="mt-1 rounded border border-line bg-sunken px-2 py-1 text-ink-muted">
                        {x.resolution}
                      </p>
                    ) : null}
                  </div>
                  {!x.resolved && resolvePerm.writable ? (
                    <Button size="sm" variant="ghost" onClick={() => setResolving(x.id)}>
                      Resolve
                    </Button>
                  ) : null}
                </div>

                {resolving === x.id ? (
                  <form
                    className="mt-2 flex flex-col gap-2"
                    onSubmit={(e) => {
                      e.preventDefault()
                      patch.mutate(
                        { id: x.id, body: { resolution: resolution.trim() } },
                        {
                          onSuccess: () => {
                            setResolving(null)
                            setResolution('')
                          },
                        },
                      )
                    }}
                  >
                    <Textarea
                      rows={2}
                      autoFocus
                      value={resolution}
                      onChange={(e) => setResolution(e.target.value)}
                      placeholder="How it was resolved. Setting this resolves the deviation; clearing it reopens."
                    />
                    <div className="flex gap-2">
                      <Button size="sm" variant="primary" type="submit" loading={patch.isPending}>
                        Resolve
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setResolving(null)}>
                        Cancel
                      </Button>
                    </div>
                  </form>
                ) : null}

                {!x.resolved && !resolvePerm.writable && resolvePerm.why ? (
                  <OwnershipNote why={resolvePerm.why} className="mt-1" />
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  )
}

// ── dependencies and entity refs ─────────────────────────────────────────────

function Dependencies({
  projectId,
  detail,
}: {
  projectId: string
  detail: NonNullable<ReturnType<typeof useWorkItem>['data']>
}) {
  const groups = [
    { label: 'Blocked by', items: detail.blocked_by ?? [], tone: 'danger' as const },
    { label: 'Blocks', items: detail.blocks ?? [], tone: 'warn' as const },
    { label: 'Relates to', items: detail.relates ?? [], tone: 'neutral' as const },
  ].filter((g) => g.items.length > 0)

  if (groups.length === 0) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <Link2 className="h-3.5 w-3.5 text-ink-faint" />
          Dependencies
        </CardTitle>
      </CardHeader>
      <div className="px-4 py-2">
        {groups.map((g) => (
          <div key={g.label} className="py-1">
            <p className="text-[11px] tracking-wide text-ink-faint uppercase">{g.label}</p>
            <ul className="mt-0.5">
              {g.items.map((dep) => (
                <li key={dep.item_id + g.label} className="py-0.5">
                  <Link
                    to={`/p/${projectId}/plan/${dep.item_id}`}
                    className="flex items-baseline gap-1.5 hover:underline"
                  >
                    <span className="font-mono text-xs text-accent-ink">{dep.ref}</span>
                    <span className="min-w-0 truncate text-ink">{dep.title}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </Card>
  )
}

function EntityRefs({
  detail,
}: {
  detail: NonNullable<ReturnType<typeof useWorkItem>['data']>
}) {
  const refs = detail.implements ?? []
  if (refs.length === 0) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <Boxes className="h-3.5 w-3.5 text-ink-faint" />
          Implements
        </CardTitle>
      </CardHeader>
      <div className="px-4 py-2">
        {/* Set by a human or an agent, never derived (§3.5). They exist so
            context assembly can pull the right architecture slice. */}
        <ul className="flex flex-wrap gap-1.5">
          {refs.map((r) => (
            <li key={r.entity_id}>
              <Badge tone="info" title={r.slug}>
                {r.name}
              </Badge>
            </li>
          ))}
        </ul>
        <p className="mt-2 text-[11px] text-ink-faint">
          Soft references to architecture entities. Never derived from the diagram.
        </p>
      </div>
    </Card>
  )
}
