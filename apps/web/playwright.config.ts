import { defineConfig, devices } from "@playwright/test";

const KEY = "e2e-api-key-123";
const SECRET = "e2e-shared-secret-0123456789";
const ALICE = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";
const root = "../..";

/**
 * Full-stack E2E: boots chainsim (with forced reorgs) + Go gateway + Rust engine + Rust signer + the built web app,
 * then drives a real browser. Set E2E_BASE_URL to target an already-running stack (e.g. docker compose).
 */
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  use: { baseURL: process.env.E2E_BASE_URL ?? "http://localhost:4173", trace: "on-first-retry" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : [
        {
          command: `${root}/dist/go/chainsim`,
          url: "http://localhost:8545",
          reuseExistingServer: true,
          env: { SIM_BLOCK_MS: "300", SIM_REORG_EVERY: "5" },
          gracefulShutdown: { signal: "SIGTERM", timeout: 2000 },
        },
        {
          command: `${root}/target/release/engine`,
          port: 50051,
          reuseExistingServer: true,
          env: { ENGINE_LISTEN: "tcp://127.0.0.1:50051", ENGINE_ALLOW_INSECURE: "true" },
        },
        {
          command: `${root}/target/release/signer`,
          url: "http://localhost:8081/healthz",
          reuseExistingServer: true,
          env: {
            SIGNER_SHARED_SECRET: SECRET,
            SIGNER_KEYS: `alice=${ALICE}`,
            SIGNER_TX_CHAINS: "1337",
            SIGNER_TX_MAX_VALUE: "1000000000000000",
          },
        },
        {
          command: `${root}/dist/go/gateway`,
          url: "http://localhost:8080/healthz",
          reuseExistingServer: true,
          env: {
            GATEWAY_UPSTREAMS: "http://localhost:8545",
            GATEWAY_API_KEYS: `e2e=${KEY}`,
            SIGNER_SHARED_SECRET: SECRET,
            SIGNER_URL: "http://localhost:8081",
            CORS_ORIGIN: "*",
            POLL_INTERVAL: "200ms",
            ENGINE_ADDR: "h2c://127.0.0.1:50051",
            RELAY_URLS: "http://localhost:8545",
          },
        },
        { command: "npx vite preview --port 4173 --strictPort", url: "http://localhost:4173", reuseExistingServer: true },
      ],
});
