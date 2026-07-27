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
     * They are separate builds rather than two lazy chunks of one app, and the reason is
     * that a lazy chunk is a bandwidth optimisation, not a boundary. With one entry, the
     * whole panel — every page, every mutation, every internal API action — is part of
     * the same module graph the share page's HTML pulls from, and keeping operator code
     * out of a stranger's browser depends on nobody ever writing the wrong import.
     *
     * With two entries it is structural. access.html reaches src/access.tsx and whatever
     * that transitively imports, and nothing else can arrive by accident: an import of
     * UserDrawer from the access island would show up as a size jump in a bundle that
     * should never contain one.
     *
     * What this does *not* do, and should not be mistaken for: it does not make the panel
     * bundle unreachable. Static assets on a public origin can be fetched by anyone who
     * knows the filename, and index.html names them. The point is what a subscriber's
     * browser is *served*, not what a determined reader can go and get.
     *
     * Genuinely shared, dependency-free leaf modules — the UI kit, the formatters, the
     * QR renderer — do end up in both, as a shared chunk or duplicated. That is correct:
     * they are shared by design, and none of them knows anything about operating a node.
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
