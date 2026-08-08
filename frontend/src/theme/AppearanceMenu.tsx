import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Monitor, Moon, Palette, Sun } from 'lucide-react'
import { ACCENTS, type ThemeMode } from './theme'
import { useTheme } from './ThemeProvider'
import { cn } from '@/lib/cn'

const MODES: { value: ThemeMode; label: string; hint: string; Icon: typeof Sun }[] = [
  { value: 'light', label: 'Light', hint: 'For a bright room', Icon: Sun },
  { value: 'dark', label: 'OLED dark', hint: 'True black background', Icon: Moon },
  { value: 'system', label: 'System', hint: 'Follow the operating system', Icon: Monitor },
]

export function AppearanceMenu() {
  const { mode, resolved, accent, setMode, setAccent } = useTheme()
  const ActiveIcon = mode === 'system' ? Monitor : mode === 'dark' ? Moon : Sun

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        className={cn(
          'inline-flex h-8 w-8 items-center justify-center rounded-md',
          'text-ink-muted transition-colors hover:bg-sunken hover:text-ink',
          'data-[state=open]:bg-sunken data-[state=open]:text-ink',
        )}
        aria-label="Appearance"
        title="Appearance"
      >
        <ActiveIcon className="h-4 w-4" />
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className={cn(
            'zp-shadow-pop z-50 w-64 rounded-lg border border-line bg-raised p-1.5',
            'data-[state=open]:animate-in data-[state=open]:fade-in-0',
          )}
        >
          <DropdownMenu.Label className="px-2 py-1.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            Theme
          </DropdownMenu.Label>

          {MODES.map(({ value, label, hint, Icon }) => (
            <DropdownMenu.Item
              key={value}
              onSelect={() => setMode(value)}
              className={cn(
                'flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 outline-none',
                'data-[highlighted]:bg-sunken',
              )}
            >
              <Icon className="h-4 w-4 shrink-0 text-ink-muted" />
              <span className="flex-1">
                <span className="block leading-tight">{label}</span>
                <span className="block text-xs leading-tight text-ink-faint">
                  {/* Naming what `system` currently resolves to, because
                      "System" alone does not tell you what you are looking at. */}
                  {value === 'system' && mode === 'system' ? `${hint} — now ${resolved}` : hint}
                </span>
              </span>
              {mode === value ? <Check className="h-4 w-4 shrink-0 text-accent-ink" /> : null}
            </DropdownMenu.Item>
          ))}

          <DropdownMenu.Separator className="my-1.5 h-px bg-line" />

          <DropdownMenu.Label className="flex items-center gap-1.5 px-2 py-1.5 text-[11px] font-semibold tracking-wide text-ink-faint uppercase">
            <Palette className="h-3 w-3" />
            Accent
          </DropdownMenu.Label>

          <div className="grid grid-cols-4 gap-1 px-1 pb-1">
            {ACCENTS.map((a) => {
              const active = accent === a.name
              return (
                <button
                  key={a.name}
                  type="button"
                  onClick={() => setAccent(a.name)}
                  title={`${a.label} — ${a.note}`}
                  aria-label={a.label}
                  aria-pressed={active}
                  className={cn(
                    'group flex flex-col items-center gap-1 rounded-md px-1 py-1.5 transition-colors',
                    active ? 'bg-sunken' : 'hover:bg-sunken',
                  )}
                >
                  <span
                    className={cn(
                      'flex h-6 w-6 items-center justify-center rounded-full',
                      'ring-offset-2 ring-offset-raised transition-shadow',
                      active ? 'ring-2 ring-ink-faint' : 'group-hover:ring-1 ring-line-strong',
                    )}
                    style={{ background: a.swatch[resolved] }}
                  >
                    {active ? (
                      <Check
                        className="h-3.5 w-3.5"
                        // Contrast against the swatch itself, not the menu —
                        // a white tick on gold is invisible.
                        style={{ color: 'var(--zp-on-accent)' }}
                      />
                    ) : null}
                  </span>
                  <span className="text-[10px] leading-none text-ink-faint">{a.label}</span>
                </button>
              )
            })}
          </div>

          <p className="px-2 pt-1 pb-1 text-[11px] leading-snug text-ink-faint">
            Stored on this device only.
          </p>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
