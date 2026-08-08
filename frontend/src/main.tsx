import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import { Providers } from './app/providers'
import { ErrorBoundary } from './components/ErrorBoundary'
import './styles.css'

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from index.html')

// A development-only handle on the API client and the error surface.
//
// It exists so the error surface can be exercised against a *real* denial from
// the running backend rather than a fabricated one — a toast test that invents
// its own envelope proves only that the test can build an envelope. Stripped
// from a production build by the constant folding on import.meta.env.DEV.
if (import.meta.env.DEV) {
  void Promise.all([import('./api/client'), import('./components/notify')]).then(
    ([api, notify]) => {
      Object.assign(window, { __zp: { ...api, ...notify } })
    },
  )
}

createRoot(root).render(
  <StrictMode>
    <ErrorBoundary what="ZettaPlanner">
      <Providers>
        <App />
      </Providers>
    </ErrorBoundary>
  </StrictMode>,
)
