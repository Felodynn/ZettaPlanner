import { useEffect } from 'react'

/**
 * Keyboard shortcuts, with the one rule that makes them safe.
 *
 * **A bare key never fires while somebody is typing.** The item list binds `j`
 * and `k`; the same screen has a title field and a worklog box, and a shortcut
 * that steals `j` from a text input is a shortcut that corrupts what people
 * write. So a handler with no modifier is skipped whenever the focus is in an
 * input, a textarea, a select, or anything `contenteditable`.
 *
 * Modifier combinations are allowed through, because `mod+s` means save
 * everywhere and nobody types it into a field on purpose.
 */

export interface Hotkey {
  /** Lower-case `key`, e.g. `j`, `enter`, `?`, `escape`. */
  key: string
  mod?: boolean
  shift?: boolean
  run: (event: KeyboardEvent) => void
  /** Shown in the help sheet. Omit to keep a binding out of it. */
  describe?: string
}

function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

export function useHotkeys(hotkeys: Hotkey[], enabled = true): void {
  useEffect(() => {
    if (!enabled) return

    const onKey = (e: KeyboardEvent) => {
      const key = e.key.toLowerCase()
      const mod = e.metaKey || e.ctrlKey

      for (const h of hotkeys) {
        if (h.key !== key) continue
        if (Boolean(h.mod) !== mod) continue
        if (h.shift !== undefined && h.shift !== e.shiftKey) continue
        // The rule above. `escape` is the exception people expect: it means
        // "get me out of here" *especially* while typing.
        if (!h.mod && key !== 'escape' && isTyping(e.target)) continue
        e.preventDefault()
        h.run(e)
        return
      }
    }

    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [hotkeys, enabled])
}

/** How a binding reads in the help sheet. */
export function chord(h: Hotkey): string {
  const parts: string[] = []
  if (h.mod) parts.push(navigator.platform.startsWith('Mac') ? '⌘' : 'Ctrl')
  if (h.shift) parts.push('Shift')
  parts.push(h.key === ' ' ? 'Space' : h.key.length === 1 ? h.key.toUpperCase() : h.key)
  return parts.join(' + ')
}
