import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The app is served with a strict CSP (default-src 'self'), so nothing may be
// inlined as a data: URI.
export default defineConfig({
  plugins: [svelte()],
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
    assetsInlineLimit: 0,
  },
});
