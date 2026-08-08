import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Dialog, Field } from '@/components/ui/Dialog'
import { Input, Select, Textarea } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { useCreateMilestone, useCreateWorkItem, useMilestones } from './queries'
import { useMembers } from '@/features/project/queries'
import { MILESTONE_STATUS_VALUES, PRIORITY_VALUES, WORK_ITEM_STATUS_VALUES } from '@/api/vocabulary'
import type { MilestoneStatus, Priority, WorkItemStatus } from '@/api/vocabulary'
import { notifySuccess } from '@/components/notify'

/**
 * Creating plan objects.
 *
 * Both forms ask for the minimum and no more. A work item needs a title; every
 * other field has a right answer that is often "not yet", and a create form
 * that demands them is a create form people work around by typing placeholders
 * — which is worse than an empty field, because a placeholder looks like a
 * decision.
 *
 * **Priority is not defaulted.** An unset priority means untriaged and an
 * explicit `normal` means somebody judged it ordinary (§3.5a); the dashboard
 * counts them separately, so pre-selecting one would put every new item into
 * the wrong count.
 */

export function CreateMilestoneDialog({ onClose }: { onClose: () => void }) {
  const { projectId = '' } = useParams()
  const create = useCreateMilestone(projectId)
  const [name, setName] = useState('')
  const [intent, setIntent] = useState('')
  const [status, setStatus] = useState<MilestoneStatus>('planned')
  const [startDate, setStartDate] = useState('')
  const [dueDate, setDueDate] = useState('')

  return (
    <Dialog
      title="New milestone"
      description="A grouping with dates. Its status is deliberately coarser than an item's — a milestone is not blocked or in review; those are facts about the items inside it."
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={create.isPending}
            disabled={!name.trim() || create.isPending}
            onClick={() =>
              create.mutate(
                {
                  name: name.trim(),
                  status,
                  ...(intent.trim() ? { intent: intent.trim() } : {}),
                  ...(startDate ? { start_date: startDate } : {}),
                  ...(dueDate ? { due_date: dueDate } : {}),
                },
                {
                  onSuccess: () => {
                    notifySuccess(`${name.trim()} created.`)
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
      <Field label="Name">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="M1 — Auth hardening" />
      </Field>
      <Field label="Intent" hint="What this milestone is for. Read by the planner as context.">
        <Textarea rows={2} value={intent} onChange={(e) => setIntent(e.target.value)} />
      </Field>
      <div className="mb-3 flex gap-2">
        <Field label="Starts">
          {/* Calendar days, kept as strings end to end (frontend-notes §10). */}
          <Input type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} />
        </Field>
        <Field label="Due">
          <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
        </Field>
      </div>
      <Field label="Status">
        <Select value={status} onChange={(e) => setStatus(e.target.value as MilestoneStatus)}>
          {MILESTONE_STATUS_VALUES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </Select>
      </Field>
    </Dialog>
  )
}

export function CreateWorkItemDialog({
  onClose,
  defaultMilestoneId,
}: {
  onClose: () => void
  defaultMilestoneId?: string
}) {
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const create = useCreateWorkItem(projectId)
  const milestones = useMilestones(projectId)
  const members = useMembers(projectId)

  const [title, setTitle] = useState('')
  const [intent, setIntent] = useState('')
  const [acceptance, setAcceptance] = useState('')
  const [status, setStatus] = useState<WorkItemStatus>('todo')
  const [priority, setPriority] = useState<Priority | ''>('')
  const [assignee, setAssignee] = useState('')
  const [milestone, setMilestone] = useState(defaultMilestoneId ?? '')
  const [startDate, setStartDate] = useState('')
  const [dueDate, setDueDate] = useState('')

  return (
    <Dialog
      title="New work item"
      description="It gets a ref as soon as it is created — issued from the project key, and never reissued."
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={create.isPending}
            disabled={!title.trim() || create.isPending}
            onClick={() =>
              create.mutate(
                {
                  title: title.trim(),
                  status,
                  ...(intent.trim() ? { intent: intent.trim() } : {}),
                  ...(acceptance.trim() ? { acceptance_criteria: acceptance.trim() } : {}),
                  ...(priority ? { priority } : {}),
                  ...(assignee ? { assignee_id: assignee } : {}),
                  ...(milestone ? { milestone_id: milestone } : {}),
                  ...(startDate ? { start_date: startDate } : {}),
                  ...(dueDate ? { due_date: dueDate } : {}),
                },
                {
                  onSuccess: (item) => {
                    notifySuccess(`${item.ref} created.`)
                    onClose()
                    navigate(`/p/${projectId}/plan/${item.id}`)
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
      <Field label="Title">
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Revoke sessions on role change"
        />
      </Field>
      <Field label="Intent" hint="Why this exists. Worth writing — it is what the planner reads.">
        <Textarea rows={2} value={intent} onChange={(e) => setIntent(e.target.value)} />
      </Field>
      <Field label="Acceptance criteria" hint="What has to be true to call this done.">
        <Textarea rows={2} value={acceptance} onChange={(e) => setAcceptance(e.target.value)} />
      </Field>

      <div className="mb-3 flex gap-2">
        <Field label="Status">
          <Select value={status} onChange={(e) => setStatus(e.target.value as WorkItemStatus)}>
            {WORK_ITEM_STATUS_VALUES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </Field>
        <Field
          label="Priority"
          hint="Leave untriaged until somebody has judged it — that is a different fact from normal."
        >
          <Select value={priority} onChange={(e) => setPriority(e.target.value as Priority | '')}>
            <option value="">untriaged</option>
            {PRIORITY_VALUES.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        </Field>
      </div>

      <div className="mb-3 flex gap-2">
        <Field label="Assignee">
          <Select value={assignee} onChange={(e) => setAssignee(e.target.value)}>
            <option value="">Unassigned</option>
            {(members.data ?? []).map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.display_name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Milestone">
          <Select value={milestone} onChange={(e) => setMilestone(e.target.value)}>
            <option value="">None</option>
            {(milestones.data ?? []).map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </Select>
        </Field>
      </div>

      <div className="flex gap-2">
        <Field label="Starts">
          <Input type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} />
        </Field>
        <Field label="Due">
          <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
        </Field>
      </div>
    </Dialog>
  )
}
