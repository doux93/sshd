import {
  test,
  expect,
  loginAPI,
  createHost,
  connectHost,
  watchSockets,
  assertFits,
} from "./fixtures";

// These scenarios run after the original onboarding checks, which exercise
// the initially empty workspace. Every scenario creates its own target.
test("terminal search and mouse assistant preserve input and fit laptop windows", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop terminal tools");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "UI audit terminal");
  const session = await connectHost(context.request, app, host.id);
  await page.addInitScript(
    (id) =>
      localStorage.setItem(
        "sshx.views.v2",
        JSON.stringify({ ids: [id], active: id }),
      ),
    session.id,
  );
  await watchSockets(page);
  await page.goto(app.url);
  await page.locator(".xterm-screen").click();
  await page.keyboard.type("UI_AUDIT_MARKER");
  await page.keyboard.press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__sshxTest.received))
    .toContain("UI_AUDIT_MARKER");
  const inputBefore = await page.evaluate(
    () => (window as any).__sshxTest.sent,
  );
  await page.getByRole("button", { name: "搜索终端", exact: true }).click();
  const search = page.getByRole("region", { name: "搜索终端输出" });
  await search.getByLabel("搜索内容").fill("UI_AUDIT_MARKER");
  await expect(search.getByRole("status")).toHaveText(/\d+\/\d+/);
  await search.getByLabel("搜索内容").press("Enter");
  await search.getByLabel("搜索内容").fill("NO_MATCH_7474728");
  await expect(search.getByRole("status")).toHaveText("无匹配");
  await search.getByLabel("搜索内容").press("Escape");
  await expect(page.locator(".xterm-helper-textarea")).toBeFocused();
  await page.keyboard.press("Control+Shift+f");
  await expect(search).toBeVisible();
  await search.getByLabel("搜索内容").press("Escape");
  await page.getByRole("button", { name: "命令助手", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "AI 命令助手" })).toBeVisible();
  await page.getByLabel("命令需求").press("Escape");
  expect(await page.evaluate(() => (window as any).__sshxTest.sent)).toBe(
    inputBefore,
  );
  const attachments = () =>
    page.evaluate(
      () =>
        (window as any).__sshxTest.controls.filter((s: string) =>
          /"type":"(?:attach|detach)"/.test(s),
        ).length,
    );
  const before = await attachments();
  await page.getByRole("button", { name: "文件", exact: true }).click();
  await expect(page.getByLabel("远端目录")).toHaveValue(/\/remote$/);
  for (const width of [1366, 1024]) {
    await page.setViewportSize({ width, height: 768 });
    await assertFits(page);
    for (const selector of [
      ".terminal-toolbar",
      ".file-toolbar",
      ".session-bar",
    ]) {
      expect(
        await page
          .locator(selector)
          .evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
      ).toBe(true);
    }
    await page.screenshot({
      path: info.outputPath(`desktop-dark-${width}.png`),
      animations: "disabled",
      fullPage: true,
    });
  }
  await page.getByRole("button", { name: "切换浅色主题" }).click();
  await page.screenshot({
    path: info.outputPath("desktop-light-1024.png"),
    animations: "disabled",
    fullPage: true,
  });
  await page.getByRole("button", { name: "会话库", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "会话库", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "上传", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "会话库", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("complementary", { name: "上传队列" }),
  ).toBeVisible();
  expect(await attachments()).toBe(before);
});

