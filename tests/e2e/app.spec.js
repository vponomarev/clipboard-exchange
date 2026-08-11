const { test, expect } = require("@playwright/test");

async function selectFiles(page, files, expected = Array.isArray(files) ? files.length : 1) {
  const rows = page.locator(".upload-row");
  await expect.poll(async () => {
    const count = await rows.count();
    if (count === 0) await page.locator("#file-input").setInputFiles(files);
    return rows.count();
  }).toBe(expected);
}

async function openComposerSettings(page) {
  const details = page.locator(".composer-options");
  await expect(details).toBeVisible();
  if (await details.getAttribute("open") === null) await details.locator("summary").click();
  await expect(details).toHaveAttribute("open", "");
}

test("HTTP-compatible UUID fallback initializes the page and honors room query", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(Crypto.prototype, "randomUUID", { value: undefined, configurable: true });
  });
  const room = `fallback-${crypto.randomUUID()}`;
  await page.goto(`/?room=${room}`);
  await expect(page.locator("#room-id")).toHaveValue(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
});

test("mobile layout version dialog shows shell and server versions and checks for updates", async ({ page }) => {
  await page.goto("/");
  await page.locator("#app-info").click();
  await expect(page.locator("#version-dialog")).toBeVisible();
  await expect(page.locator("#shell-version")).toHaveText("dev");
  await expect(page.locator("#server-version")).toHaveText("dev");
  await expect(page.locator("#update-status")).toContainText("актуальная версия");
  await page.locator("#check-update").click();
  await expect(page.locator("#check-update")).toBeEnabled();
  await expect(page.locator("#update-status")).toContainText(/актуальная версия|Новая версия загружена/);
});

test("protected room exposes separate read-only and read-write links", async ({ page, browser }) => {
  const room = `protected-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.locator("#write-protected").check();
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#.*write=cw1_`));

  const readerContext = await browser.newContext();
  const reader = await readerContext.newPage();
  await reader.goto(`/r/${room}`);
  await expect(reader.locator("#item-form")).toBeHidden();
  const denied = await reader.evaluate(async roomID => (await fetch(`/api/rooms/${roomID}/items`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: crypto.randomUUID(), kind: "text", content: "denied" })
  })).status, room);
  expect(denied).toBe(403);

  await page.getByRole("button", { name: "Поделиться" }).click();
  await expect(page.locator("#share-url")).not.toHaveValue(/write=cw1_/);
  await page.getByText("R/W", { exact: true }).click();
  await expect(page.locator("#share-url")).toHaveValue(/write=cw1_/);
  await readerContext.close();
});

test("mobile layout protected short link opens with PIN once without exposing room URL", async ({ page, request }) => {
  const room = `short-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.locator("#write-protected").check();
  await page.locator("#encrypted").check();
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#`));

  await page.getByRole("button", { name: "Поделиться" }).click();
  const fullURL = await page.locator("#share-url").inputValue();
  expect(fullURL).toContain("#key=ce1_");
  await page.locator(".short-create summary").click();
  await page.locator("#short-pin").fill("4827");
  await page.locator("#create-short-link").click();
  await expect(page.locator("#short-result")).toBeVisible();
  const shortURL = await page.locator("#short-url").inputValue();
  expect(shortURL).toMatch(/\/s\/[23456789ABCDEFGHJKMNPQRSTVWXYZ]{5}$/);
  await expect(page.locator("#short-result-pin")).toHaveText("4827");

  const code = shortURL.split("/").pop();
  const envelope = await (await request.get(`/api/short-links/${code}`)).text();
  expect(envelope).not.toContain(room);
  expect(envelope).not.toContain("4827");
  expect(envelope).not.toContain(new URL(fullURL).hash);

  await page.goto(shortURL);
  await expect(page.locator("#short-open-code")).toHaveValue(code);
  await page.locator("#short-open-pin").fill("0000");
  await page.getByRole("button", { name: "Открыть комнату" }).click();
  await expect(page.locator("#short-open-error")).toContainText("PIN неверен");
  await page.locator("#short-open-pin").fill("4827");
  await page.getByRole("button", { name: "Открыть комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}#.*key=ce1_`));

  await page.goto(shortURL);
  await page.locator("#short-open-pin").fill("4827");
  await page.getByRole("button", { name: "Открыть комнату" }).click();
  await expect(page.locator("#short-open-error")).toContainText("Ссылка недоступна");
});

