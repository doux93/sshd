import {
  test,
  expect,
  loginAPI,
  createHost,
  connectHost,
  seedAlert,
  watchSockets,
  assertFits,
} from "./fixtures";

test("login, PWA navigation and logout revoke the credential", async ({
  page,
  context,
  app,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(app.url);
  await expect(page).toHaveURL(/\/login\?/);
  await page.locator("input[type=password]").fill(app.password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "退出登录", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => navigator.serviceWorker.ready.then(() => true));
  await page.reload();
  await expect(
    page.getByRole("button", { name: "退出登录", exact: true }),
  ).toBeVisible();
  await assertFits(page);
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("workbench.png"),
    fullPage: true,
  });
  const cookie = (await context.cookies()).find(
    (c) => c.name === "sshx_session",
  );
  expect(cookie).toBeTruthy();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  const res = await context.request.get(app.url + "/api/sessions", {
    headers: { "X-Auth-Token": cookie!.value },
  });
  expect(res.status()).toBe(401);
  expect(errors).toEqual([]);
});

test("desktop connection test, terminal persistence, reconnect and AI preview", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop interaction flow");
  await loginAPI(context.request, app);
  await watchSockets(page);
  await page.goto(app.url);
  await page.getByRole("button", { name: "新建主机", exact: true }).click();
  await page.getByLabel("名称（选填）").fill("E2E primary");
  await page.getByLabel("主机地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("端口", { exact: true }).fill(String(app.sshPort));
  await page.getByLabel("登录用户").fill("test");
  await page.getByLabel("密码", { exact: true }).fill("secret");
  await page.getByRole("button", { name: "测试连接", exact: true }).click();
  await page.getByRole("button", { name: "信任并测试", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("认证成功");
  expect(
    (await (await context.request.get(app.url + "/api/sessions")).json())
      .length,
  ).toBe(0);
  await page.getByRole("button", { name: "保存主机", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "新建主机" })).toHaveCount(0);
  const hosts = await (
    await context.request.get(app.url + "/api/nodes")
  ).json();
  const host = hosts.find((n: any) => n.name === "E2E primary");
  expect(host).toBeTruthy();
  await page.getByLabel("收藏 E2E primary", { exact: true }).click();
  await page.getByLabel("搜索主机").fill("E2E primary");
  await page.getByText("E2E primary", { exact: true }).dblclick();
  await expect(
    page.getByRole("tab", { name: "E2E primary", exact: true }),
  ).toBeVisible();
  const sessions = await (
    await context.request.get(app.url + "/api/sessions")
  ).json();
  expect(sessions.length).toBe(1);
  const id = sessions[0].id;
  await page.locator(".xterm-screen").click();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect
    .poll(() => page.evaluate(() => (window as any).__sshxTest.binary))
    .toBeGreaterThan(0);
  await page
    .getByRole("button", {
      name: "关闭视图 E2E primary，保留 SSH 会话",
      exact: true,
    })
    .click();
  await expect
    .poll(async () => {
      const list = await (
        await context.request.get(app.url + "/api/sessions")
      ).json();
      return list.find((s: any) => s.id === id)?.attached;
    })
    .toBe(0);
  await page
    .getByRole("button", { name: /会话库/ })
    .first()
    .click();
  await page.getByRole("button", { name: "打开视图", exact: true }).click();
  await expect(
    page.getByRole("tab", { name: "E2E primary", exact: true }),
  ).toBeVisible();
  await context.setOffline(true);
  await page.evaluate(() => {
    const x = (window as any).__sshxTest;
    x.sockets.at(-1).close();
  });
  await expect(
    page.getByText("正在重新连接工作台…", { exact: true }),
  ).toBeVisible();
  await context.setOffline(false);
  await expect(page.getByText("工作台已连接", { exact: true })).toBeVisible();
  await expect
    .poll(async () => {
      const list = await (
        await context.request.get(app.url + "/api/sessions")
      ).json();
      return list.find((s: any) => s.id === id)?.state;
    })
    .toBe("active");
  await context.request.post(app.url + "/api/ai/config", {
    data: {
      provider: "openai",
      model: "test-model",
      baseURL: app.modelURL,
      apiKey: "test-only",
    },
  });
  await page.locator(".xterm-screen").click();
  await page.keyboard.press("Control+Shift+k");
  await page.getByRole("textbox", { name: "命令需求" }).fill("显示当前目录");
  await page.getByRole("button", { name: "生成", exact: true }).click();
  await expect(
    page.getByText("只读取目录位置，不修改文件。", { exact: true }),
  ).toBeVisible();
  expect(
    await page.getByRole("textbox", { name: "生成的命令" }).inputValue(),
  ).toBe("pwd");
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: app.url,
  });
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await page.getByRole("button", { name: "复制命令", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "已复制", exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("pwd");
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await page.getByRole("button", { name: "关闭 AI 助手" }).click();
  await page.locator(".xterm-screen").evaluate((el) => {
    const canvas = Array.from(el.querySelectorAll("canvas")).find((c) =>
      c.getContext("webgl2"),
    );
    if (!canvas) throw new Error("Expected accelerated desktop renderer");
    const extension = canvas
      .getContext("webgl2")!
      .getExtension("WEBGL_lose_context");
    if (!extension) throw new Error("Missing WebGL context-loss test support");
    extension.loseContext();
  });
  await expect(page.locator(".xterm-rows")).toContainText("/remote");
  const separator = page.getByRole("separator", { name: "主机栏宽度" });
  await separator.focus();
  await page.keyboard.press("ArrowRight");
  const size = await separator.getAttribute("aria-valuenow");
  await page.reload();
  await expect(separator).toHaveAttribute("aria-valuenow", size!);
  await assertFits(page);
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("desktop-terminal.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: /会话库/ })
    .first()
    .click();
  await page.getByRole("button", { name: "结束会话", exact: true }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "结束会话", exact: true }).click();
  await expect
    .poll(async () => {
      const list = await (
        await context.request.get(app.url + "/api/sessions")
      ).json();
      return list.some((s: any) => s.id === id);
    })
    .toBe(false);
});

test("AI shortcut is configurable without reattaching and Ctrl+K reaches the shell by default", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop keyboard preferences");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "Shortcut host");
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
  await page.keyboard.press("Control+k");
  await expect
    .poll(() => page.evaluate(() => (window as any).__sshxTest.sent))
    .toContain("\x0b");
  await expect(page.getByRole("dialog", { name: "AI 命令助手" })).toHaveCount(
    0,
  );
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await page.keyboard.press("Control+Shift+k");
  await expect(page.getByRole("dialog", { name: "AI 命令助手" })).toBeVisible();
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await page.getByRole("button", { name: "关闭 AI 助手" }).click();
  const controls = await page.evaluate(
    () => (window as any).__sshxTest.controls.length,
  );
  await page.getByRole("button", { name: "AI 设置", exact: true }).click();
  const settings = page.getByRole("dialog", { name: "AI 设置", exact: true });
  await expect(settings.getByLabel("命令助手快捷键")).toHaveValue(
    "ctrl-shift-k",
  );
  await settings.getByLabel("命令助手快捷键").selectOption("ctrl-k");
  await page.screenshot({
    path: info.outputPath("ai-shortcut-settings.png"),
    animations: "disabled",
    fullPage: true,
  });
  await settings.getByRole("button", { name: "关闭", exact: true }).click();
  expect(
    await page.evaluate(() => (window as any).__sshxTest.controls.length),
  ).toBe(controls);
  await page.locator(".xterm-screen").click();
  await page.keyboard.press("Control+k");
  await expect(page.getByRole("dialog", { name: "AI 命令助手" })).toBeVisible();
  await page.reload();
  await page.locator(".xterm-screen").click();
  await page.keyboard.press("Control+k");
  await expect(page.getByRole("dialog", { name: "AI 命令助手" })).toBeVisible();
});

