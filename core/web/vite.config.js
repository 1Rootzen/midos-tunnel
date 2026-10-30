import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

// The build lands in ../panelweb, which the Go binary embeds. The panel is
// served under a random secret path, so every URL is relative (base './').
export default defineConfig({
  base: './',
  plugins: [vue()],
  build: {
    outDir: '../panelweb',
    emptyOutDir: true,
    // the panel's Content-Security-Policy allows no data: fonts or scripts,
    // so nothing is inlined as a data URI
    assetsInlineLimit: 0,
    target: 'es2020',
    cssCodeSplit: true,
    modulePreload: { polyfill: false },
    reportCompressedSize: false,
    rollupOptions: {
      output: {
        // vue + router change rarely: their own long-cached chunk
        manualChunks: { vue: ['vue', 'vue-router'] },
      },
    },
  },
});
