import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      src: path.resolve(__dirname, "src"),
    },
  },
  server: {
    port: 3000,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8088",
        changeOrigin: true,
        // The async SQL Lab channel lives on /api/v1/ws and needs an upgrade.
        ws: true,
      },
      "/static": {
        target: "http://127.0.0.1:8088",
        changeOrigin: true,
      },
    },
  },
  preview: {
    port: 4173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8088",
        changeOrigin: true,
        ws: true,
      },
      "/static": {
        target: "http://127.0.0.1:8088",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    chunkSizeWarningLimit: 3000,
  },
});
