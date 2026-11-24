import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';

function compressedWebAssets() {
  return {
    name: 'compressed-web-assets',
    async writeBundle({ dir }, bundle) {
      // Vite still replaces dynamic-import preload markers in generateBundle.
      for (const name of Object.keys(bundle)) {
        if (!/^assets\/.+-[\w-]{8}\.(js|css)$/.test(name)) continue;
        const file = join(dir, name);
        const contents = await readFile(file);
        const compressed = gzipSync(contents);
        if (compressed.length < contents.length) {
          await writeFile(file + '.gz', compressed);
        }
      }
    },
  };
}

// Build to ../web so the Go binary embeds it via //go:embed web.
export default defineConfig({
  plugins: [svelte(), compressedWebAssets()],
  base: './',
  build: {
    outDir: '../web',
    emptyOutDir: true,
  },
});
