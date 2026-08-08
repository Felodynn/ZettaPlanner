import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import {
  applyTheme,
  readAccent,
  readMode,
  resolveMode,
  writeAccent,
  writeMode,
  type AccentName,
  type ResolvedMode,
  type ThemeMode,
} from './theme'

interface ThemeContextValue {
  mode: ThemeMode
  /** What `mode` actually resolves to right now — `system` is not a look. */
  resolved: ResolvedMode
  accent: AccentName
  setMode: (mode: ThemeMode) => void
  setAccent: (accent: AccentName) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

/** Turns the crossfade on for one frame, so only deliberate switches animate. */
function animateOnce(): void {
  const root = document.documentElement
  root.setAttribute('data-theme-animating', '')
  window.setTimeout(() => root.removeAttribute('data-theme-animating'), 200)
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(readMode)
  const [accent, setAccentState] = useState<AccentName>(readAccent)
  const [resolved, setResolved] = useState<ResolvedMode>(() => resolveMode(readMode()))

  // Apply on mount and on every change. The pre-paint script in index.html has
  // already done this once; repeating it is idempotent and keeps React the
  // authority from here on.
  useEffect(() => {
    setResolved(applyTheme(mode, accent))
  }, [mode, accent])

  // Follow the OS while the choice is `system`, and stop following the moment
  // it is not. A user who picks Light explicitly means it at 2am too.
  useEffect(() => {
    if (mode !== 'system') return
    if (typeof matchMedia !== 'function') return
    const mq = matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => {
      animateOnce()
      setResolved(applyTheme('system', accent))
    }
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [mode, accent])

  // Another tab changed the choice. Same person, same device — follow it.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === 'zp.appearance.mode') setModeState(readMode())
      if (e.key === 'zp.appearance.accent') setAccentState(readAccent())
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [])

  const setMode = useCallback((next: ThemeMode) => {
    animateOnce()
    writeMode(next)
    setModeState(next)
  }, [])

  const setAccent = useCallback((next: AccentName) => {
    animateOnce()
    writeAccent(next)
    setAccentState(next)
  }, [])

  const value = useMemo<ThemeContextValue>(
    () => ({ mode, resolved, accent, setMode, setAccent }),
    [mode, resolved, accent, setMode, setAccent],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used inside a ThemeProvider')
  return ctx
}
