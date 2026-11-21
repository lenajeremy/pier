import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The Go server embeds this build and serves it at /, so the whole site
// ships as one binary.
export default defineConfig({
  base: '/',
  plugins: [react()],
  build: {
    outDir: '../server/dist-v3',
    emptyOutDir: true,
  },
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
})
