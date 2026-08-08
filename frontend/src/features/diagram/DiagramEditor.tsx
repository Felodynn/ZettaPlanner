import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  ArrowLeft,
  Lock,
  Redo2,
  Save,
  Undo2,
  X,
} from 'lucide-react'
import { GraphCanvas } from './GraphCanvas'
import { Palette } from './Palette'
import { EditorPanel } from './EditorPanel'
import { RelationPicker } from './RelationPicker'
import { useDiagram, diagramKeys } from './queries'
import { useEditor, toSavePayload } from './editorStore'
import { useLease } from './useLease'
import type { SemanticGraph } from './model'
import { client, list, unwrap } from '@/api/client'
import { ApiError } from '@/api/errors'
import { Badge, Loading } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { notifyError, notifyInfo, notifySuccess, notifyWarn } from '@/components/notify'
import { cn } from '@/lib/cn'
import type { Relation } from '@/api/vocabulary'

export function DiagramEditor() {
  const { projectId = '', diagramId = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()

  const diagram = useDiagram(diagramId)
  const lease = useLease(diagramId, Boolean(diagramId))

  // Selected individually, never as `useEditor()`. Subscribing to the whole
  // store hands back a new object every update, so anything holding it in a
  // dependency array re-runs on every keystroke — and an effect whose cleanup
  // resets the store then loops forever.
  const nodes = useEditor((s) => s.nodes)
  const edges = useEditor((s) => s.edges)
  const pendingLayout = useEditor((s) => s.pendingLayout)
  const baseRevision = useEditor((s) => s.baseRevision)
  const flippedToManual = useEditor((s) => s.flippedToManual)
  const past = useEditor((s) => s.past)
  const future = useEditor((s) => s.future)

  // Actions are defined once when the store is created, so these references
  // are stable and safe as dependencies.
  const begin = useEditor((s) => s.begin)
  const discard = useEditor((s) => s.discard)
  const rebase = useEditor((s) => s.rebase)
  const addNode = useEditor((s) => s.addNode)
  const addEdge = useEditor((s) => s.addEdge)
  const undo = useEditor((s) => s.undo)
  const redo = useEditor((s) => s.redo)

  const [selected, setSelected] = useState<string | undefined>()
  const [pendingEdge, setPendingEdge] = useState<{ from: string; to: string } | null>(null)
  const [conflict, setConflict] = useState<ApiError | null>(null)
  const [saving, setSaving] = useState(false)
  const noticedManual = useRef(false)
  const started = useRef(false)

  // Seed the working copy once, from the state the lease was taken against.
  useEffect(() => {
    if (started.current || !diagram.data) return
    started.current = true
    begin(
      { nodes: list(diagram.data.nodes), edges: list(diagram.data.edges), notes: list(diagram.data.notes) },
      diagram.data.revision,
    )
  }, [diagram.data, begin])

  useEffect(() => () => discard(), [discard])

  // Keyboard: undo/redo, and save.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey
      if (!mod) return
      // Lower-cased: holding shift makes `key` "Z", so matching on 'z'
      // silently loses redo on the shortcut most people reach for first.
      const key = e.key.toLowerCase()
      if (key === 'z' && !e.shiftKey) {
        e.preventDefault()
        undo()
      } else if ((key === 'z' && e.shiftKey) || key === 'y') {
        e.preventDefault()
        redo()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [undo, redo])

  // Leaving with unsaved semantic changes.
  useEffect(() => {
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (useEditor.getState().isDirty()) e.preventDefault()
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [])

  const graph: SemanticGraph = useMemo(
    () => ({ nodes, edges, notes: diagram.data ? list(diagram.data.notes) : [] }),
    [nodes, edges, diagram.data],
  )

  const storedLayout = useMemo(() => {
    const base = Object.fromEntries(
      graph.nodes.filter((n) => n.layout).map((n) => [n.id, n.layout!]),
    )
    for (const [nodeId, move] of Object.entries(pendingLayout)) {
      base[nodeId] = { ...(base[nodeId] ?? { collapsed: false, unplaced: false }), ...move }
    }
    return base
  }, [graph.nodes, pendingLayout])

  /**
   * Layout writes, debounced and entirely separate from the semantic save
   * (§1.1 rule 2). No lease, no revision — moving a box is not a semantic
   * change, and any project member may do it (§4.2).
   */
  const layoutTimer = useRef<number | undefined>(undefined)
  const flushLayout = useCallback(async () => {
    const pending = useEditor.getState().pendingLayout
    const entries = Object.entries(pending)
    if (entries.length === 0) return
    // A local node has no server id yet; its position lands with the next
    // flush, after the save gives it one.
    const placements = entries
      .filter(([nodeId]) => !nodeId.startsWith('new:'))
      .map(([node_id, p]) => ({ node_id, x: p.x, y: p.y }))
    if (placements.length === 0) return
    try {
      await client.PUT('/api/v1/diagrams/{id}/layout', {
        params: { path: { id: diagramId } },
        body: { nodes: placements },
      })
      useEditor.getState().clearPendingLayout()
      void qc.invalidateQueries({ queryKey: diagramKeys.detail(diagramId) })
    } catch (e) {
      notifyError(e)
    }
  }, [diagramId, qc])

  const onNodeMoved = useCallback(
    (slug: string, x: number, y: number) => {
      const node = useEditor.getState().nodes.find((n) => n.slug === slug)
      if (!node) return
      useEditor.getState().moveNode(node.id, x, y)

      // The first drag flips the diagram to manual, permanently. Said once,
      // plainly, because auto-layout will not rearrange it again (§3.2).
      if (!noticedManual.current && diagram.data?.layout_mode === 'auto') {
        noticedManual.current = true
        notifyWarn(
          'This diagram was arranged automatically. Moving a box switches it to manual arrangement permanently — automatic layout will not rearrange it again, and re-running it later discards everybody’s positions.',
          'Switched to manual arrangement',
        )
      }

      window.clearTimeout(layoutTimer.current)
      layoutTimer.current = window.setTimeout(() => void flushLayout(), 900)
    },
    [diagram.data?.layout_mode, flushLayout],
  )

  const save = async () => {
    if (lease.state.status !== 'held') return
    setSaving(true)
    try {
      const payload = toSavePayload(useEditor.getState())
      const result = await unwrap(
        client.POST('/api/v1/diagrams/{id}/revisions', {
          params: { path: { id: diagramId } },
          body: {
            base_revision: useEditor.getState().baseRevision,
            summary: describe(payload),
            ...payload,
          },
        }),
      )
      const fresh = await unwrap(
        client.GET('/api/v1/diagrams/{id}', { params: { path: { id: diagramId } } }),
      )
      rebase(
        { nodes: list(fresh.nodes), edges: list(fresh.edges), notes: list(fresh.notes) },
        fresh.revision,
      )
      void qc.invalidateQueries({ queryKey: diagramKeys.root })
      notifySuccess(`Saved as revision ${result.revision}.`)

      // Entity resolution is never automatic (§3.4): a plausible name match is
      // recorded as a note for a person to settle, not merged.
      const raised = list(result.notes_raised)
      if (raised.length > 0) {
        notifyInfo(
          `${raised.length} clarification${raised.length === 1 ? '' : 's'} recorded — most often a possible duplicate entity, kept as a question rather than merged.`,
          'Notes raised',
        )
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === 'stale_base_revision') {
        // Never auto-merge (§5.1). Show both sides and let the person choose.
        setConflict(e)
      } else {
        notifyError(e)
      }
    } finally {
      setSaving(false)
    }
  }

  if (diagram.isPending) return <Loading what="this diagram" />
  if (diagram.error) throw diagram.error

  const d = diagram.data
  const dirty = useEditor.getState().isDirty()
  const blocked = lease.state.status !== 'held'
  const containers = graph.nodes
    .filter((n) => n.kind === 'boundary' || n.kind === 'system')
    .map((n) => ({ slug: n.slug, name: n.name }))

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <Link
          to={`/p/${projectId}/diagrams/${diagramId}`}
          className="flex items-center gap-1 text-ink-faint hover:text-ink"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
        </Link>
        <h1 className="font-semibold text-ink">{d.name}</h1>
        <Badge tone="accent">editing</Badge>
        <Badge tone={flippedToManual || d.layout_mode === 'manual' ? 'accent' : 'neutral'}>
          {flippedToManual || d.layout_mode === 'manual' ? 'manual' : 'auto'} layout
        </Badge>
        <span className="zp-num text-[11px] text-ink-faint">
          base rev {baseRevision}
        </span>

        <div className="flex-1" />

        <Button size="sm" variant="ghost" onClick={undo} disabled={past.length === 0}>
          <Undo2 className="h-3.5 w-3.5" />
        </Button>
        <Button size="sm" variant="ghost" onClick={redo} disabled={future.length === 0}>
          <Redo2 className="h-3.5 w-3.5" />
        </Button>

        <Button
          size="sm"
          variant="primary"
          onClick={() => void save()}
          loading={saving}
          disabled={blocked || !dirty}
          title={
            blocked
              ? 'Saving is blocked: this tab does not hold the edit session.'
              : dirty
                ? 'Save every change as one revision'
                : 'Nothing has changed'
          }
        >
          {saving ? null : <Save className="h-3.5 w-3.5" />}
          Save
        </Button>
      </header>

      <LeaseBanner state={lease.state} onLeave={() => navigate(`/p/${projectId}/diagrams/${diagramId}`)} />

      <div className="flex min-h-0 flex-1">
        <Palette
          onAdd={(kind, name, parent) => setSelected(addNode(kind, name, parent))}
          parentOptions={containers}
        />

        <div className={cn('relative min-w-0 flex-1', blocked && 'pointer-events-none opacity-60')}>
          <GraphCanvas
            graph={graph}
            layout={storedLayout}
            layoutMode={flippedToManual || d.layout_mode === 'manual' ? 'manual' : 'auto'}
            mode="edit"
            selected={selected}
            onSelect={setSelected}
            onNodeMoved={onNodeMoved}
            onConnect={(from, to) => setPendingEdge({ from, to })}
          />
        </div>

        {selected ? (
          <EditorPanel
            nodes={graph.nodes}
            edges={graph.edges}
            selected={selected}
            onSelect={setSelected}
            onClose={() => setSelected(undefined)}
          />
        ) : null}
      </div>

      {pendingEdge ? (
        <RelationPicker
          from={graph.nodes.find((n) => n.slug === pendingEdge.from)?.name ?? pendingEdge.from}
          to={graph.nodes.find((n) => n.slug === pendingEdge.to)?.name ?? pendingEdge.to}
          onPick={(relation: Relation) => {
            addEdge(pendingEdge.from, pendingEdge.to, relation)
            setPendingEdge(null)
          }}
          onCancel={() => setPendingEdge(null)}
        />
      ) : null}

      {conflict ? (
        <ConflictDialog
          error={conflict}
          onDiscard={async () => {
            const fresh = await unwrap(
              client.GET('/api/v1/diagrams/{id}', { params: { path: { id: diagramId } } }),
            )
            rebase(
              { nodes: list(fresh.nodes), edges: list(fresh.edges), notes: list(fresh.notes) },
              fresh.revision,
            )
            setConflict(null)
          }}
          onCopyOut={() => {
            const payload = toSavePayload(useEditor.getState())
            void navigator.clipboard?.writeText(JSON.stringify(payload, null, 2))
            notifySuccess('Your version is on the clipboard as JSON.')
          }}
          onClose={() => setConflict(null)}
        />
      ) : null}
    </div>
  )
}

/** A one-line summary for the revision log and the git commit. */
function describe(payload: ReturnType<typeof toSavePayload>): string {
  return `${payload.nodes.length} nodes, ${payload.edges.length} edges`
}

function LeaseBanner({
  state,
  onLeave,
}: {
  state: ReturnType<typeof useLease>['state']
  onLeave: () => void
}) {
  if (state.status === 'held') return null

  if (state.status === 'acquiring') {
    return (
      <div className="flex items-center gap-2 border-b border-line bg-sunken px-4 py-1.5 text-ink-muted">
        <Lock className="h-3.5 w-3.5" />
        Taking the edit session…
      </div>
    )
  }

  const title =
    state.status === 'denied'
      ? 'Somebody else is editing this diagram'
      : state.status === 'forbidden'
        ? 'This diagram is not yours to edit'
        : state.status === 'lost'
          ? 'Your edit session ended'
          : 'The edit session was released'

  const body =
    state.status === 'denied' || state.status === 'forbidden'
      ? // The backend's sentence: it names the owning role, or the holder and
        // when their session lapses (§7.3).
        state.message
      : state.status === 'lost'
        ? state.reason
        : 'This tab was in the background for a while, so the diagram was released for somebody else to edit.'

  const requestId =
    state.status === 'denied' || state.status === 'forbidden' ? state.requestId : ''

  return (
    <div className="flex items-start gap-2 border-b border-danger/30 bg-danger-soft px-4 py-2">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
      <div className="min-w-0 flex-1">
        <p className="font-medium text-danger-ink">{title}</p>
        <p className="text-ink">{body}</p>
        <p className="mt-0.5 text-[11px] text-ink-muted">
          {state.status === 'forbidden'
            ? 'Nothing here can be saved. Waiting will not change that — the route forward is in the sentence above.'
            : 'Your changes are still on screen and can be copied out, but they cannot be saved from here.'}
        </p>
        {requestId ? (
          <p className="mt-0.5 font-mono text-[11px] text-ink-faint">{requestId}</p>
        ) : null}
      </div>
      <Button size="sm" variant="secondary" onClick={onLeave}>
        View instead
      </Button>
    </div>
  )
}

function ConflictDialog({
  error,
  onDiscard,
  onCopyOut,
  onClose,
}: {
  error: ApiError
  onDiscard: () => void | Promise<void>
  onCopyOut: () => void
  onClose: () => void
}) {
  const revisions = Array.isArray(error.details.revisions_since)
    ? (error.details.revisions_since as { seq?: number; actor?: string; summary?: string }[])
    : []

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-canvas/70 p-6">
      <div className="zp-shadow-pop w-full max-w-lg rounded-card border border-line bg-raised">
        <div className="flex items-start gap-2 border-b border-line px-4 py-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" />
          <div className="min-w-0 flex-1">
            <p className="font-semibold text-ink">Somebody else saved first</p>
            <p className="mt-0.5 text-ink">{error.message}</p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="px-4 py-3">
          {revisions.length > 0 ? (
            <>
              <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
                Landed since you started
              </p>
              <ul className="mt-1 flex flex-col gap-1">
                {revisions.map((r, i) => (
                  <li key={i} className="flex items-baseline gap-2 text-ink">
                    <span className="zp-num font-mono text-xs text-ink-faint">rev {r.seq}</span>
                    <span className="min-w-0 flex-1">{r.summary ?? 'no summary'}</span>
                    {r.actor ? <span className="text-xs text-ink-faint">{r.actor}</span> : null}
                  </li>
                ))}
              </ul>
            </>
          ) : null}

          {/* §5.1: never auto-merge. The two options are the only honest ones. */}
          <p className="mt-3 text-ink-muted">
            Nothing was merged and nothing was applied. Merging two whole-diagram saves without
            being asked is how somebody's work disappears quietly, so this is a choice rather than
            a retry.
          </p>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-line px-4 py-3">
          <Button variant="secondary" onClick={onCopyOut}>
            Copy my version out
          </Button>
          <Button variant="danger" onClick={() => void onDiscard()}>
            Discard mine and reload
          </Button>
        </div>
      </div>
    </div>
  )
}
