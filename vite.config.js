import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the app talks to the Go backend through this proxy, so the
// browser sees a single origin and no CORS is involved. In production set
// VITE_API_BASE_URL to the deployed backend URL instead.
const backend = process.env.BACKEND_ORIGIN || 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: true },
      '/media': { target: backend, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
});
