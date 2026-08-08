import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// The API base is same-origin in a deployment: the SPA is served by the Go
// binary (D2), so there is no CORS at all. In development the two are separate
// processes, and rather than depend on ZP_DEV_ORIGIN + CORS we proxy — which
// keeps the browser on one origin and means the session cookie behaves in dev
// exactly as it will in production.
const API_TARGET = process.env.ZP_API ?? 'http://localhost:8090'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: API_TARGET,
        changeOrigin: false,
      },
      '/health': { target: API_TARGET, changeOrigin: false },
    },
  },
})
