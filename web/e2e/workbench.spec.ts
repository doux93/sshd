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
  await page.keyboard.press("Control+k");
  await page.getByRole("textbox", { name: "命令需求" }).fill("显示当前目录");
  await page.getByRole("button", { name: "生成", exact: true }).click();
  await expect(
    page.getByText("只读取目录位置，不修改文件。", { exact: true }),
  ).toBeVisible();
  expect(
    await page.getByRole("textbox", { name: "生成的命令" }).inputValue(),
  ).toBe("pwd");
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await page.getByRole("button", { name: "复制命令", exact: true }).click();
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await page.getByRole("button", { name: "关闭 AI 助手" }).click();
  const separator = page.getByRole("separator", { name: "主机栏宽度" });
  await separator.focus();
  await page.keyboard.press("ArrowRight");
  const size = await separator.getAttribute("aria-valuenow");
  await page.reload();
  await expect(separator).toHaveAttribute("aria-valuenow", size!);
  await assertFits(page);
  await page.screenshot({
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
    .toContain("/root $");
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  await info.attach("terminal-layout", {
    body: JSON.stringify(
      await page.locator(".xterm").evaluate((el) => ({
        html: el.outerHTML,
        rect: el.getBoundingClientRect().toJSON(),
        children: Array.from(
          el.querySelectorAll("canvas,.xterm-screen,.xterm-viewport"),
        ).map((n) => ({
          tag: n.tagName,
          rect: n.getBoundingClientRect().toJSON(),
        })),
      })),
    ),
    contentType: "application/json",
  });
  await page.locator(".xterm-screen").tap();
  const before = await page.evaluate(() => (window as any).__sshxTest.binary);
  await page.keyboard.type("do-not-send");
  await page.keyboard.press("Enter");
  expect(await page.evaluate(() => (window as any).__sshxTest.binary)).toBe(
    before,
  );
  await assertFits(page);
  await page.screenshot({
    path: info.outputPath("mobile-readonly.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "返回", exact: true }).tap();
  await expect(
    page.getByText("只读 · 不发送输入", { exact: true }),
  ).toHaveCount(0);
});
