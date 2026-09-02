import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    // Written straight into the Go module's embed directory: no copy step, and
    // no chance of shipping a binary with a stale SPA in it. The Docker build
    // mirrors this path when it copies the output between stages.
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    // `npm run dev` proxies the API to a locally running `alder serve`, so the
    // browser sees one origin and the session cookie behaves as it will in
    // production.
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8899",
        changeOrigin: false,
      },
    },
  },
});
