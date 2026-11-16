import {
  test as base,
  expect,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "node:http";
import { DatabaseSync } from "node:sqlite";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const suffix = process.platform === "win32" ? ".exe" : "";
const password = "e2e-only-master-password";
interface App {
  url: string;
  sshPort: number;
  db: string;
  modelURL: string;
  password: string;
}
function launch(
  binary: string,
  args: string[],
  pattern: RegExp,
): Promise<{ child: ChildProcess; match: RegExpMatchArray }> {
  const child = spawn(resolve(root, ".testbin", binary + suffix), args, {
    stdio: ["ignore", "pipe", "pipe"],
  });
  return new Promise((resolve, reject) => {
    let output = "";
    const timer = setTimeout(() => {
      child.kill();
      reject(Error("Fixture startup timed out: " + output));
    }, 20000);
    const read = (data: Buffer) => {
      output = (output + data.toString()).slice(-10000);
      const match = output.match(pattern);
      if (match) {
        clearTimeout(timer);
        resolve({ child, match });
      }
    };
    child.stdout!.on("data", read);
    child.stderr!.on("data", read);
    child.once("error", (e) => {
      clearTimeout(timer);
      reject(e);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(Error("Fixture exited " + code + ": " + output));
    });
  });
}
async function stop(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      resolve();
    }, 5000);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill("SIGTERM");
  });
}
export const test = base.extend<{}, { app: App }>({
  app: [
    async ({}, use) => {
      const dir = await mkdtemp(join(tmpdir(), "sshx-e2e-"));
      const processes: ChildProcess[] = [];
      const model = createServer((req, res) => {
        req.resume();
        res.setHeader("Content-Type", "application/json");
        res.end(
          JSON.stringify({
            choices: [
              {
                message: {
                  content: JSON.stringify({
                    command: "pwd",
                    explanation: "显示当前工作目录。",
                    impact: "只读取目录位置，不修改文件。",
                  }),
                },
              },
            ],
          }),
        );
      });
      try {
        await new Promise<void>((resolve) =>
          model.listen(0, "127.0.0.1", resolve),
        );
        const address = model.address();
        if (!address || typeof address === "string")
          throw Error("model address");
        const fake = await launch(
          "fakesshd",
          ["-addr", "127.0.0.1:0"],
          /fake sshd on 127\.0\.0\.1:(\d+)/,
        );
        processes.push(fake.child);
        const master = join(dir, "master");
        await writeFile(master, password, { mode: 0o600 });
        const db = join(dir, "sshx.db");
        const backend = await launch(
          "sshx",
          [
            "--mode",
            "remote",
            "--addr",
            "127.0.0.1:0",
            "--data",
            db,
            "--master-password-file",
            master,
            "--secure-cookies=false",
          ],
          /http:\/\/127\.0\.0\.1:\d+/,
        );
        processes.push(backend.child);
        await use({
          url: backend.match[0],
          sshPort: Number(fake.match[1]),
          db,
          modelURL: `http://127.0.0.1:${address.port}`,
          password,
        });
      } finally {
        await Promise.all(processes.map(stop));
        await new Promise<void>((resolve) => model.close(() => resolve()));
        await rm(dir, { recursive: true, force: true, maxRetries: 5 });
      }
    },
    { scope: "worker" },
  ],
});
export { expect };
export async function loginAPI(request: APIRequestContext, app: App) {
  const res = await request.post(app.url + "/api/auth/login", {
    data: { password: app.password },
  });
  expect(res.status()).toBe(204);
}
export async function createHost(
  request: APIRequestContext,
  app: App,
  name: string,
) {
  const res = await request.post(app.url + "/api/nodes", {
    data: {
      parentId: "",
      kind: "connection",
      name,
      config: {
        host: "127.0.0.1",
        port: app.sshPort,
        user: "test",
        authType: "password",
      },
      password: "secret",
    },
  });
  expect(res.status()).toBe(201);
  return await res.json();
}
export async function connectHost(
  request: APIRequestContext,
  app: App,
  id: string,
) {
  let res = await request.post(app.url + `/api/nodes/${id}/connect`, {
    data: {},
  });
  if (res.status() === 409) {
    const body = await res.json();
    res = await request.post(app.url + `/api/nodes/${id}/connect`, {
      data: { accepts: { [body.hostKey.host]: body.hostKey.fingerprint } },
    });
  }
  expect(res.status()).toBe(201);
  return await res.json();
}
export function seedAlert(app: App, nodeId: string, label: string) {
  const db = new DatabaseSync(app.db);
  try {
    db.exec("PRAGMA busy_timeout=5000");
    db.prepare(
      "INSERT INTO alert_history(node_id,node_label,level,message,ts) VALUES(?,?,?,?,?)",
    ).run(nodeId, label, "warning", "E2E CPU warning", Date.now());
  } finally {
    db.close();
  }
}
export async function watchSockets(page: Page) {
  await page.addInitScript(() => {
    const Native = window.WebSocket;
    const state = { sockets: [] as WebSocket[], binary: 0, received: "" };
    (window as any).__sshxTest = state;
    window.WebSocket = class extends Native {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        state.sockets.push(this);
        this.addEventListener("message", (event) => {
          if (event.data instanceof ArrayBuffer) {
            state.received += new TextDecoder().decode(
              new Uint8Array(event.data).subarray(1),
            );
          }
        });
      }
      send(data: string | ArrayBufferLike | Blob | ArrayBufferView) {
        if (typeof data !== "string") state.binary++;
        super.send(data);
      }
    };
  });
}
export async function assertFits(page: Page) {
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
}
