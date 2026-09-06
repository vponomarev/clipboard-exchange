'use strict';

// Regenerates the packaging PNG from the repository's source SVG.
const path = require('node:path');
const { chromium } = require('../../node_modules/@playwright/test');

void (async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 512, height: 512 } });
  await page.goto(`file:///${path.resolve(__dirname, '../assets/icon.svg').replaceAll('\\', '/')}`);
  await page.screenshot({ path: path.resolve(__dirname, '../assets/icon.png'), omitBackground: true });
  await browser.close();
})();
