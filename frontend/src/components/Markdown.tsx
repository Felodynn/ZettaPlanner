import { memo, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Check, Copy } from 'lucide-react'
import { cn } from '@/lib/cn'

/**
 * Markdown, for the prose the product is made of.
 *
 * The planner writes in it — bold, tables, fenced code, ordered lists — and
 * rendering that raw puts `**M1 — Auth hardening**` and a pipe-delimited table
 * on screen as literal characters. The answer is there but it has to be
 * decoded, which is the opposite of what a thread is for.
 *
 * **No raw HTML.** `react-markdown` ignores it unless `rehype-raw` is added,
 * and it is not: the text here comes from a model and from other people, and
 * neither is a source you hand a script tag. That single omission is what
 * makes this safe without a sanitiser.
 *
 * GFM is on for tables, strikethrough and task lists — the parts of the
 * dialect a model actually reaches for.
 *
 * Every element is styled explicitly rather than inherited. This renders
 * inside a dense transcript, and the browser's defaults for `h1` and `table`
 * are sized for a document.
 */

const components: Components = {
  p: ({ children }) => <p className="mb-2 leading-relaxed last:mb-0">{children}</p>,

  // Headings step down in weight rather than in size. A model reaching for
  // `#` inside a chat message means "this is a section", not "this is the
  // title of the page", and rendering it at document scale makes one answer
  // shout over the rest of the thread.
  h1: ({ children }) => <h3 className="mt-3 mb-1.5 font-semibold text-ink first:mt-0">{children}</h3>,
  h2: ({ children }) => <h4 className="mt-3 mb-1.5 font-semibold text-ink first:mt-0">{children}</h4>,
  h3: ({ children }) => <h5 className="mt-2.5 mb-1 font-semibold text-ink first:mt-0">{children}</h5>,
  h4: ({ children }) => (
    <h6 className="mt-2 mb-1 font-semibold text-ink-muted first:mt-0">{children}</h6>
  ),
  h5: ({ children }) => (
    <h6 className="mt-2 mb-1 font-semibold text-ink-muted first:mt-0">{children}</h6>
  ),
  h6: ({ children }) => (
    <h6 className="mt-2 mb-1 font-semibold text-ink-muted first:mt-0">{children}</h6>
  ),

  ul: ({ children }) => <ul className="mb-2 ml-5 list-disc last:mb-0">{children}</ul>,
  ol: ({ children }) => <ol className="mb-2 ml-5 list-decimal last:mb-0">{children}</ol>,
  // A GFM task item carries its own checkbox, so it drops the bullet — a
  // disc *and* a box is two markers for one item.
  li: ({ children, className }) => (
    <li
      className={cn(
        'mb-0.5 leading-relaxed',
        className?.includes('task-list-item') && '-ml-5 flex items-start gap-1.5 list-none',
      )}
    >
      {children}
    </li>
  ),

  // Rendered by remark-gfm for task lists. Left disabled: a checkbox in a
  // transcript is a record of what somebody wrote, not a control — the real
  // one lives on the work item, where ticking it is a write with an owner.
  input: ({ checked, type }) =>
    type === 'checkbox' ? (
      <input
        type="checkbox"
        checked={Boolean(checked)}
        readOnly
        disabled
        className="mt-1 h-3 w-3 shrink-0 accent-[var(--zp-accent)]"
      />
    ) : null,

  strong: ({ children }) => <strong className="font-semibold text-ink">{children}</strong>,
  em: ({ children }) => <em className="italic">{children}</em>,
  del: ({ children }) => <del className="text-ink-faint line-through">{children}</del>,

  a: ({ href, children }) => (
    <a
      href={href}
      // A link in a message may have been written by a model, so it opens
      // detached from this page and carries no referrer.
      target="_blank"
      rel="noreferrer noopener"
      className="text-accent-ink underline underline-offset-2 hover:brightness-110"
    >
      {children}
    </a>
  ),

  blockquote: ({ children }) => (
    <blockquote className="mb-2 border-l-2 border-line-strong pl-3 text-ink-muted last:mb-0">
      {children}
    </blockquote>
  ),

  hr: () => <hr className="my-3 border-line" />,

  // Tables scroll inside their own box. A model asked for a comparison
  // produces six columns without being asked twice, and letting that widen the
  // transcript pushes every other message sideways.
  table: ({ children }) => (
    // `w-fit max-w-full`, not `w-full`: a three-column comparison stretched to
    // the width of the thread puts a hand's width of empty space between the
    // label and its number, which is the reading problem a table exists to
    // remove. It grows to its content and scrolls only once it runs out.
    <div className="mb-2 w-fit max-w-full overflow-x-auto rounded-md border border-line last:mb-0 zp-scroll">
      <table className="w-auto border-collapse">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="bg-sunken">{children}</thead>,
  tr: ({ children }) => <tr className="border-b border-line last:border-b-0">{children}</tr>,
  th: ({ children }) => (
    <th className="px-2.5 py-1.5 text-left font-semibold whitespace-nowrap text-ink">{children}</th>
  ),
  td: ({ children }) => <td className="px-2.5 py-1.5 align-top text-ink">{children}</td>,

  code: ({ className, children, ...rest }) => {
    // react-markdown gives inline code and fenced code the same element; the
    // fenced one carries a `language-*` class and is wrapped in a <pre>.
    const fenced = /language-(\w+)/.exec(className ?? '')
    if (!fenced) {
      return (
        <code
          className="rounded bg-sunken px-1 py-0.5 font-mono text-[0.9em] text-ink"
          {...rest}
        >
          {children}
        </code>
      )
    }
    return (
      <code className={cn('font-mono', className)} {...rest}>
        {children}
      </code>
    )
  },

  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
}

/**
 * A fenced block, with the one affordance people always want from one.
 *
 * Copy reads the rendered text rather than the source: what somebody wants is
 * the code they can see, not the fence around it.
 */
function CodeBlock({ children }: { children: ReactNode }) {
  const [copied, setCopied] = useState(false)

  const copy = (e: React.MouseEvent<HTMLButtonElement>) => {
    const block = e.currentTarget.parentElement?.querySelector('pre')
    const text = block?.textContent ?? ''
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="group relative mb-2 last:mb-0">
      <pre className="max-h-96 overflow-auto rounded-md border border-line bg-sunken px-3 py-2 font-mono text-[12px] leading-relaxed text-ink zp-scroll">
        {children}
      </pre>
      <button
        type="button"
        onClick={copy}
        className="absolute top-1.5 right-1.5 rounded border border-line bg-surface px-1.5 py-1 text-ink-faint opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100 hover:text-ink"
        aria-label="Copy this block"
      >
        {copied ? <Check className="h-3 w-3 text-good" /> : <Copy className="h-3 w-3" />}
      </button>
    </div>
  )
}

/**
 * Memoised on the text.
 *
 * A streaming answer re-renders on every delta, and re-parsing a long message
 * per keystroke-worth of tokens is the difference between smooth output and a
 * thread that stutters as it arrives.
 */
export const Markdown = memo(function Markdown({
  children,
  className,
}: {
  children: string
  className?: string
}) {
  return (
    <div className={cn('min-w-0 wrap-break-word', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  )
})
