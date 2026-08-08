import { Link, useParams } from 'react-router-dom'
import { Network } from 'lucide-react'
import { useDiagrams } from './queries'
import { Badge, Card, EmptyState, Loading } from '@/components/ui/primitives'

export function DiagramList() {
  const { projectId = '' } = useParams()
  const diagrams = useDiagrams(projectId)

  if (diagrams.isPending) return <Loading what="diagrams" />
  if (diagrams.error) throw diagrams.error

  return (
    <div className="mx-auto w-full max-w-3xl px-6 py-6">
      <div className="flex items-center gap-2">
        <Network className="h-4 w-4 text-ink-faint" />
        <h1 className="text-base font-semibold text-ink">Diagrams</h1>
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
    </div>
  )
}
