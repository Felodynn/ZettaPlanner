import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Network, Plus } from 'lucide-react'
import { useCreateDiagram, useDiagrams } from './queries'
import { Badge, Card, EmptyState, Input, Loading, Select } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { Dialog, Field } from '@/components/ui/Dialog'
import { DIAGRAM_LEVEL_VALUES, type DiagramLevel } from '@/api/vocabulary'
import { useOwnership } from '@/features/plan/ownership'
import { notifySuccess } from '@/components/notify'

export function DiagramList() {
  const { projectId = '' } = useParams()
  const diagrams = useDiagrams(projectId)
  // A diagram's semantic layer is manager-owned in full (§4.2).
  const canCreate = useOwnership(projectId).field('diagram', '*').writable
  const [creating, setCreating] = useState(false)

  if (diagrams.isPending) return <Loading what="diagrams" />
  if (diagrams.error) throw diagrams.error

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <Network className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Diagrams</h1>
        <div className="flex-1" />
        {canCreate ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus className="h-3.5 w-3.5" />
            New diagram
          </Button>
        ) : null}
      </div>
      <p className="mt-0.5 text-ink-muted">
        The architecture graph. Linked to the plan, and never derived from it (§3.5).
      </p>

      {diagrams.data.length === 0 ? (
        <EmptyState
          className="pt-16"
          icon={<Network className="h-7 w-7" />}
          title="No diagrams yet"
          body="Draw one by hand and ingest the photograph, or create an empty diagram and edit it directly."
          {...(canCreate
            ? {
                action: (
                  <Button variant="primary" onClick={() => setCreating(true)}>
                    <Plus className="h-3.5 w-3.5" />
                    New diagram
                  </Button>
                ),
              }
            : {})}
        />
      ) : (
        <div className="mt-4 flex flex-col gap-2">
          {diagrams.data.map((d) => (
            <Link key={d.id} to={`/p/${projectId}/diagrams/${d.id}`}>
              <Card className="flex items-center gap-3 px-4 py-3 transition-colors hover:border-accent-line hover:bg-accent-soft/40">
                <Network className="h-4 w-4 shrink-0 text-ink-faint" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-ink">{d.name}</span>
                  <span className="block font-mono text-[11px] text-ink-faint">{d.slug}</span>
                </span>
                <Badge tone="outline">{d.level}</Badge>
                <Badge tone={d.layout_mode === 'manual' ? 'accent' : 'neutral'}>
                  {d.layout_mode}
                </Badge>
                <span className="zp-num shrink-0 text-[11px] text-ink-faint">rev {d.revision}</span>
              </Card>
            </Link>
          ))}
        </div>
      )}
      {creating ? <CreateDiagramDialog onClose={() => setCreating(false)} /> : null}
    </div>
  )
}

const LEVEL_MEANS: Record<DiagramLevel, string> = {
  context: 'The system and what is outside it. Who uses it, what it talks to.',
  container: 'The deployable pieces inside one system, and how they communicate.',
  component: 'The parts inside one container. The tightest altitude there is.',
}

function CreateDiagramDialog({ onClose }: { onClose: () => void }) {
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const create = useCreateDiagram(projectId)
  const [name, setName] = useState('')
  const [level, setLevel] = useState<DiagramLevel>('container')

  return (
    <Dialog
      title="New diagram"
      description="Empty to start. Add nodes in the editor, or accept an ingested drawing into it later."
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
                { name: name.trim(), level },
                {
                  onSuccess: (d) => {
                    notifySuccess(`${d.name} created.`)
                    onClose()
                    navigate(`/p/${projectId}/diagrams/${d.id}/edit`)
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
          placeholder="Platform overview"
        />
      </Field>
      <Field
        label="Altitude"
        hint={`${LEVEL_MEANS[level]} Permanent in practice — the three answer different questions, and one drawn at the wrong altitude is mislabelled for good.`}
      >
        <Select value={level} onChange={(e) => setLevel(e.target.value as DiagramLevel)}>
          {DIAGRAM_LEVEL_VALUES.map((l) => (
            <option key={l} value={l}>
              {l}
            </option>
          ))}
        </Select>
      </Field>
    </Dialog>
  )
}