test("alert deep link selects the host, acknowledgment survives reload", async ({
  page,
  context,
  app,
}, info) => {
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "E2E alert host");
  await connectHost(context.request, app, host.id);
  seedAlert(app, host.id, host.name);
  await page.goto(app.url + "/?panel=alerts&node=" + host.id);
  await expect(
    page.getByText("E2E CPU warning", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("主机：" + host.name, { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "标记已确认", exact: true }).click();
  await page.reload();
  await expect(page.getByText("已确认", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "查看主机与会话", exact: true })
    .click();
  if (info.project.name === "desktop")
    await expect(
      page.getByRole("tab", { name: host.name, exact: true }),
    ).toBeVisible();
  else
    await expect(
      page.getByText("选择下方会话查看输出。", { exact: true }),
    ).toBeVisible();
  await assertFits(page);
});

test("mobile touch opens a read-only terminal and never sends keyboard input", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "mobile", "Mobile interaction flow");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "E2E mobile host");
  await connectHost(context.request, app, host.id);
  await watchSockets(page);
  await page.goto(app.url);
  await page.getByRole("button").filter({ hasText: "E2E mobile host" }).tap();
  await expect(
    page.getByText("只读 · 不发送输入", { exact: true }),
  ).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => (window as any).__sshxTest.sockets.length))
    .toBeGreaterThan(0);
  await expect
    .poll(() => page.evaluate(() => (window as any).__sshxTest.received))
    .toContain("/remote");
  await expect(page.locator(".xterm-rows")).toContainText("/remote");
  await page.locator(".xterm-screen").tap();
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await page.keyboard.type("do-not-send");
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await assertFits(page);
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("mobile-readonly.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "切换浅色主题" }).last().tap();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator(".xterm-rows")).toContainText("/remote");
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("mobile-light.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "返回", exact: true }).tap();
  await expect(
    page.getByText("只读 · 不发送输入", { exact: true }),
  ).toHaveCount(0);
});

