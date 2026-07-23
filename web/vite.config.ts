import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The Go server (production) serves the contents of dist/. In development the
// Go server on :8080 reverse-proxies unknown routes here (:5173), and this dev
// server proxies /api back to Go so a single origin works for the browser.
const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // noVNC 1.7 ships top-level await; require a target that supports it.
    target: "es2022",
  },
  esbuild: {
    target: "es2022",
  },
  optimizeDeps: {
    // noVNC uses top-level await; the dev dependency optimizer needs a target
    // that supports it too (not just the production build).
    esbuildOptions: { target: "es2022" },
  },
});
