import { useCallback, useRef, useState, type DragEvent } from 'react'
import { FileText, ImageIcon, Upload, X } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/primitives'
import { notifyError } from '@/components/notify'
import { cn } from '@/lib/cn'

/**
 * The upload.
 *
 * Drag-drop, several photographs, or one PDF — and never both at once. That is
 * not a UI preference: **one ingest is one document**, and a PDF beside loose
 * photographs would merge across drawings that were never on the same desk.
 * The backend refuses it; refusing it here as well means nobody assembles a
 * batch, waits for the upload, and is turned away by a sentence.
 *
 * Order matters and is the user's. Page 1 is page 1 because it is first in the
 * list, and "continues on the next page" is meaningless if the pages arrive in
 * whatever order the file picker felt like.
 */

const IMAGE_TYPES = /^image\/(jpeg|png|gif|webp|bmp)$/
const MAX_BYTES = 64 * 1024 * 1024

function isPdf(f: File): boolean {
  return f.type === 'application/pdf' || /\.pdf$/i.test(f.name)
}

function isImage(f: File): boolean {
  return IMAGE_TYPES.test(f.type) || /\.(jpe?g|png|gif|webp|bmp)$/i.test(f.name)
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} kB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export function Uploader({
  onUpload,
  uploading,
}: {
  onUpload: (files: File[]) => void
  uploading: boolean
}) {
  const [files, setFiles] = useState<File[]>([])
  const [over, setOver] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  const add = useCallback((incoming: File[]) => {
    if (incoming.length === 0) return
    setFiles((current) => {
      const next = [...current]
      for (const f of incoming) {
        if (!isPdf(f) && !isImage(f)) {
          notifyError(
            new Error(
              `${f.name} is neither an image nor a PDF. A drawing has to arrive as a photograph (JPEG, PNG, GIF, WebP or BMP) or as a PDF.`,
            ),
          )
          continue
        }
        if (next.some((e) => e.name === f.name && e.size === f.size)) continue
        next.push(f)
      }
      return next
    })
  }, [])

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setOver(false)
    add([...e.dataTransfer.files])
  }

  const move = (from: number, to: number) => {
    setFiles((current) => {
      if (to < 0 || to >= current.length) return current
      const next = [...current]
      const [moved] = next.splice(from, 1)
      if (!moved) return current
      next.splice(to, 0, moved)
      return next
    })
  }

  const pdfs = files.filter(isPdf)
  const images = files.filter(isImage)
  const total = files.reduce((sum, f) => sum + f.size, 0)

  // The two refusals the backend makes, said before the upload rather than
  // after it. Both quote the reason, because the reason is the whole point.
  const refusal =
    pdfs.length > 0 && images.length > 0
      ? 'One ingest is one document. A PDF and loose photographs in the same upload would be merged across drawings that were never on the same desk — send them as two ingests.'
      : pdfs.length > 1
        ? 'One PDF per ingest. Two PDFs are two documents, and merging them would join pages that were never part of the same drawing.'
        : total > MAX_BYTES
          ? `That is ${formatBytes(total)}. The limit is 64 MB per ingest — about a dozen phone photographs or a scanned notebook.`
          : ''

  return (
    <Card className="overflow-hidden">
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={onDrop}
        onClick={() => input.current?.click()}
        className={cn(
          'flex cursor-pointer flex-col items-center gap-1.5 border-b border-dashed border-line-strong px-6 py-10 text-center transition-colors',
          over ? 'border-accent bg-accent-soft' : 'hover:bg-sunken',
        )}
      >
        <Upload className="h-6 w-6 text-ink-faint" />
        <p className="font-medium text-ink">Drop a drawing here</p>
        <p className="max-w-md text-ink-muted">
          Photographs of a whiteboard or a notebook, or one PDF. Each page is read on its own — one
          model call per page, never one for the document — so a page that comes out badly can be
          re-read without touching the others.
        </p>
        <input
          ref={input}
          type="file"
          multiple
          accept="image/jpeg,image/png,image/gif,image/webp,image/bmp,application/pdf"
          className="hidden"
          onChange={(e) => {
            add([...(e.target.files ?? [])])
            e.target.value = ''
          }}
        />
      </div>

      {files.length > 0 ? (
        <div className="flex flex-col gap-1 px-3 py-3">
          <p className="text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            {files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(total)} · pages are
            read in this order
          </p>

          {files.map((f, i) => (
            <div
              key={`${f.name}:${f.size}:${i}`}
              className="flex items-center gap-2 rounded-md border border-line px-2 py-1.5"
            >
              <span className="zp-num w-6 shrink-0 text-right text-[11px] text-ink-faint">
                {i + 1}
              </span>
              {isPdf(f) ? (
                <FileText className="h-4 w-4 shrink-0 text-ink-faint" />
              ) : (
                <ImageIcon className="h-4 w-4 shrink-0 text-ink-faint" />
              )}
              <span className="min-w-0 flex-1 truncate text-ink">{f.name}</span>
              <span className="zp-num shrink-0 text-[11px] text-ink-faint">
                {formatBytes(f.size)}
              </span>
              {images.length > 1 && isImage(f) ? (
                <>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-6 w-6"
                    disabled={i === 0}
                    onClick={() => move(i, i - 1)}
                    aria-label="Move earlier"
                    title="Move earlier"
                  >
                    ↑
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-6 w-6"
                    disabled={i === files.length - 1}
                    onClick={() => move(i, i + 1)}
                    aria-label="Move later"
                    title="Move later"
                  >
                    ↓
                  </Button>
                </>
              ) : null}
              <Button
                size="icon"
                variant="ghost"
                className="h-6 w-6"
                onClick={() => setFiles((c) => c.filter((_, j) => j !== i))}
                aria-label={`Remove ${f.name}`}
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}

          {refusal ? (
            <p className="mt-1 rounded-md border border-warn/30 bg-warn-soft px-2.5 py-1.5 text-warn-ink">
              {refusal}
            </p>
          ) : null}

          <div className="mt-1 flex items-center justify-end gap-2">
            <Button variant="ghost" onClick={() => setFiles([])} disabled={uploading}>
              Clear
            </Button>
            <Button
              variant="primary"
              loading={uploading}
              disabled={Boolean(refusal) || uploading}
              onClick={() => onUpload(files)}
            >
              {uploading
                ? 'Uploading…'
                : // A PDF's page count is not its file count, and claiming
                  // "read 1 page" of a six-page scan is a promise it breaks.
                  pdfs.length > 0
                  ? 'Read this PDF'
                  : `Read ${files.length} page${files.length === 1 ? '' : 's'}`}
            </Button>
          </div>
        </div>
      ) : null}
    </Card>
  )
}