test("themes, split terminals and saved commands preserve explicit input targets", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop workspace flow");
  await loginAPI(context.request, app);
  const hostA = await createHost(context.request, app, "Split A");
  const hostB = await createHost(context.request, app, "Split B");
  const a = await connectHost(context.request, app, hostA.id);
  const b = await connectHost(context.request, app, hostB.id);
  await watchSockets(page);
  await page.addInitScript(
    ({ a, b }) =>
      localStorage.setItem(
        "sshx.views.v2",
        JSON.stringify({ ids: [a, b], active: a }),
      ),
    { a: a.id, b: b.id },
  );
  await page.goto(app.url);
  await page.getByRole("button", { name: "分屏", exact: true }).click();
  const paneA = page.getByRole("region", { name: "终端窗格 Split A" });
  const paneB = page.getByRole("region", { name: "终端窗格 Split B" });
  await expect(paneA).toBeVisible();
  await expect(paneB).toBeVisible();
  const sessions = async () =>
    await (await context.request.get(app.url + "/api/sessions")).json();
  await expect
    .poll(
      async () =>
        (await sessions()).filter(
          (s: any) => [a.id, b.id].includes(s.id) && s.attached === 1,
        ).length,
    )
    .toBe(2);
  const controls = await page.evaluate(
    () => (window as any).__sshxTest.controls.length,
  );
  await page.getByRole("button", { name: "切换浅色主题" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  expect(
    await page.evaluate(() => (window as any).__sshxTest.controls.length),
  ).toBe(controls);
  await paneA.locator(".xterm-screen").click();
  await page.keyboard.type("cd /split-a");
  await page.keyboard.press("Enter");
  await expect
    .poll(async () => (await sessions()).find((s: any) => s.id === a.id)?.cwd)
    .toBe("/split-a");
  expect((await sessions()).find((s: any) => s.id === b.id)?.cwd).toContain(
    "/remote",
  );
  await paneB.locator(".xterm-screen").click();
  await page.keyboard.type("cd /split-b");
  await page.keyboard.press("Enter");
  await expect
    .poll(async () => (await sessions()).find((s: any) => s.id === b.id)?.cwd)
    .toBe("/split-b");
  expect((await sessions()).find((s: any) => s.id === a.id)?.cwd).toBe(
    "/split-a",
  );
  await assertFits(page);
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("light-split.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "上下排列", exact: true }).click();
  await expect(page.locator(".split-terminals")).toHaveClass(/stacked/);
  await page.getByRole("button", { name: "常用命令", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "常用命令" });
  await expect(dialog).toContainText("Split B ·");
  await dialog.getByLabel("命令名称").fill("查看目录");
  await dialog.getByLabel("命令内容").fill("pwd");
  await dialog.getByRole("button", { name: "收藏命令", exact: true }).click();
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: app.url,
  });
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await dialog.getByRole("button", { name: "复制", exact: true }).click();
  await expect(dialog.getByRole("status")).toContainText("已复制");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("pwd");
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("command-library.png"),
    fullPage: true,
  });
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "常用命令", exact: true }).click();
  await expect(dialog.getByText("查看目录", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭常用命令" }).click();
  await page.getByRole("button", { name: "分屏", exact: true }).click();
  await expect(page.locator(".split-terminals")).toHaveClass(/stacked/);
  await page.getByRole("button", { name: "左右排列", exact: true }).click();
  await page.getByRole("button", { name: "切换深色主题" }).click();
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("dark-split.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 900, height: 760 });
  await assertFits(page);
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("compact-workspace.png"),
    fullPage: true,
  });
});

