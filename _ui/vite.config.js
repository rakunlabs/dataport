import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  base: './',
  plugins: [tailwindcss(), svelte()],
  resolve: {
    alias: {
      '@': '/src',
    },
  },
  build: {
    outDir: '../internal/server/dist',
    emptyOutDir: true,
  },
  server: {
    port: 3000,
    // Vite rewrites Host to the target; xfwd passes the original one in
    // X-Forwarded-Host, which the backend compares with Origin to reject
    // cross-site requests.
    proxy: { '/api': { target: 'http://localhost:8080', xfwd: true } },
  },
});
