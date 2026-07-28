/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // .gitkeep lives in dist and is what keeps `//go:embed all:dist` compiling on a
    // checkout that has never run this build. Emptying the directory would delete it.
    emptyOutDir: false,

    /**
     * Two entries, two islands: the operator's panel and the subscriber's share page.
     *
     * Separate builds rather than lazy chunks, because a chunk is a bandwidth
     * optimisation, not a boundary — with one entry, keeping operator code out of a
     * stranger's browser depends on nobody writing the wrong import. It does not make the
     * panel bundle unreachable; it decides what a subscriber is served.
     */
    rollupOptions: {
      input: {
        index: "index.html",
        access: "access.html",
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // In development the SPA is served by Vite but the API is the Go binary on :80,
      // so both look same-origin to the browser — which matters, because the session
      // cookie is SameSite=Lax and every node call goes through /api/proxy.
      "/api": { target: "http://127.0.0.1:80", changeOrigin: false },
      "/healthz": { target: "http://127.0.0.1:80", changeOrigin: false },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