test("first-host errors explain authentication and save-and-connect opens the terminal", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop onboarding flow");
  await loginAPI(context.request, app);
  await page.goto(app.url);
  await page.getByRole("button", { name: "新建主机", exact: true }).click();
  await page.getByLabel("名称（选填）").fill("Onboarding host");
  await page.getByLabel("主机地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("端口", { exact: true }).fill(String(app.sshPort));
  await page.getByLabel("登录用户").fill("test");
  await page.getByLabel("密码", { exact: true }).fill("wrong-password");
  await page.getByRole("button", { name: "测试连接", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "新建主机" })).toContainText(
    "认证失败，请检查登录用户",
  );
  await page.getByLabel("密码", { exact: true }).fill("secret");
  await page.getByRole("button", { name: "保存并连接", exact: true }).click();
  await expect(
    page.getByRole("tab", { name: "Onboarding host", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".xterm-screen")).toBeVisible();
});

test("SFTP upload survives panel closure and cancelled or failed jobs retry", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop SFTP flow");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "File host");
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
  await expect(page.getByLabel("远端目录")).toHaveValue(/\/remote$/);
  const remoteDir = await page.getByLabel("远端目录").inputValue();
  // Delay one actual upload request, then verify cancellation and retry against SFTP.
  let release: () => void = () => {};
  let intercepted = false;
  await page.route(
    "**/api/files/upload?**",
    async (route) => {
      intercepted = true;
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      await route.abort().catch(() => {});
    },
    { times: 1 },
  );
  await page.locator("input[type=file]").setInputFiles({
    name: "acceptance.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("verified SFTP contents\n"),
  });
  const queue = page.getByRole("complementary", { name: "上传队列" });
  await expect(queue).toBeVisible();
  await expect.poll(() => intercepted).toBe(true);
  // Refusing a browser reload keeps the original in-flight upload and queue.
  const leavePrompt = page.waitForEvent("dialog");
  // Trigger a real reload without waiting for a load event: cancelling the
  // beforeunload dialog intentionally leaves the current document in place.
  await page.evaluate(() => {
    setTimeout(() => location.reload(), 0);
  });
  const leaveDialog = await leavePrompt;
  expect(leaveDialog.type()).toBe("beforeunload");
  await leaveDialog.dismiss();
  await expect(queue).toBeVisible();
  // Logout must ask before revoking the credential, not after navigating away.
  const logoutPrompt = page.waitForEvent("dialog");
  const logoutClick = page
    .getByRole("button", { name: "退出登录", exact: true })
    .click();
  const logoutDialog = await logoutPrompt;
  expect(logoutDialog.type()).toBe("confirm");
  await logoutDialog.dismiss();
  await logoutClick;
  expect((await context.request.get(app.url + "/api/sessions")).status()).toBe(
    200,
  );
  await expect(queue.getByRole("button", { name: "取消上传" })).toBeVisible();
  await queue.getByRole("button", { name: "取消上传" }).click();
  await expect(queue.getByText("已取消", { exact: true })).toBeVisible();
  release();
  await queue.getByRole("button", { name: "重试上传" }).click();
  await expect(queue.getByText("已完成", { exact: true })).toBeVisible();
  const uploaded = await context.request.get(
    app.url +
      "/api/files/download?session=" +
      session.id +
      "&path=" +
      encodeURIComponent(remoteDir + "/acceptance.txt"),
  );
  expect(uploaded.ok()).toBe(true);
  expect(await uploaded.text()).toBe("verified SFTP contents\n");
  await queue.getByRole("button", { name: "收起上传队列" }).click();
  await page.route(
    "**/api/files/upload?**",
    (route) =>
      route.fulfill({
        status: 502,
        contentType: "application/json",
        body: JSON.stringify({ error: "Injected connection loss" }),
      }),
    { times: 1 },
  );
  await page.locator("input[type=file]").setInputFiles({
    name: "retry.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("retry contents"),
  });
  await expect(queue.getByText("上传失败", { exact: true })).toBeVisible();
  await queue.getByRole("button", { name: "收起上传队列" }).click();
  await page.getByRole("button", { name: "文件", exact: true }).click();
  await page.getByRole("button", { name: "上传", exact: true }).click();
  await queue.getByRole("button", { name: "重试上传" }).click();
  await expect(queue.getByText("已完成", { exact: true })).toHaveCount(2);
  const retried = await context.request.get(
    app.url +
      "/api/files/download?session=" +
      session.id +
      "&path=" +
      encodeURIComponent(remoteDir + "/retry.txt"),
  );
  expect(retried.ok()).toBe(true);
  expect(await retried.text()).toBe("retry contents");
  await page.screenshot({
    animations: "disabled",
    path: info.outputPath("upload-queue.png"),
    fullPage: true,
  });
  const remainingPrompts: string[] = [];
  const rejectUnexpectedPrompt = async (
    dialog: import("@playwright/test").Dialog,
  ) => {
    remainingPrompts.push(dialog.type());
    await dialog.dismiss();
  };
  page.on("dialog", rejectUnexpectedPrompt);
  await page.reload();
  expect(remainingPrompts).toEqual([]);
  page.off("dialog", rejectUnexpectedPrompt);
  await page.getByRole("button", { name: "上传", exact: true }).click();
  await expect(queue).toContainText("在文件面板选择或拖入文件");
});

