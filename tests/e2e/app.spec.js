const { test, expect } = require("@playwright/test");

test("HTTP-compatible UUID fallback initializes the page and honors room query", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(Crypto.prototype, "randomUUID", { value: undefined, configurable: true });
  });
  const room = `fallback-${crypto.randomUUID()}`;
  await page.goto(`/?room=${room}`);
  await expect(page.locator("#room-id")).toHaveValue(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#.*write=cw1_`));
});

test("plain room preserves multiline text and updates another client", async ({ page, browser }) => {
  const room = `plain-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#.*write=cw1_`));

  const secondContext = await browser.newContext();
  const second = await secondContext.newPage();
  await second.goto(`/r/${room}`);
  const exact = "  printf '%s\\n' \"$PATH\"\n\tline two\n";
  await page.locator("#alias").fill("Вася");
  await page.locator("#item-text").fill(exact);
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await expect(second.locator(".item-content")).toHaveText(exact);
  await expect(second.locator(".item-alias")).toHaveText("Вася");
  await expect(second.locator("#item-form")).toBeHidden();
  await expect(second.locator(".delete")).toHaveCount(0);
  await expect(second.getByText("В сети")).toBeVisible();

  const denied = await second.evaluate(async roomID => (await fetch(`/api/rooms/${roomID}/items`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: crypto.randomUUID(), kind: "text", content: "denied" })
  })).status, room);
  expect(denied).toBe(403);

  await page.locator(".delete").click();
  await expect(second.locator(".item")).toHaveCount(0);
  await secondContext.close();
});

test("long text is collapsed to two visual lines and can be expanded", async ({ page }) => {
  const room = `collapse-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();

  const exact = Array.from({ length: 8 }, (_, index) => `line ${index + 1}`).join("\n");
  await page.locator("#item-text").fill(exact);
  await page.getByRole("button", { name: "Добавить", exact: true }).click();

  const content = page.locator(".item-content");
  const expand = page.getByRole("button", { name: "Развернуть" });
  await expect(expand).toBeVisible();
  await expect(content).toHaveText(exact);
  await expect(content).toHaveClass(/collapsed/);

  await expand.click();
  await expect(page.getByRole("button", { name: "Свернуть" })).toBeVisible();
  await expect(content).not.toHaveClass(/collapsed/);

  await page.getByRole("button", { name: "Свернуть" }).click();
  await expect(page.getByRole("button", { name: "Развернуть" })).toBeVisible();
  await expect(content).toHaveClass(/collapsed/);
});

test("open file uploads in chunks and is available to a read-only client", async ({ page, request }) => {
  const room = `file-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await page.locator("#alias").fill("Вася");
  const content = Buffer.from("first line\nsecond line\n", "utf8");
  await page.locator("#file-input").setInputFiles({ name: "script $HOME.txt", mimeType: "text/plain", buffer: content });
  await expect(page.locator(".file-item .file-name")).toHaveText("script $HOME.txt");
  await expect(page.locator(".file-item .item-alias")).toHaveText("Вася");

  const data = await (await request.get(`/api/rooms/${room}`)).json();
  expect(data.files).toHaveLength(1);
  const downloaded = await request.get(`/api/rooms/${room}/files/${data.files[0].id}`);
  expect(await downloaded.body()).toEqual(content);

  const readOnly = await page.context().newPage();
  await readOnly.goto(`/r/${room}`);
  await expect(readOnly.locator(".file-item .file-name")).toHaveText("script $HOME.txt");
  await expect(readOnly.locator(".file-item .delete")).toHaveCount(0);
  await readOnly.close();
  await page.locator(".file-item .delete").click();
  await expect(page.locator(".file-item")).toHaveCount(0);
});

test("interrupted upload resumes after reload and verifies completed chunks", async ({ page, request }) => {
  const room = `resume-${crypto.randomUUID()}`;
  const content = Buffer.alloc((1 << 20) + 19, 0x5a);
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name:"Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#`));
  let interrupted = false;
  await page.route("**/chunks/1", async route => {
    if (!interrupted) { interrupted = true; await route.abort("connectionrefused"); }
    else await route.continue();
  });
  const selected = { name:"resume.bin", mimeType:"application/octet-stream", buffer:content };
  await page.locator("#file-input").setInputFiles(selected);
  await expect(page.locator(".upload-row .muted")).toContainText("Ошибка");
  await page.reload();
  await page.locator("#file-input").setInputFiles(selected);
  await expect(page.locator(".file-item .file-name")).toHaveText("resume.bin");
  const data = await (await request.get(`/api/rooms/${room}`)).json();
  const downloaded = await request.get(`/api/rooms/${room}/files/${data.files[0].id}`);
  expect(await downloaded.body()).toEqual(content);
});

