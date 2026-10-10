import { defineConfig } from "@playwright/test";
import { servers } from "./support/servers";

/**
 * End-to-end journeys: the real UI, served by the real binary, against the
 * real directories in test/compose.
 *
 * Assumes the harness is up (`task compose:up`) and the SPA is built into
 * the binary's embedded filesystem (`task web`); `task test:e2e` does the
 * second. Playwright starts Alder itself, so a journey never depends on a
 * server someone left running.
 *
 * One project per directory server, the same pair the conformance suite
 * holds to identical behaviour. Journeys run one at a time: they share two
 * directories, and a pass that depended on scheduling would not be one.
 */
const port = Number(process.env.ALDER_E2E_PORT ?? 18443);

export default defineConfig({
  testDir: "./journeys",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: servers.map((server) => ({
    name: server.name,
    use: { browserName: "chromium" as const, server },
  })),
  webServer: {
    // HTTP is fine on the loopback: the browser treats 127.0.0.1 as a secure
    // context, so the session cookie keeps its Secure attribute. What matters
    // here -- LDAPS to the directory, with the certificate verified -- is
    // untouched by it.
    command: `go run ./cmd/alder serve --addr 127.0.0.1:${port} --allow-http`,
    cwd: "../..",
    url: `http://127.0.0.1:${port}/api/v1/source`,
    reuseExistingServer: false,
    // The first `go run` on a cold cache compiles the whole binary.
    timeout: 180_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