test("plain room preserves multiline text and updates another client", async ({ page, browser }) => {
  const room = `plain-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));

  const secondContext = await browser.newContext();
  const second = await secondContext.newPage();
  await second.goto(`/r/${room}`);
  const exact = "  printf '%s\\n' \"$PATH\"\n\tline two\n";
  await openComposerSettings(page);
  await page.locator("#alias").fill("Вася");
  await page.locator("#item-text").fill(exact);
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await expect(second.locator(".item-content")).toHaveText(exact);
  await expect(second.locator(".item-alias")).toHaveText("Вася");
  await expect(second.locator("#item-form")).toBeVisible();
  await expect(second.locator(".item .delete")).toHaveCount(1);
  await expect(second.getByText("В сети")).toBeVisible();
  await second.locator(".item .delete").click();
  await expect(page.locator(".item")).toHaveCount(0);
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

test("text and multiple files are sent as one entry only after Add", async ({ page, request }) => {
  const room = `file-${crypto.randomUUID()}`;
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await openComposerSettings(page);
  await page.locator("#alias").fill("Вася");
  await page.locator("#item-text").fill("Файлы к задаче");
  await expect(page.locator("#file-input")).toBeEnabled();
  const content = Buffer.from("first line\nsecond line\n", "utf8");
  const secondContent = Buffer.from("another attachment", "utf8");
  await selectFiles(page, [
    { name: "script $HOME.txt", mimeType: "text/plain", buffer: content },
    { name: "notes.txt", mimeType: "text/plain", buffer: secondContent }
  ]);
  await expect(page.locator(".upload-row .muted")).toHaveText(["Готов к отправке", "Готов к отправке"]);
  const beforeAdd = await (await request.get(`/api/rooms/${room}`)).json();
  expect(beforeAdd.items).toHaveLength(0);
  expect(beforeAdd.files).toHaveLength(0);
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".item")).toHaveCount(1);
  await expect(page.locator(".item-content")).toHaveText("Файлы к задаче");
  await expect(page.locator(".file-attachment .file-name")).toHaveText(["script $HOME.txt", "notes.txt"]);
  await expect(page.locator(".item .item-alias")).toHaveText("Вася");

  const data = await (await request.get(`/api/rooms/${room}`)).json();
  expect(data.items).toHaveLength(1);
  expect(data.files).toHaveLength(2);
  expect(new Set(data.files.map(file => file.entryId))).toEqual(new Set([data.items[0].id]));
  const firstFile = data.files.find(file => file.name === "script $HOME.txt");
  const downloaded = await request.get(`/api/rooms/${room}/files/${firstFile.id}`);
  expect(await downloaded.body()).toEqual(content);

  await page.locator(".file-attachment").filter({ hasText:"script $HOME.txt" }).getByRole("button", { name:"Открыть" }).click();
  await expect(page.locator("#preview-dialog")).toBeVisible();
  await expect(page.locator("#preview-title")).toHaveText("script $HOME.txt");
  await expect(page.locator("#preview-content")).toContainText("first line");
  await page.locator("#preview-dialog").getByRole("button", { name:"Закрыть" }).click();

  const participant = await page.context().newPage();
  await participant.goto(`/r/${room}`);
  await expect(participant.locator(".file-attachment")).toHaveCount(2);
  await expect(participant.locator(".item .delete")).toHaveCount(1);
  await participant.locator(".item .delete").click();
  await expect(page.locator(".item")).toHaveCount(0);
  await participant.close();
});

test("interrupted upload resumes after reload and verifies completed chunks", async ({ page, request }) => {
  const room = `resume-${crypto.randomUUID()}`;
  const content = Buffer.alloc((1 << 20) + 19, 0x5a);
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "serviceWorker", { configurable:true, get:() => undefined }));
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name:"Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
  let interrupted = false;
  await page.route("**/chunks/1", async route => {
    if (!interrupted) { interrupted = true; await route.abort("connectionrefused"); }
    else await route.continue();
  });
  const selected = { name:"resume.bin", mimeType:"application/octet-stream", buffer:content };
  await expect(page.locator("#file-input")).toBeEnabled();
  await selectFiles(page, selected);
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".upload-row .muted")).toContainText("Ошибка");
  await page.reload();
  await expect(page.locator("#file-input")).toBeEnabled();
  await selectFiles(page, selected);
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".file-attachment .file-name")).toHaveText("resume.bin");
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
  })).toMatchObject({ key: expect.stringMatching(/^ce1_/), write: null });

  const secret = "ssh root@internal\nexport TOKEN=do-not-leak";
  const secretAlias = "Секретный Вася";
  await openComposerSettings(page);
  await page.locator("#alias").fill(secretAlias);
  await page.locator("#item-text").fill(secret);
  const fileSecret = Buffer.from("private file bytes\n", "utf8");
  await selectFiles(page, { name:"private.txt", mimeType:"text/plain", buffer:fileSecret });
  await expect(page.locator(".upload-row .muted")).toHaveText("Готов к отправке");
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await expect(page.locator(".item-content")).toHaveText(secret);
  await expect(page.locator(".file-attachment .file-name")).toHaveText("private.txt");
  await expect(page.locator(".item .item-alias")).toHaveText(secretAlias);

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
    navigator.serviceWorker.controller.postMessage({ type:"prepare-download", token, config:{ rawKey:params.get("key").slice(4), roomID:room, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size, mimeType:"text/plain", disposition:"inline", url:`/api/rooms/${room}/files/${file.id}` } }, [channel.port2]);
    await ready;
    try { const response = await fetch(`/client-download/${token}`); return { disposition:response.headers.get("Content-Disposition"), bytes:Array.from(new Uint8Array(await response.arrayBuffer())) }; }
    catch (error) { return { error:String(error) }; }
  }, { room, file:data.files[0], size:fileSecret.length });
  expect(streamed).toEqual({ disposition:expect.stringMatching(/^inline/), bytes:Array.from(fileSecret) });

  const downloadPromise = page.waitForEvent("download");
  await page.locator(".file-attachment").getByRole("button", { name:"Скачать" }).click();
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
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "serviceWorker", { configurable:true, get:() => undefined }));
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
  await expect(page.locator("#file-input")).toBeEnabled();
  await selectFiles(page, selected);
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".upload-row .muted")).toContainText("Ошибка");
  await page.reload();
  await expect(page.locator("#file-input")).toBeEnabled();
  await selectFiles(page, selected);
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".file-attachment .file-name")).toHaveText("encrypted-resume.bin");
});

test("encrypted room can be unlocked with separately shared passphrase", async ({ page, browser }) => {
  const room = `password-${crypto.randomUUID()}`;
  const password = "correct horse battery staple";
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.locator("#encrypted").check();
  await page.locator("#room-key").fill(password);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await page.locator("#item-text").fill("exact secret");
  await page.getByRole("button", { name: "Добавить", exact: true }).click();
  await expect(page.locator(".item-content")).toHaveText("exact secret");

  const readerContext = await browser.newContext();
  const reader = await readerContext.newPage();
  await reader.goto(`/r/${room}`);
  await expect(reader.locator("#key-dialog")).toBeVisible();
  await reader.locator("#unlock-key").fill(password);
  await reader.getByRole("button", { name: "Открыть" }).click();
  await expect(reader.locator(".item-content")).toHaveText("exact secret");
  await readerContext.close();
});

test("clipboard, search, pin, favorite and clear stay client-safe", async ({ page, request }) => {
  const room = `productivity-${crypto.randomUUID()}`;
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "clipboard", { configurable:true, get:() => ({
    read: async () => [{ types:["text/plain"], getType: async () => new Blob(["from clipboard"], { type:"text/plain" }) }]
  }) }));
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name:"Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
  await page.locator("#read-clipboard").click();
  await expect(page.locator("#item-text")).toHaveValue("from clipboard");
  await openComposerSettings(page);
  await page.locator("#entry-ttl").selectOption("300");
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".item")).toHaveCount(1);
  await page.locator("#item-text").fill("second searchable value");
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".item")).toHaveCount(2);
  await page.locator("#search").fill("searchable");
  await expect(page.locator(".item")).toHaveCount(1);
  await page.locator("#search").fill("");
  await page.locator(".item").filter({ hasText:"from clipboard" }).getByRole("button", { name:"Закрепить" }).click();
  await expect.poll(async () => (await (await request.get(`/api/rooms/${room}`)).json()).entries.some(entry => entry.pinned)).toBe(true);
  await page.locator("#favorite-room").click();
  await expect(page.locator("#favorite-room")).toContainText("В избранном");
  await page.reload();
  await expect(page.locator("#favorite-room")).toContainText("В избранном");
  const data = await (await request.get(`/api/rooms/${room}`)).json();
  expect(data.entries.some(entry => entry.pinned)).toBe(true);
  expect(data.entries.some(entry => entry.expiresAt)).toBe(true);
  page.once("dialog", dialog => dialog.accept());
  await page.locator("#clear-room").click();
  await expect(page.locator(".item")).toHaveCount(0);
});

test("composer stays compact while advanced settings remain accessible", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "android-chrome", "desktop-specific bounds");
  const room = `compact-${crypto.randomUUID()}`;
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "serviceWorker", { configurable:true, get:() => undefined }));
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name:"Создать комнату" }).click();
  await expect(page.locator(".room-heading")).toBeVisible();
  await expect(page.locator("#item-form")).toBeVisible();
  const heading = await page.locator(".room-heading").boundingBox();
  const composer = await page.locator("#item-form").boundingBox();
  expect(heading.height).toBeLessThan(60);
  expect(composer.height).toBeLessThan(150);
  await expect(page.locator("#alias")).toBeHidden();
  await page.getByText("Настройки", { exact:true }).click();
  await expect(page.locator("#alias")).toBeVisible();
  await page.locator("#alias").fill("Вася");
  await page.getByText("Настройки", { exact:true }).click();
  await page.locator("#item-text").fill("compact composer");
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".item-alias")).toHaveText("Вася");
});

test("mobile layout QR scanner accepts only rooms from the current installation", async ({ page, request }) => {
  const room = `scan-${crypto.randomUUID()}`;
  const created = await request.post("/api/rooms", { data:{ id:room, encrypted:false, keyId:"", writeProtected:false, writeToken:"", ttlSeconds:0 } });
  expect(created.status()).toBe(201);
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "mediaDevices", { configurable:true, get:() => undefined }));
  await page.goto("/");
  await page.getByRole("button", { name:"QR-код" }).click();
  await expect(page.locator("#scan-dialog")).toBeVisible();
  await page.locator("#scan-link").fill("https://invalid.example/r/not-allowed");
  await page.getByRole("button", { name:"Открыть", exact:true }).click();
  await expect(page.locator("#scan-status")).toContainText("только ссылку комнаты этого Clipboard Exchange");
  await page.locator("#scan-link").fill(`${new URL(page.url()).origin}/r/${room}`);
  await page.getByRole("button", { name:"Открыть", exact:true }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
  await expect(page.locator("#item-form")).toBeVisible();
});

test("camera QR detector opens a scanned room", async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name !== "chrome", "native detector mock is covered once");
  const room = `camera-scan-${crypto.randomUUID()}`;
  const created = await request.post("/api/rooms", { data:{ id:room, encrypted:false, keyId:"", writeProtected:false, writeToken:"", ttlSeconds:0 } });
  expect(created.status()).toBe(201);
  await page.addInitScript(roomID => {
    Object.defineProperty(globalThis, "BarcodeDetector", { configurable:true, value:class {
      static async getSupportedFormats() { return ["qr_code"]; }
      async detect() { return [{ rawValue:`/r/${roomID}` }]; }
    } });
    Object.defineProperty(Navigator.prototype, "mediaDevices", { configurable:true, get:() => ({ getUserMedia:async () => new MediaStream() }) });
    Object.defineProperty(HTMLMediaElement.prototype, "readyState", { configurable:true, get:() => HTMLMediaElement.HAVE_ENOUGH_DATA });
    HTMLMediaElement.prototype.play = async () => {};
  }, room);
  await page.goto("/");
  await page.getByRole("button", { name:"QR-код" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
});

test("mobile layout can create a room and show QR", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "android-chrome", "mobile-specific scenario");
  const room = `mobile-${crypto.randomUUID()}`;
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "serviceWorker", { configurable:true, get:() => undefined }));
  await page.goto("/");
  await page.locator("#room-id").fill(room);
  await page.getByRole("button", { name: "Создать комнату" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${room}$`));
  await expect(page.locator("#item-form")).toBeVisible();
  const heading = await page.locator(".room-heading").boundingBox();
  const composer = await page.locator("#item-form").boundingBox();
  expect(heading.height).toBeLessThan(95);
  expect(composer.height).toBeLessThan(165);
  await expect(page.locator("#alias")).toBeHidden();
  await page.getByText("Настройки", { exact:true }).click();
  await expect(page.locator("#alias")).toBeVisible();
  await page.getByText("Настройки", { exact:true }).click();
  await page.locator("#item-text").fill("mobile smoke");
  await page.getByRole("button", { name:"Добавить", exact:true }).click();
  await expect(page.locator(".item-content")).toHaveText("mobile smoke");
  await page.getByRole("button", { name: "Поделиться" }).click();
  await expect(page.locator("#share-dialog")).toBeVisible();
  await expect(page.locator("#qr canvas:visible, #qr img:visible")).toBeVisible();
});
