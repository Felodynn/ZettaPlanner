import { Trash2, X } from 'lucide-react'
import type { EdgeView, NodeView } from './model'
import { KIND, RELATION, canContain } from './vocabulary'
import { NODE_KIND_VALUES, RELATION_VALUES, type NodeKind, type Relation } from '@/api/vocabulary'
import { Badge, Input, Select, Textarea } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { useEditor } from './editorStore'

/**
 * Editing whatever is selected.
 *
 * Every control here is a closed list or free text where the contract allows
 * free text — a node's name and summary, an edge's label. Kind and relation
 * are pickers over the generated vocabulary, so this panel cannot express
 * something the server would refuse.
 */
export function EditorPanel({
  nodes,
  edges,
  selected,
  onSelect,
  onClose,
}: {
  nodes: NodeView[]
  edges: EdgeView[]
  selected: string
  onSelect: (slug: string | undefined) => void
  onClose: () => void
}) {
  const editor = useEditor()
  const node = nodes.find((n) => n.slug === selected)
  if (!node) return null

  const spec = KIND[node.kind]
  const outgoing = edges.filter((e) => e.from === selected)
  const incoming = edges.filter((e) => e.to === selected)
  const containers = nodes.filter(
    (n) => canContain(n.kind) && n.slug !== node.slug && n.parent !== node.slug,
  )

  return (
    <aside className="flex w-80 shrink-0 flex-col overflow-y-auto border-l border-line bg-surface zp-scroll">
      <div className="flex items-start gap-2 border-b border-line px-3 py-2.5">
        <spec.Icon className="mt-0.5 h-4 w-4 shrink-0" style={{ color: spec.hue }} />
        <div className="min-w-0 flex-1">
          <p className="wrap-break-word font-semibold text-ink">{node.name}</p>
          <p className="font-mono text-[11px] text-ink-faint">{node.slug}</p>
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="flex flex-col gap-3 px-3 py-3">
        <Field label="Name">
          <Input
            value={node.name}
            onChange={(e) => editor.renameNode(node.slug, e.target.value)}
            className="h-7 text-xs"
          />
        </Field>

        <Field label="Kind">
          <Select
            value={node.kind}
            onChange={(e) => editor.setNodeKind(node.slug, e.target.value as NodeKind)}
            className="h-7 text-xs"
          >
            {NODE_KIND_VALUES.map((k) => (
              <option key={k} value={k}>
                {KIND[k].label}
              </option>
            ))}
          </Select>
          <p className="mt-0.5 text-[11px] text-ink-faint">{spec.means}</p>
        </Field>

        <Field label="Summary">
          <Textarea
            rows={3}
            value={node.summary ?? ''}
            onChange={(e) => editor.setNodeSummary(node.slug, e.target.value)}
            placeholder="What it is, in a line"
            className="text-xs"
          />
        </Field>

        {node.kind !== 'note' ? (
          <Field label="Inside">
            <Select
              value={node.parent ?? ''}
              onChange={(e) => editor.reparent(node.slug, e.target.value || undefined)}
              className="h-7 text-xs"
            >
              <option value="">Nothing</option>
              {containers.map((c) => (
                <option key={c.slug} value={c.slug}>
                  {c.name}
                </option>
              ))}
            </Select>
            {containers.length === 0 ? (
              <p className="mt-0.5 text-[11px] text-ink-faint">
                Only a boundary or a system may contain other nodes.
              </p>
            ) : null}
          </Field>
        ) : null}

        {node.provenance === 'vlm_extracted' ? (
          <div className="rounded border border-warn/30 bg-warn-soft px-2 py-1.5">
            <Badge tone="warn">unchecked extraction</Badge>
            <p className="mt-1 text-[11px] leading-snug text-warn-ink">
              {/* §5.4: a correction here is the highest-signal input the
                  re-evaluate flow will ever receive. */}
              A vision model read this off a drawing. Correcting it marks it as read by a person,
              and that correction is what tells the model where its reading was weak.
            </p>
          </div>
        ) : null}

        {node.kind !== 'note' ? (
          <>
            <EdgeList
              title="Outgoing"
              edges={outgoing}
              nodes={nodes}
              end="to"
              onSelect={onSelect}
            />
            <EdgeList
              title="Incoming"
              edges={incoming}
              nodes={nodes}
              end="from"
              onSelect={onSelect}
            />
          </>
        ) : (
          <p className="text-[11px] text-ink-faint">A note carries no relations (§3.3).</p>
        )}

        <Button
          variant="danger"
          size="sm"
          onClick={() => {
            editor.deleteNode(node.slug)
            onSelect(undefined)
          }}
        >
          <Trash2 className="h-3 w-3" />
          Delete this node
        </Button>
      </div>
    </aside>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="mb-0.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
        {label}
      </p>
      {children}
    </div>
  )
}

function EdgeList({
  title,
  edges,
  nodes,
  end,
  onSelect,
}: {
  title: string
  edges: EdgeView[]
  nodes: NodeView[]
  end: 'to' | 'from'
  onSelect: (slug: string | undefined) => void
}) {
  const editor = useEditor()
  if (edges.length === 0) return null
  const bySlug = new Map(nodes.map((n) => [n.slug, n]))

  return (
    <div>
      <p className="mb-1 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
        {title} ({edges.length})
      </p>
      <ul className="flex flex-col gap-1.5">
        {edges.map((e) => {
          const otherSlug = end === 'to' ? e.to : e.from
          return (
            <li key={e.id} className="rounded border border-line px-1.5 py-1">
              <div className="flex items-center gap-1">
                <Select
                  value={e.relation}
                  onChange={(ev) => editor.setEdgeRelation(e.id, ev.target.value as Relation)}
                  className="h-6 flex-1 text-[11px]"
                >
                  {RELATION_VALUES.map((r) => (
                    <option key={r} value={r}>
                      {RELATION[r].label}
                    </option>
                  ))}
                </Select>
                <Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6"
                  onClick={() => editor.deleteEdge(e.id)}
                  aria-label="Delete this relation"
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
              <button
                type="button"
                onClick={() => onSelect(otherSlug)}
                className="mt-0.5 block w-full truncate text-left text-xs text-accent-ink hover:underline"
              >
                {bySlug.get(otherSlug)?.name ?? otherSlug}
              </button>
              <Input
                value={e.label ?? ''}
                onChange={(ev) => editor.setEdgeLabel(e.id, ev.target.value)}
                placeholder="Label, e.g. POST /auth/token"
                className="mt-1 h-6 text-[11px]"
              />
            </li>
          )
        })}
      </ul>
    </div>
  )
}
