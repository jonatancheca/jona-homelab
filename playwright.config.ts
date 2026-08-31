import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  workers: 1,
  fullyParallel: false,
  timeout: 30000,
  use: { baseURL: 'http://127.0.0.1:3123', trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  reporter: [['list']],
  webServer: {
    command: 'node tests/e2e/server.mjs',
    url: 'http://127.0.0.1:3123/api/health',
    reuseExistingServer: false,
    timeout: 60000,
  },
})
