import path from 'node:path'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const apiTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/healthz': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/readyz': {
        target: apiTarget,
        changeOrigin: true,
      },
      '/ws': {
        target: apiTarget,
        ws: true,
        changeOrigin: true,
      },
      '/graphql': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (['react', 'react-dom', 'react-router', 'react-router-dom'].some(
            (name) => id.includes(`/node_modules/${name}/`),
          )) {
            return 'vendor'
          }
          if (['@tanstack/react-query', 'zustand', 'dayjs'].some(
            (name) => id.includes(`/node_modules/${name}/`),
          )) {
            return 'query'
          }
        },
      },
    },
  },
})
