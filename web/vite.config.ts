/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import fs from "node:fs";
import path from "node:path";

const embedDir = path.resolve(__dirname, "../internal/web/dist");

const gitkeep = [
  'Committed so that "go build ./..." works before the SPA has ever been built.',
  "//go:embed fails outright on a missing directory.",
  "",
].join("\n");

/**
 * Go embeds ../internal/web/dist, and //go:embed fails outright on a missing
 * directory — so a clone without one cannot compile the server at all, before a
 * line of Go is read. The directory is therefore kept in git by a .gitkeep, and
 * `emptyOutDir` deletes it on every build.
 *
 * Restoring it here rather than in the Makefile means it happens on every path
 * that builds the SPA: `task web`, a bare `npm run build`, and the Docker
 * stage. Leaving it to the caller to remember has broken the Go build twice.
 */
function keepEmbedDirectory(): Plugin {
  return {
    name: "alder-keep-embed-directory",
    closeBundle() {
      fs.mkdirSync(embedDir, { recursive: true });
      fs.writeFileSync(path.join(embedDir, ".gitkeep"), gitkeep);
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), keepEmbedDirectory()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    // Written straight into the Go module's embed directory: no copy step, and
    // no chance of shipping a binary with a stale SPA in it. The Docker build
    // mirrors this path when it copies the output between stages.
    outDir: embedDir,
    emptyOutDir: true,
    sourcemap: false,
    // Measured on feat/replication, 2026-09-27, with a sourcemap build
    // attributing every emitted byte back to its source module:
    //
    //   entry JS   832 kB raw   241 kB gzip   ~202 kB brotli
    //   CSS         45 kB raw     8 kB gzip
    //   61% of the JS is vendor; react-dom alone is 182 kB, TanStack ~119 kB,
    //   radix ~80 kB, lucide 33 kB already tree-shaken. No highlighter, no
    //   diff library, no date library — the LDIF, schema and config renderers
    //   are all hand-written app code.
    //
    // The limit is raised rather than the bundle split, and the reasoning is
    // in docs/DECISIONS.md under this date. The short version: Fiber
    // compresses the response, so first load is ~200 kB once, from localhost
    // or an internal host; and a complete route-level split moves at most
    // 260 kB of app code, leaving an entry chunk of ~546 kB that would trip
    // this warning anyway. A split that ends in "and then raise the limit"
    // is not an answer to the warning.
    //
    // 1000 is a ceiling with about 170 kB of headroom, not a silencer. When
    // it fires, measure again rather than raising it again.
    chunkSizeWarningLimit: 1000,
  },
  test: {
    // Node, not jsdom: what is worth testing here is the logic that decides
    // what to send to a directory — which modification a diff produces, how a
    // value is escaped into a DN, whether a value looks like a switch. None of
    // it touches the DOM, and a DOM environment would only add a dependency and
    // a second way for these to fail.
    // Components are tested by rendering them to markup, which needs no DOM
    // either.
    environment: "node",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
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
