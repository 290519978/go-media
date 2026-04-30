import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    rollupOptions: {
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        camera2: fileURLToPath(new URL('./camera2.html', import.meta.url)),
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:15123',
        changeOrigin: true,
      },
      '/ai': {
        target: 'http://127.0.0.1:15123',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://127.0.0.1:15123',
        ws: true,
        changeOrigin: true,
      },
    },
  },
})
