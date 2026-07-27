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
