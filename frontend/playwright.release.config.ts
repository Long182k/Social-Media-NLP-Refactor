import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e-release',
  workers: 1,
  timeout: 180_000,
  use: {
    baseURL: process.env.RENDER_DEMO_ORIGIN,
    ...devices['Desktop Chrome'],
  },
})
