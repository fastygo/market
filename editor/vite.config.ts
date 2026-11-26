import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [solid()],
  build: {
    outDir: resolve(__dirname, "../internal/web/static/editor"),
    emptyOutDir: true,
    rollupOptions: {
      input: resolve(__dirname, "src/main.tsx"),
      output: {
        entryFileNames: "editory-[hash].js",
        assetFileNames: "editory-[hash][extname]",
        format: "es",
      },
    },
  },
});
