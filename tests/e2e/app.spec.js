const { test, expect } = require("@playwright/test");

test("plain room preserves multiline text and updates another client", async ({ page, browser }) => {
  const room = `plain-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));

  const second = await browser.newPage();
  await second.goto(`/r/${room}`);
  const exact = "  printf '%s\\n' \"$PATH\"\n\tline two\n";
  await page.locator("#item-text").fill(exact);
  await page.getByRole("button", { name: "Добавить" }).click();
  await expect(second.locator(".item-content")).toHaveText(exact);
  await expect(second.getByText("В сети")).toBeVisible();

  await second.locator(".item").getByRole("button", { name: "Удалить" }).click();
  await expect(page.locator(".item")).toHaveCount(0);
  await second.close();
});

test("encrypted room keeps plaintext out of the server response", async ({ page, request }) => {
  const room = `secure-${crypto.randomUUID()}`;
  await page.goto("/");

	await expect.poll(() => page.evaluate(() => Boolean(globalThis.crypto?.subtle))).toBe(true);
  await page.locator("#room-id").fill(room);
  await page.locator("#encrypted").check();
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(/#key=ce1_/);

  const secret = "ssh root@internal\nexport TOKEN=do-not-leak";
  await page.locator("#item-text").fill(secret);
  await page.getByRole("button", { name: "Добавить" }).click();
  await expect(page.locator(".item-content")).toHaveText(secret);

  const response = await request.get(`/api/rooms/${room}`);
  const raw = await response.text();
  expect(raw).not.toContain(secret);
  const data = JSON.parse(raw);
  expect(data.items[0].kind).toBe("encrypted");
  expect(data.items[0].content).toBeUndefined();

  await page.getByRole("button", { name: "Поделиться" }).click();
  await expect(page.locator("#qr canvas:visible, #qr img:visible")).toBeVisible();
  await expect(page.locator("#share-url")).toHaveValue(/#key=ce1_/);
  await page.getByText("Без ключа").click();
  await expect(page.locator("#share-url")).not.toHaveValue(/#key=/);
});

test("encrypted room can be unlocked with separately shared passphrase", async ({ page }) => {
  const room = `password-${crypto.randomUUID()}`;
  const password = "correct horse battery staple";
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.locator("#encrypted").check();
  await page.locator("#room-key").fill(password);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await page.locator("#item-text").fill("exact secret");
  await page.getByRole("button", { name: "Добавить" }).click();

  await page.goto(`/r/${room}`);
  await expect(page.locator("#key-dialog")).toBeVisible();
  await page.locator("#unlock-key").fill(password);
  await page.getByRole("button", { name: "Открыть" }).click();
  await expect(page.locator(".item-content")).toHaveText("exact secret");
});

test("mobile layout can create a room and show QR", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "android-chrome", "mobile-specific scenario");
  const room = `mobile-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await page.getByRole("button", { name: "Поделиться" }).click();
  await expect(page.locator("#share-dialog")).toBeVisible();
  await expect(page.locator("#qr canvas:visible, #qr img:visible")).toBeVisible();
});
