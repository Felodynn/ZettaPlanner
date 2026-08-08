/**
 * Appearance: chosen on the device, stored on the device.
 *
 * Deliberately not a user setting on the server. A theme is a property of the
 * screen you are sitting at, not of your account — the same person on an OLED
 * laptop at night and a bright monitor at noon wants different answers, and
 * syncing it would make one of those two wrong every time. It also keeps this
 * off the API surface entirely, which is one less thing the contract has to
 * carry.
 */

export type ThemeMode = 'system' | 'light' | 'dark'
export type ResolvedMode = 'light' | 'dark'

export type AccentName =
  | 'gold'
  | 'amber'
  | 'emerald'
  | 'teal'
  | 'azure'
  | 'indigo'
  | 'violet'
  | 'magenta'

export interface Accent {
  name: AccentName
  label: string
  /** What it reads as, for the picker's tooltip. */
  note: string
  /** Swatch colour per mode, so the picker previews honestly. */
  swatch: Record<ResolvedMode, string>
}

/**
 * Eight hues that survive being the only colour on a dense screen.
 *
 * Red is missing on purpose: `danger` is red in both modes, and an accent that
 * matches it would make "save" and "delete this permanently" the same colour.
 */
export const ACCENTS: readonly Accent[] = [
  {
    name: 'gold',
    label: 'Gold',
    note: 'Warm and legible on true black. The default.',
    swatch: { light: 'oklch(79% 0.145 86)', dark: 'oklch(82% 0.15 86)' },
  },
  {
    name: 'amber',
    label: 'Amber',
    note: 'Gold pushed towards orange.',
    swatch: { light: 'oklch(73% 0.155 62)', dark: 'oklch(77% 0.16 62)' },
  },
  {
    name: 'emerald',
    label: 'Emerald',
    note: 'Sits close to the `done` green — check before choosing.',
    swatch: { light: 'oklch(56% 0.14 158)', dark: 'oklch(72% 0.15 158)' },
  },
  {
    name: 'teal',
    label: 'Teal',
    note: 'The quietest of the eight.',
    swatch: { light: 'oklch(57% 0.11 205)', dark: 'oklch(74% 0.11 205)' },
  },
  {
    name: 'azure',
    label: 'Azure',
    note: 'Conventional, and never mistaken for a status.',
    swatch: { light: 'oklch(56% 0.16 248)', dark: 'oklch(70% 0.15 248)' },
  },
  {
    name: 'indigo',
    label: 'Indigo',
    note: 'Deeper blue, higher contrast on white.',
    swatch: { light: 'oklch(53% 0.18 277)', dark: 'oklch(67% 0.17 277)' },
  },
  {
    name: 'violet',
    label: 'Violet',
    note: 'Vivid. Reads well on black, loud on white.',
    swatch: { light: 'oklch(56% 0.2 305)', dark: 'oklch(69% 0.19 305)' },
  },
  {
    name: 'magenta',
    label: 'Magenta',
    note: 'The most saturated option.',
    swatch: { light: 'oklch(57% 0.19 352)', dark: 'oklch(70% 0.19 352)' },
  },
]

export const DEFAULT_MODE: ThemeMode = 'system'
export const DEFAULT_ACCENT: AccentName = 'gold'

const MODE_KEY = 'zp.appearance.mode'
const ACCENT_KEY = 'zp.appearance.accent'

const ACCENT_NAMES = new Set<string>(ACCENTS.map((a) => a.name))

function isMode(v: unknown): v is ThemeMode {
  return v === 'system' || v === 'light' || v === 'dark'
}

export function readMode(): ThemeMode {
  try {
    const v = localStorage.getItem(MODE_KEY)
    return isMode(v) ? v : DEFAULT_MODE
  } catch {
    // Private mode, or storage disabled. A theme is not worth an error.
    return DEFAULT_MODE
  }
}

export function readAccent(): AccentName {
  try {
    const v = localStorage.getItem(ACCENT_KEY)
    return v !== null && ACCENT_NAMES.has(v) ? (v as AccentName) : DEFAULT_ACCENT
  } catch {
    return DEFAULT_ACCENT
  }
}

export function writeMode(mode: ThemeMode): void {
  try {
    localStorage.setItem(MODE_KEY, mode)
  } catch {
    /* nothing to do; the choice lasts for this tab */
  }
}

export function writeAccent(accent: AccentName): void {
  try {
    localStorage.setItem(ACCENT_KEY, accent)
  } catch {
    /* as above */
  }
}

export function systemPrefersDark(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches
}

export function resolveMode(mode: ThemeMode): ResolvedMode {
  if (mode === 'system') return systemPrefersDark() ? 'dark' : 'light'
  return mode
}

/**
 * Writes the two attributes the stylesheet keys off.
 *
 * Also the function the pre-paint script in index.html mirrors — if the two
 * ever disagree, the page flashes the wrong theme before React mounts, so the
 * attribute names are stated in exactly these two places and nowhere else.
 */
export function applyTheme(mode: ThemeMode, accent: AccentName): ResolvedMode {
  const resolved = resolveMode(mode)
  const root = document.documentElement
  root.setAttribute('data-theme', resolved)
  root.setAttribute('data-accent', accent)
  return resolved
}
