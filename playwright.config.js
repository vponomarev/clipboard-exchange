const { defineConfig, devices } = require("@playwright/test");
const firefoxExecutable = process.env.PLAYWRIGHT_FIREFOX_EXECUTABLE;

module.exports = defineConfig({
  testDir: "./tests/e2e",
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: false,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  use: { baseURL: "http://127.0.0.1:18080", trace: "retain-on-failure" },
  webServer: {
    command: process.platform === "win32"
      ? ".\\clipboard-exchange-e2e.exe --listen=127.0.0.1:18080 --database=e2e.db --room-ttl=0 --rate-limit=0"
      : "./clipboard-exchange-e2e.exe --listen=127.0.0.1:18080 --database=e2e.db --room-ttl=0 --rate-limit=0",
    url: "http://127.0.0.1:18080/readyz",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000
  },
  projects: [
    { name: "chrome", use: { ...devices["Desktop Chrome"], channel: "chrome" } },
    { name: "firefox", use: { ...devices["Desktop Firefox"], ...(firefoxExecutable ? { launchOptions: { executablePath: firefoxExecutable } } : {}) } },
    { name: "android-chrome", use: { ...devices["Pixel 7"] } }
  ]
});
