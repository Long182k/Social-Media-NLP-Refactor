import { defineConfig, devices } from '@playwright/test'

const externalBaseURL = process.env.PLAYWRIGHT_BASE_URL
const baseURL = externalBaseURL ?? 'http://127.0.0.1:15173'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL,
    headless: true,
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: externalBaseURL
    ? undefined
    : [
        {
          command:
            'sh -c "mkdir -p ../backend/tmp && (cd ../backend && go build -o tmp/api-e2e ./cmd/api) && exec ../backend/tmp/api-e2e"',
          url: 'http://127.0.0.1:18080/readyz',
          timeout: 90_000,
          reuseExistingServer: false,
          env: {
            PORT: '18080',
            APP_ENV: 'test',
            APP_ORIGIN: 'http://127.0.0.1:15173',
            PUBLIC_BASE_URL: 'http://127.0.0.1:15173',
            DATABASE_URL:
              'postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable',
            VALKEY_URL: 'redis://127.0.0.1:6379/0',
            JWT_SECRET: 'e2e-test-access-secret-value-12345',
            REFRESH_JWT_SECRET: 'e2e-test-refresh-secret-value-67890',
            COOKIE_SECURE: 'false',
            SMTP_HOST: '127.0.0.1',
            SMTP_PORT: '1025',
            S3_ENDPOINT: 'http://127.0.0.1:9000',
            S3_BUCKET: 'social-media-local',
            S3_ACCESS_KEY: 'minioadmin',
            S3_SECRET_KEY: 'minioadmin',
          },
        },
        {
          command: 'pnpm exec vite --host 127.0.0.1 --port 15173 --strictPort',
          url: 'http://127.0.0.1:15173',
          timeout: 60_000,
          reuseExistingServer: false,
          env: {
            VITE_API_PROXY_TARGET: 'http://127.0.0.1:18080',
          },
        },
      ],
})
