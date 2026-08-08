import { useState } from 'react'
import { Pencil } from 'lucide-react'
import { Dialog, Field } from '@/components/ui/Dialog'
import { Badge, Input } from '@/components/ui/primitives'
import { Button } from '@/components/ui/Button'
import {
  useEntities,
  useSetDependencies,
  useSetEntityRefs,
  useWorkItems,
  type WorkItemDetail,
} from './queries'
import { notifyError, notifySuccess } from '@/components/notify'
import { cn } from '@/lib/cn'

/**
 * Editing what an item is wired to.
 *
 * Both are whole-set PUTs, which is not an API accident: a `blocks` cycle is
 * rejected over the *graph*, not over one edge, so adding one link at a time
 * would mean a check that can only fail after the fact. Sending the set means
 * the refusal names the cycle path before anything is written.
 */

export function EditDependencies({
  projectId,
  detail,
  onClose,
}: {
  projectId: string
  detail: WorkItemDetail
  onClose: () => void
}) {
  const save = useSetDependencies(projectId, detail.id)
  const all = useWorkItems(projectId, {})

  const [blocks, setBlocks] = useState<string[]>((detail.blocks ?? []).map((d) => d.item_id))
  const [relates, setRelates] = useState<string[]>((detail.relates ?? []).map((d) => d.item_id))
  const [query, setQuery] = useState('')

  // `blocked_by` is the other side of somebody else's `blocks`, so it is not
  // editable from here — it would mean writing a link onto another item.
  const candidates = (all.data ?? []).filter(
    (i) =>
      i.id !== detail.id &&
      !i.deleted_at &&
      (query.trim() === '' ||
        `${i.ref} ${i.title}`.toLowerCase().includes(query.trim().toLowerCase())),
  )

  const toggle = (list: string[], set: (v: string[]) => void, id: string) =>
    set(list.includes(id) ? list.filter((x) => x !== id) : [...list, id])

  return (
    <Dialog
      title={`Dependencies for ${detail.ref}`}
      description="Between work items, not a tree. Nothing is rescheduled by a link — a violated date produces a warning and nothing else, deliberately."
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={save.isPending}
            disabled={save.isPending}
            onClick={() =>
              save.mutate(
                { blocks, relates },
                {
                  onSuccess: () => {
                    notifySuccess('Dependencies saved.')
                    onClose()
                  },
                  // A `blocks` cycle comes back as a 400 naming the path. It is
                  // worth reading, so it is surfaced rather than swallowed.
                  onError: (e) => notifyError(e),
                },
              )
            }
          >
            Save
          </Button>
        </>
      }
    >
      {(detail.blocked_by ?? []).length > 0 ? (
        <p className="mb-3 rounded-md border border-line bg-sunken px-3 py-2 text-ink-muted">
          {detail.ref} is blocked by{' '}
          {(detail.blocked_by ?? []).map((d) => d.ref).join(', ')}. That link lives on the other
          item — edit it there.
        </p>
      ) : null}

      <Field label="Find an item">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="ref or title"
        />
      </Field>

      <div className="mt-1 max-h-80 overflow-auto rounded-md border border-line zp-scroll">
        <table className="w-full">
          <thead className="sticky top-0 bg-surface">
            <tr className="border-b border-line text-left text-[11px] tracking-wide text-ink-faint uppercase">
              <th className="px-3 py-1.5 font-semibold">Item</th>
              <th className="w-24 px-2 py-1.5 text-center font-semibold" title="This item must finish before that one">
                Blocks
              </th>
              <th className="w-24 px-2 py-1.5 text-center font-semibold" title="Worth reading together; no ordering">
                Relates
              </th>
            </tr>
          </thead>
          <tbody>
            {candidates.map((i) => (
              <tr key={i.id} className="border-b border-line/60 last:border-b-0">
                <td className="px-3 py-1.5">
                  <span className="font-mono text-xs text-ink-faint">{i.ref}</span>{' '}
                  <span className="text-ink">{i.title}</span>
                </td>
                <td className="px-2 py-1.5 text-center">
                  <input
                    type="checkbox"
                    checked={blocks.includes(i.id)}
                    onChange={() => toggle(blocks, setBlocks, i.id)}
                    aria-label={`${detail.ref} blocks ${i.ref}`}
                    className="h-4 w-4 accent-[var(--zp-accent)]"
                  />
                </td>
                <td className="px-2 py-1.5 text-center">
                  <input
                    type="checkbox"
                    checked={relates.includes(i.id)}
                    onChange={() => toggle(relates, setRelates, i.id)}
                    aria-label={`${detail.ref} relates to ${i.ref}`}
                    className="h-4 w-4 accent-[var(--zp-accent)]"
                  />
                </td>
              </tr>
            ))}
            {candidates.length === 0 ? (
              <tr>
                <td colSpan={3} className="px-3 py-4 text-center text-ink-muted">
                  Nothing matches.
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>

      <p className="mt-2 text-[11px] text-ink-faint">
        A cycle in <code>blocks</code> is refused at save with the path named. `relates` has no
        ordering, so cycles there are fine.
      </p>
    </Dialog>
  )
}

export function EditEntityRefs({
  projectId,
  detail,
  onClose,
}: {
  projectId: string
  detail: WorkItemDetail
  onClose: () => void
}) {
  const save = useSetEntityRefs(projectId, detail.id)
  const entities = useEntities(projectId)
  const [slugs, setSlugs] = useState<string[]>((detail.implements ?? []).map((r) => r.slug))
  const [query, setQuery] = useState('')

  const list = (entities.data ?? []).filter(
    (e) =>
      query.trim() === '' ||
      `${e.slug} ${e.name}`.toLowerCase().includes(query.trim().toLowerCase()),
  )

  return (
    <Dialog
      title={`What ${detail.ref} implements`}
      description="Soft references to architecture entities. Set by a person or an agent, never derived from a diagram — a derivation that is wrong one time in ten is a permanent source of confusion."
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={save.isPending}
            disabled={save.isPending}
            onClick={() =>
              save.mutate(slugs, {
                onSuccess: () => {
                  notifySuccess('Saved.')
                  onClose()
                },
              })
            }
          >
            Save
          </Button>
        </>
      }
    >
      <Field label="Find an entity">
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="name or slug" />
      </Field>

      {entities.isPending ? (
        <p className="py-4 text-center text-ink-muted">Loading the catalog…</p>
      ) : list.length === 0 ? (
        <p className="py-4 text-center text-ink-muted">
          {(entities.data ?? []).length === 0
            ? 'This project has no architecture entities yet. They appear as diagrams gain nodes.'
            : 'Nothing matches.'}
        </p>
      ) : (
        <div className="max-h-80 overflow-auto rounded-md border border-line zp-scroll">
          {list.map((e) => {
            const on = slugs.includes(e.slug)
            return (
              <label
                key={e.id}
                className={cn(
                  'flex cursor-pointer items-center gap-2 border-b border-line/60 px-3 py-1.5 last:border-b-0',
                  on && 'bg-accent-soft',
                )}
              >
                <input
                  type="checkbox"
                  checked={on}
                  onChange={() =>
                    setSlugs(on ? slugs.filter((s) => s !== e.slug) : [...slugs, e.slug])
                  }
                  className="h-4 w-4 accent-[var(--zp-accent)]"
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-ink">{e.name}</span>
                  <span className="block truncate font-mono text-[11px] text-ink-faint">
                    {e.slug}
                  </span>
                </span>
                <Badge tone="outline">{e.kind}</Badge>
              </label>
            )
          })}
        </div>
      )}
    </Dialog>
  )
}

/** The pencil that opens either editor, shown only when the caller may write. */
export function EditLinksButton({
  label,
  onClick,
  disabled,
  why,
}: {
  label: string
  onClick: () => void
  disabled: boolean
  why?: string
}) {
  return (
    <Button
      size="sm"
      variant="ghost"
      onClick={onClick}
      disabled={disabled}
      title={disabled ? why : label}
      aria-label={label}
    >
      <Pencil className="h-3 w-3" />
      Edit
    </Button>
  )
}

