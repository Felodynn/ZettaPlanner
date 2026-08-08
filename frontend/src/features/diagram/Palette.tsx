import { useState } from 'react'
import { Plus } from 'lucide-react'
import { NODE_KIND_VALUES, type NodeKind } from '@/api/vocabulary'
import { KIND } from './vocabulary'
import { Input } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'

/**
 * The palette **is** the schema (D6, F4).
 *
 * No free-form node type and no free-form relation. There is deliberately no
 * "other" box and no way to type a kind: if the vocabulary feels short that is
 * a decision entry and a backend migration, not a text input. The list comes
 * from the generated vocabulary, so it cannot drift from what the server will
 * accept.
 */
export function Palette({
  onAdd,
  parentOptions,
}: {
  onAdd: (kind: NodeKind, name: string, parent?: string) => void
  /** Boundaries and systems already on the diagram. */
  parentOptions: { slug: string; name: string }[]
}) {
  const [kind, setKind] = useState<NodeKind>('service')
  const [name, setName] = useState('')
  const [parent, setParent] = useState('')

  const submit = () => {
    const trimmed = name.trim()
    if (!trimmed) return
    onAdd(kind, trimmed, parent || undefined)
    setName('')
  }

  return (
    <div className="flex w-56 shrink-0 flex-col border-r border-line bg-surface">
      <div className="border-b border-line px-3 py-2">
        <p className="font-semibold text-ink">Add</p>
        <p className="text-[11px] leading-snug text-ink-faint">
          The vocabulary is closed. A kind that is not here is a migration, not a setting.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-1 p-2">
        {NODE_KIND_VALUES.map((k) => {
          const spec = KIND[k]
          const active = kind === k
          return (
            <button
              key={k}
              type="button"
              onClick={() => setKind(k)}
              title={spec.means}
              className={cn(
                'flex items-center gap-1.5 rounded-md border px-1.5 py-1.5 text-left transition-colors',
                active
                  ? 'border-accent-line bg-accent-soft text-accent-ink'
                  : 'border-line text-ink-muted hover:bg-sunken hover:text-ink',
              )}
            >
              <spec.Icon
                className="h-3.5 w-3.5 shrink-0"
                style={{ color: active ? undefined : spec.hue }}
              />
              <span className="truncate text-[11px]">{spec.label}</span>
            </button>
          )
        })}
      </div>

      <div className="flex flex-col gap-2 border-t border-line px-2 py-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit()
          }}
          placeholder={`${KIND[kind].label} name`}
          className="h-7 text-xs"
        />

        {parentOptions.length > 0 && kind !== 'note' ? (
          <select
            value={parent}
            onChange={(e) => setParent(e.target.value)}
            className="h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
          >
            <option value="">Not inside anything</option>
            {parentOptions.map((p) => (
              <option key={p.slug} value={p.slug}>
                Inside {p.name}
              </option>
            ))}
          </select>
        ) : null}

        <Button size="sm" variant="primary" onClick={submit} disabled={!name.trim()}>
          <Plus className="h-3 w-3" />
          Add {KIND[kind].label.toLowerCase()}
        </Button>
      </div>

      <p className="mt-auto border-t border-line px-3 py-2 text-[11px] leading-snug text-ink-faint">
        Drag from a node's right edge to another to draw a relation. Moving a box is not a semantic
        change and saves separately.
      </p>
    </div>
  )
}
