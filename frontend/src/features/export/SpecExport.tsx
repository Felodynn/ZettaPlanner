import { useState } from 'react'
import { Download, FileText, X } from 'lucide-react'
import { client } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { notifyError, notifySuccess } from '@/components/notify'

type Scope = 'work-item' | 'milestone'

/**
 * Spec export: preview, then download.
 *
 * F7 asks for the preview and it is not decoration. The export is a POST that
 * writes an audit entry, and it assembles a slice of the architecture around
 * whatever it is exporting — so what comes out is not obvious from the button,
 * and handing somebody a file they have not seen is how a wrong spec reaches a
 * contractor.
 *
 * There is deliberately **no CSV** here. The audit CSV is v0.1 (§6.5, D13) and
 * shipping half of it now would set an expectation about its columns that the
 * real design would have to break.
 */
export function SpecExportButton({
  scope,
  id,
  name,
  className,
}: {
  scope: Scope
  id: string
  name: string
  className?: string
}) {
  const [preview, setPreview] = useState<string | null>(null)
  const [filename, setFilename] = useState('spec.md')
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    setPreview(null)
    try {
      const path =
        scope === 'work-item'
          ? '/api/v1/work-items/{id}/spec-export'
          : '/api/v1/milestones/{id}/spec-export'
      // `parseAs: 'text'` because this returns text/markdown, not JSON — the
      // generated client would otherwise try to parse a document as an object
      // and hand back nothing useful.
      const res = await client.POST(path as '/api/v1/work-items/{id}/spec-export', {
        params: { path: { id } },
        parseAs: 'text',
      })
      if (res.error) throw new Error('The export could not be generated.')
      setPreview(String(res.data))
      setFilename(dispositionName(res.response) ?? `${slug(name)}-spec.md`)
    } catch (e) {
      notifyError(e)
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
      <Button size="sm" variant="secondary" loading={loading} onClick={load} className={className}>
        {loading ? null : <FileText className="h-3.5 w-3.5" />}
        Export spec
      </Button>

      {preview !== null ? (
        <PreviewDialog
          title={name}
          filename={filename}
          markdown={preview}
          onClose={() => setPreview(null)}
        />
      ) : null}
    </>
  )
}

function PreviewDialog({
  title,
  filename,
  markdown,
  onClose,
}: {
  title: string
  filename: string
  markdown: string
  onClose: () => void
}) {
  const download = () => {
    // Built from the text already on screen, so the file and the preview are
    // the same bytes by construction rather than by two requests agreeing.
    const blob = new Blob([markdown], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
    notifySuccess(`${filename} saved. The export is recorded in the audit trail.`, 'Downloaded')
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-canvas/70 p-6">
      <div className="zp-shadow-pop flex h-[80vh] w-full max-w-3xl flex-col rounded-card border border-line bg-raised">
        <div className="flex items-start gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1">
            <p className="font-semibold text-ink">Spec export</p>
            <p className="truncate text-ink-muted">
              {title} · <span className="font-mono text-[11px]">{filename}</span>
            </p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close">
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <pre className="min-h-0 flex-1 overflow-auto bg-sunken px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink zp-scroll">
          {markdown}
        </pre>

        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          <p className="min-w-0 flex-1 text-[11px] text-ink-muted">
            This is the document, not a summary of it. Exporting is recorded in the audit trail —
            the fact that a spec left the system is itself worth knowing.
          </p>
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
          <Button variant="primary" onClick={download}>
            <Download className="h-3.5 w-3.5" />
            Download
          </Button>
        </div>
      </div>
    </div>
  )
}

/** The filename the backend chose, when it sent one. */
function dispositionName(res: Response | undefined): string | undefined {
  const header = res?.headers.get('Content-Disposition')
  if (!header) return undefined
  const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(header)
  return m?.[1] ? decodeURIComponent(m[1]) : undefined
}

function slug(name: string): string {
  return (
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'spec'
  )
}
