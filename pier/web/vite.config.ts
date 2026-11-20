import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const outDir = resolve(import.meta.dirname, '../internal/web/dist')

// The dashboard is embedded into pierd from internal/web/dist. That folder
// keeps a .gitkeep so pierd builds before the dashboard has been built.
export default defineConfig({
  plugins: [
    react(),
    { name: 'keep-gitkeep', closeBundle: () => writeFileSync(resolve(outDir, '.gitkeep'), '') },
  ],
  build: { outDir, emptyOutDir: true },
  server: {
    // `make pier-dev` runs pierd's API on 7777. PIER_API points at another one.
    proxy: { '/api': process.env.PIER_API ?? 'http://127.0.0.1:7777' },
  },
})
