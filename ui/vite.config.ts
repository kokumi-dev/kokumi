import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // Proxy /api requests to the local API server during development.
    // Run `make run-server` in a separate terminal.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
