import { create } from 'zustand'

/**
 * Assistant text arriving as it is generated.
 *
 * Kept apart from the message cache on purpose. FRONTEND F1: streaming appends
 * `message.delta`; `message.complete` replaces with the canonical server
 * message, and **deltas are never the record**. Two stores makes that
 * structural rather than a rule somebody has to remember — the transcript is
 * whatever the server returned, and this is a temporary overlay that is
 * discarded the moment the real message lands.
 */

interface StreamStore {
  /** Accumulated text per running turn. */
  text: Record<string, string>
  /** Turns whose assistant message has completed but whose refetch is in flight. */
  settling: Record<string, true>

  append: (turnId: string, chunk: string) => void
  /** The turn produced a complete message; hold the text until the refetch lands. */
  settle: (turnId: string) => void
  /** The canonical messages have arrived. Drop the overlay. */
  clear: (turnId: string) => void
  clearAll: () => void
}

export const useStream = create<StreamStore>((set) => ({
  text: {},
  settling: {},

  append: (turnId, chunk) =>
    set((s) => ({ text: { ...s.text, [turnId]: (s.text[turnId] ?? '') + chunk } })),

  settle: (turnId) => set((s) => ({ settling: { ...s.settling, [turnId]: true } })),

  clear: (turnId) =>
    set((s) => {
      const { [turnId]: _text, ...text } = s.text
      const { [turnId]: _settling, ...settling } = s.settling
      return { text, settling }
    }),

  clearAll: () => set({ text: {}, settling: {} }),
}))
