import darkModeLogo from '@/assets/felodynn-dark.png'
import lightModeLogo from '@/assets/felodynn-light.png'
import { useTheme } from '@/theme/ThemeProvider'
import { cn } from '@/lib/cn'

/**
 * The Felodynn wordmark, in whichever version the current theme can show.
 *
 * Two files rather than one, because the mark is not a single colour that CSS
 * could invert: the text flips between white and black while the full stop
 * stays brand blue in both. `filter: invert()` would take the blue with it.
 *
 * Switched in React rather than in CSS. The header does not exist until React
 * mounts, so there is nothing to flash — and `resolved` is computed in the
 * theme store's initialiser, so the very first render already has the right
 * one. Following `resolved` rather than `mode` is what makes `system` work:
 * `system` is not a look, and the OS changing at dusk has to move this too.
 *
 * The sources under `docs/assets/` are the originals at full size; these are
 * derivatives generated for the web. Regenerate with the snippet in
 * `scripts/make-wordmark.py` if the brand files change.
 */
export function Wordmark({ className }: { className?: string }) {
  const { resolved } = useTheme()

  return (
    <img
      src={resolved === 'dark' ? darkModeLogo : lightModeLogo}
      // The alt text carries the name; the surrounding link carries the
      // destination. A logo whose alt says "logo" tells a screen reader
      // nothing anybody wanted to know.
      alt="Felodynn"
      // Intrinsic size, so the header reserves the space before the image
      // decodes and the project switcher does not jump sideways on load.
      width={393}
      height={88}
      draggable={false}
      className={cn('h-5 w-auto select-none', className)}
    />
  )
}