test("encrypted room keeps plaintext out of the server response", async ({ page, request }) => {
  const room = `secure-${crypto.randomUUID()}`;
  await page.goto("/");

	await expect.poll(() => page.evaluate(() => Boolean(globalThis.crypto?.subtle))).toBe(true);
  await page.locator("#room-id").fill(room);
  await page.locator("#encrypted").check();
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#`));
  expect(await page.evaluate(() => {
    const params = new URLSearchParams(location.hash.slice(1));
    return { key: params.get("key"), write: params.get("write") };
  })).toMatchObject({ key: expect.stringMatching(/^ce1_/), write: expect.stringMatching(/^cw1_/) });

  const secret = "ssh root@internal\nexport TOKEN=do-not-leak";
  const secretAlias = "Секретный Вася";
  await page.locator("#alias").fill(secretAlias);
  await page.locator("#item-text").fill(secret);
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await expect(page.locator(".item-content")).toHaveText(secret);

  const fileSecret = Buffer.from("private file bytes\n", "utf8");
  await page.locator("#file-input").setInputFiles({ name:"private.txt", mimeType:"text/plain", buffer:fileSecret });
  await expect(page.locator(".file-item .file-name")).toHaveText("private.txt");
  await expect(page.locator(".file-item .item-alias")).toHaveText(secretAlias);

  const response = await request.get(`/api/rooms/${room}`);
  const raw = await response.text();
  expect(raw).not.toContain(secret);
  expect(raw).not.toContain(secretAlias);
  expect(raw).not.toContain("private.txt");
  expect(raw).not.toContain("private file bytes");
  const data = JSON.parse(raw);
  expect(data.items[0].kind).toBe("encrypted");
  expect(data.items[0].content).toBeUndefined();
  expect(data.files[0].encrypted).toBe(true);
  expect(data.files[0].name).toBeUndefined();

  const streamed = await page.evaluate(async ({ room, file, size }) => {
    const params = new URLSearchParams(location.hash.slice(1));
    const token = crypto.randomUUID();
    const channel = new MessageChannel();
    const ready = new Promise(resolve => { channel.port1.onmessage = resolve; });
    navigator.serviceWorker.controller.postMessage({ type:"prepare-download", token, config:{ rawKey:params.get("key").slice(4), roomID:room, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size, mimeType:"text/plain", url:`/api/rooms/${room}/files/${file.id}` } }, [channel.port2]);
    await ready;
    try { return { bytes:Array.from(new Uint8Array(await (await fetch(`/client-download/${token}`)).arrayBuffer())) }; }
    catch (error) { return { error:String(error) }; }
  }, { room, file:data.files[0], size:fileSecret.length });
  expect(streamed).toEqual({ bytes:Array.from(fileSecret) });

  const downloadPromise = page.waitForEvent("download");
  await page.locator(".file-item").getByRole("button", { name:"Скачать" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("private.txt");
  const stream = await download.createReadStream();
  const chunks = [];
  for await (const chunk of stream) chunks.push(chunk);
  expect(Buffer.concat(chunks)).toEqual(fileSecret);

  await page.getByRole("button", { name: "Поделиться" }).click();
  await expect(page.locator("#qr canvas:visible, #qr img:visible")).toBeVisible();
  await expect(page.locator("#share-url")).toHaveValue(/key=ce1_/);
  await page.getByText("Без ключа").click();
  await expect(page.locator("#share-url")).not.toHaveValue(/#key=/);
});

test("encrypted upload resumes without retransmitting a verified chunk", async ({ page }) => {
  const room = `encrypted-resume-${crypto.randomUUID()}`;
  const content = Buffer.alloc((1 << 20) + 23, 0x31);
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.locator("#encrypted").check();
  await page.getByRole("button", { name:"Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#`));
  let interrupted = false;
  await page.route("**/chunks/1", async route => {
    if (!interrupted) { interrupted = true; await route.abort("connectionrefused"); }
    else await route.continue();
  });
  const selected = { name:"encrypted-resume.bin", mimeType:"application/octet-stream", buffer:content };
  await page.locator("#file-input").setInputFiles(selected);
  await expect(page.locator(".upload-row .muted")).toContainText("Ошибка");
  await page.reload();
  await page.locator("#file-input").setInputFiles(selected);
  await expect(page.locator(".file-item .file-name")).toHaveText("encrypted-resume.bin");
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
  await page.getByRole("button", { name: "Добавить", exact: true }).click();

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
