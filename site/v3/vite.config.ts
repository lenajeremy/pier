import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// V3 is served by the Go server at /v3/, next to V2 at /. The server embeds
// this build, so the whole site still ships as one binary.
export default defineConfig({
  base: '/v3/',
  plugins: [react()],
  build: {
    outDir: '../server/dist-v3',
    emptyOutDir: true,
  },
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
})
