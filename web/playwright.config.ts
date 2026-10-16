import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  // Balance CI shards by test, including tests in the large interaction file.
  // Each shard keeps one worker and its own backend on a separate runner.
  fullyParallel: true,
  workers: 1,
  timeout: 30_000,
  expect: {
    timeout: 5_000,
  },
  use: {
    baseURL: process.env.WEBFILE_E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    hasTouch: true,
    isMobile: true,
    viewport: { width: 1024, height: 800 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium-touch', use: { browserName: 'chromium' } },
    { name: 'webkit-ipad', use: { ...devices['iPad Pro 11'] } },
  ],
})