test("recovery UI guards endpoint changes and separates terminate from forgetting", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(info.project.name !== "desktop", "Desktop recovery management");
  await loginAPI(context.request, app);
  const host = await createHost(context.request, app, "Recovery entry");
  const { DatabaseSync } = await import("node:sqlite");
  const db = new DatabaseSync(app.db);
  const id = "abcdef0123456789abcdef0123456789";
  try {
    db.exec("PRAGMA busy_timeout=5000");
    db.prepare(
      "INSERT INTO terminal_recovery(id,node_id,label,target,route_key,created_at) VALUES(?,?,?,?,?,?)",
    ).run(
      id,
      host.id,
      host.name,
      "test@original-host:22",
      "different-original-route",
      Date.now(),
    );
  } finally {
    db.close();
  }
  await page.goto(app.url);
  await page.getByRole("button", { name: "会话库", exact: true }).click();
  const card = page.getByRole("article", { name: "保活终端 Recovery entry" });
  await expect(card).toContainText("待接入");
  await expect(card).toBeInViewport();
  await card.getByRole("button", { name: "恢复终端", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "主机地址、用户或跳板路径已变化",
  );
  await page.getByRole("button", { name: "关闭错误提示" }).click();
  let terminationRequests = 0;
  page.on("request", (req) => {
    if (req.url().includes("/terminate")) terminationRequests++;
  });
  await card.getByRole("button", { name: "结束远端终端", exact: true }).click();
  const confirm = page.getByRole("alertdialog", { name: "结束远端终端" });
  await expect(confirm).toContainText("正在运行的任务可能中断");
  expect(terminationRequests).toBe(0);
  await confirm.getByRole("button", { name: "取消", exact: true }).click();
  await card.getByText("手动接入命令", { exact: true }).click();
  await page.screenshot({
    path: info.outputPath("recovery-shelf.png"),
    fullPage: true,
    animations: "disabled",
  });
  const other = await context.newPage();
  await other.goto(app.url);
  await other.getByRole("button", { name: "会话库", exact: true }).click();
  const otherCard = other.getByRole("article", {
    name: "保活终端 Recovery entry",
  });
  await expect(otherCard).toBeVisible();
  await card.getByRole("button", { name: "忘记恢复记录", exact: true }).click();
  const forget = page.getByRole("alertdialog", { name: "忘记恢复记录" });
  await expect(forget).toContainText("不停止远端任务");
  await forget
    .getByRole("button", { name: "确认忘记恢复记录", exact: true })
    .click();
  await expect(card).toHaveCount(0);
  await expect(otherCard).toHaveCount(0);
  await other.close();
  expect(
    (await (await context.request.get(app.url + "/api/recovery")).json())
      .length,
  ).toBe(0);
  expect(terminationRequests).toBe(0);
});

test("missing tmux gives an actionable error without leaving a recovery record", async ({
  page,
  context,
  app,
}, info) => {
  test.skip(
    info.project.name !== "desktop",
    "Desktop persistent connection setup",
  );
  await loginAPI(context.request, app);
  await page.goto(app.url);
  await page
    .getByRole("button", { name: "新建主机", exact: true })
    .first()
    .click();
  await page.getByLabel("名称（选填）").fill("Tmux unavailable");
  await page.getByLabel("主机地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("端口", { exact: true }).fill(String(app.sshPort));
  await page.getByLabel("登录用户").fill("test");
  await page.getByLabel("密码", { exact: true }).fill("secret");
  await page.getByRole("button", { name: /高级设置/ }).click();
  await page.getByLabel("终端保活与恢复（tmux）").check();
  await page.getByRole("button", { name: "保存并连接", exact: true }).click();
  const trust = page.getByRole("button", { name: "信任并连接", exact: true });
  await expect(page.getByRole("alert").or(trust)).toBeVisible();
  if (await trust.isVisible()) await trust.click();
  await expect(page.getByRole("alert")).toContainText("远端未找到 tmux");
  expect(
    (await (await context.request.get(app.url + "/api/recovery")).json())
      .length,
  ).toBe(0);
});
