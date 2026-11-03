import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: 'http://127.0.0.1:3173',
    viewport: { width: 1440, height: 1050 },
    launchOptions: { args: ['--use-angle=swiftshader', '--enable-unsafe-swiftshader'] },
  },
  webServer: { command: 'npx vite --host 127.0.0.1 --port 3173 --strictPort', url: 'http://127.0.0.1:3173', reuseExistingServer: false },
})
