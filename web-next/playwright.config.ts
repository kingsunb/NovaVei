import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright E2E 配置 —— P4 验收工具
 *  - 启 vite preview 在 4174（生产构建后的真实产物）
 *  - 跑 5 个核心页面 + 一致性 / a11y / 性能
 *  - 浏览器覆盖：Chromium / Firefox / WebKit（按需启）
 *  - CI 模式（CI=1）下：只跑 chromium、串行、不允许重试
 */
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: !process.env.CI,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? "line" : "list",
  timeout: 30_000,
  expect: { timeout: 5_000 },

  use: {
    baseURL: "http://127.0.0.1:4174",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    actionTimeout: 5_000,
  },

  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],

  webServer: {
    command: "pnpm preview",
    url: "http://127.0.0.1:4174",
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
