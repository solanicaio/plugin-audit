import { defineConfig } from 'vite';
import type { Plugin } from 'vite';
import react from '@vitejs/plugin-react-swc';
import { fileURLToPath } from 'node:url';

// Resolved at runtime by the host import map, so the plugin shares the host React singleton.
// Everything else (MUI, Emotion) is bundled so the plugin keeps its own pinned copy.
const HOST_PROVIDED = ['react', 'react-dom', 'react/jsx-runtime'];

// A bundled CommonJS dep that require()s a host-provided module compiles to a stub
// that throws on load, and the host only logs plugin load errors to the console.
const failOnHostRequire = (): Plugin => ({
  name: 'fail-on-host-require',
  apply: 'build',
  renderChunk(code, chunk) {
    for (const id of HOST_PROVIDED) {
      if (code.includes(`__require("${id}")`)) {
        this.error(`${chunk.fileName} calls require("${id}"); alias that CommonJS dependency to ESM`);
      }
    }
    return null;
  },
});

const shim = (file: string) => fileURLToPath(new URL(`./src/shims/${file}`, import.meta.url));

export default defineConfig(({ command }) => ({
  plugins: [react(), failOnHostRequire()],
  resolve: {
    // @mui/x-charts depends on CommonJS use-sync-external-store, see src/shims.
    alias: [
      {
        find: /^use-sync-external-store\/shim\/with-selector(\.js)?$/,
        replacement: shim('use-sync-external-store-with-selector.ts'),
      },
      {
        find: /^use-sync-external-store\/shim(\/index\.js)?$/,
        replacement: shim('use-sync-external-store.ts'),
      },
    ],
  },
  // Library mode leaves process.env untouched, but bundled MUI/Emotion read NODE_ENV.
  define:
    command === 'build'
      ? { 'process.env.NODE_ENV': JSON.stringify('production') }
      : undefined,
  build: {
    // The Go backend embeds the bundle.
    outDir: 'backend/dist',
    emptyOutDir: false,
    lib: {
      entry: 'src/main.tsx',
      formats: ['es'],
      fileName: () => 'main.js',
    },
    rollupOptions: {
      external: HOST_PROVIDED,
    },
  },
  server: {
    port: 3001,
    cors: true,
  },
}));
