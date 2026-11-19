import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    // The Go server embeds this folder, so the site ships as one binary.
    outDir: '../server/dist',
    emptyOutDir: true,
  },
  server: {
    // `go run .` in server/ listens on :8080.
    proxy: { '/api': 'http://localhost:8080' },
  },
})