test("modals keep focus, restore the trigger and report AI configuration failures", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop modal workflow");
  await loginAPI(context.request, app);
  await page.goto(app.url);
  await page.setViewportSize({ width: 1024, height: 640 });
  const trigger = page
    .getByRole("complementary")
    .getByRole("button", { name: "新建主机", exact: true });
  await trigger.click();
  const form = page.getByRole("dialog", { name: "新建主机", exact: true });
  await expect(form).toBeVisible();
  await form.getByLabel("名称（选填）").fill("draft stays in modal");
  await form.getByRole("button", { name: "保存主机", exact: true }).focus();
  await page.keyboard.press("Tab");
  expect(await form.evaluate((el) => el.contains(document.activeElement))).toBe(
    true,
  );
  await page.keyboard.press("Escape");
  await expect(form).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await page.getByRole("button", { name: "快速连接", exact: true }).click();
  const quick = page.getByRole("dialog", { name: "快速连接", exact: true });
  await quick.getByLabel("密码", { exact: true }).fill("discarded-secret");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "快速连接", exact: true }).click();
  await expect(quick.getByLabel("密码", { exact: true })).toHaveValue("");
  await page.keyboard.press("Escape");
  await page.route(
    "**/api/ai/config",
    (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "Settings temporarily unavailable" }),
      }),
    { times: 1 },
  );
  await page.getByRole("button", { name: "AI 设置", exact: true }).click();
  const settings = page.getByRole("dialog", { name: "AI 设置", exact: true });
  await expect(settings.getByRole("alert")).toContainText(
    "Settings temporarily unavailable",
  );
  await expect(
    settings.getByRole("button", { name: "保存", exact: true }),
  ).toBeDisabled();
  await settings.getByRole("button", { name: "重新加载", exact: true }).click();
  await expect(settings.getByLabel("模型 ID")).toBeEnabled();
  await settings.getByLabel("模型 ID").fill("test-model");
  await settings.getByLabel("服务地址（选填）").fill(app.modelURL);
  await settings.getByLabel(/^API Key/).fill("test-key");
  await settings.getByRole("button", { name: "保存", exact: true }).click();
  await expect(settings.getByRole("status")).toContainText("已保存");
  await expect(settings.getByLabel(/^API Key/)).toHaveValue("");
  await settings.getByRole("button", { name: "保存", exact: true }).focus();
  await page.keyboard.press("Tab");
  expect(
    await settings.evaluate((el) => el.contains(document.activeElement)),
  ).toBe(true);
  await assertFits(page);
  expect(
    await settings.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
  ).toBe(true);
  await page.screenshot({
    path: info.outputPath("ai-settings-compact.png"),
    animations: "disabled",
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("button", { name: "AI 设置", exact: true }),
  ).toBeFocused();
});

test("file panel filters and keyboard navigation recover from a missing directory", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop file navigation");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "UI audit files");
  const session = await connectHost(context.request, app, host.id);
  await page.addInitScript(
    (id) =>
      localStorage.setItem(
        "sshx.views.v2",
        JSON.stringify({ ids: [id], active: id }),
      ),
    session.id,
  );
  await page.goto(app.url);
  await page.getByRole("button", { name: "文件", exact: true }).click();
  const panel = page.getByRole("region", { name: "远端文件", exact: true });
  const path = panel.getByLabel("远端目录");
  await expect(path).toHaveValue(/\/remote$/);
  const home = await path.inputValue();
  const folder = "中文 UI audit";
  const made = await context.request.post(
    app.url + "/api/files/mkdir?session=" + session.id,
    { data: { path: home + "/" + folder } },
  );
  expect(made.ok()).toBe(true);
  await panel.getByRole("button", { name: "刷新目录", exact: true }).click();
  await panel.getByLabel("筛选文件").fill("中文 UI");
  const entry = panel.getByRole("button", { name: folder, exact: true });
  await expect(entry).toBeVisible();
  await entry.focus();
  await page.keyboard.press("Enter");
  await expect(path).toHaveValue(home + "/" + folder);
  await expect(panel.getByLabel("筛选文件")).toHaveValue("");
  await path.fill(home + "/NO_SUCH_DIRECTORY_74837");
  await path.press("Enter");
  await expect(panel.getByRole("alert")).toContainText("无法打开");
  await expect(path).toHaveValue(home + "/" + folder);
  await panel.getByRole("button", { name: "上级目录", exact: true }).click();
  await expect(path).toHaveValue(home);
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await panel.getByLabel("筛选文件").fill("UNMATCHED_7382");
  await expect(
    panel.getByText("没有匹配的文件", { exact: true }),
  ).toBeVisible();
  await panel.getByLabel("筛选文件").fill("");
  await page
    .locator("input[type=file]")
    .setInputFiles({
      name: "中文 操作检查.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("UI download contents"),
    });
  const queue = page.getByRole("complementary", { name: "上传队列" });
  await expect(queue.getByText("已完成", { exact: true })).toBeVisible();
  await queue.getByRole("button", { name: "收起上传队列" }).click();
  const downloadReady = page.waitForEvent("download");
  await panel
    .getByRole("link", { name: "中文 操作检查.txt", exact: true })
    .click();
  const download = await downloadReady;
  expect(download.suggestedFilename()).toBe("中文 操作检查.txt");
  const stream = await download.createReadStream();
  const chunks = [];
  for await (const chunk of stream!) chunks.push(chunk);
  expect(Buffer.concat(chunks).toString()).toBe("UI download contents");
  await page.setViewportSize({ width: 1024, height: 768 });
  await page.getByRole("button", { name: "切换浅色主题" }).click();
  await assertFits(page);
  await page.screenshot({
    path: info.outputPath("files-light.png"),
    animations: "disabled",
    fullPage: true,
  });
  await panel
    .getByRole("button", { name: "收起文件面板", exact: true })
    .click();
  await expect(panel).toHaveCount(0);
});
