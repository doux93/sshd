const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const ts = require("typescript");

function browser() {
  const sockets = [],
    timers = new Map(),
    storage = new Map();
  class Socket {
    static OPEN = 1;
    static CLOSED = 3;
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.sent = [];
      sockets.push(this);
    }
    open() {
      this.readyState = 1;
      this.onopen?.();
    }
    close() {
      this.readyState = 3;
      this.onclose?.();
    }
    send(data) {
      this.sent.push(data);
    }
    message(data) {
      this.onmessage?.({ data });
    }
  }
  let timerId = 0;
  const context = vm.createContext({
    console: { warn() {} },
    URL,
    Uint8Array,
    ArrayBuffer,
    Map,
    Set,
    WebSocket: Socket,
    window: {
      location: {
        protocol: "https:",
        host: "console.example",
        href: "https://console.example/",
        pathname: "/",
        search: "",
        assign() {},
      },
      history: { replaceState() {} },
    },
    sessionStorage: {
      getItem: (k) => storage.get(k) ?? null,
      setItem: (k, v) => storage.set(k, v),
      removeItem: (k) => storage.delete(k),
    },
    fetch: async () => ({
      ok: true,
      json: async () => ({
        mode: "remote",
        authenticated: true,
        locked: false,
      }),
    }),
    setTimeout: (fn) => {
      timers.set(++timerId, fn);
      return timerId;
    },
    clearTimeout: (id) => timers.delete(id),
  });
  const cache = new Map();
  function load(file) {
    file = path.resolve(__dirname, "..", file);
    if (cache.has(file)) return cache.get(file);
    const module = { exports: {} };
    cache.set(file, module.exports);
    const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2022,
      },
    }).outputText;
    const wrapper = vm.runInContext(
      `(function(require,module,exports){${compiled}\n})`,
      context,
    );
    wrapper(
      (name) => load(path.resolve(path.dirname(file), name + ".ts")),
      module,
      module.exports,
    );
    return module.exports;
  }
  return { load, sockets, timers, storage, context };
}

test("runtime auth works without an injected HTML script and clears stale token", async () => {
  const b = browser();
  b.storage.set("sshx.token", "old-local-token");
  const api = b.load("src/api/client.ts");
  await api.initAuth();
  assert.equal(api.getMode(), "remote");
  assert.equal(api.getToken(), "");
  assert.equal(b.storage.size, 0);
});
test("metrics object is delivered; malformed frames do not break subsequent messages", () => {
  const b = browser();
  const { WsClient } = b.load("src/ws/protocol.ts");
  const c = new WsClient();
  let got;
  c.onMetrics((sid, s) => (got = { sid, s }));
  c.connect();
  const ws = b.sockets[0];
  ws.open();
  ws.message("{broken");
  ws.message(
    JSON.stringify({
      type: "metrics",
      sessionId: "one",
      sample: { cpuUsage: 12 },
    }),
  );
  assert.equal(got.sid, "one");
  assert.equal(got.s.cpuUsage, 12);
});
test("reset precedes replay on reconnect and slow-subscriber reset reattaches", () => {
  const b = browser();
  const { WsClient } = b.load("src/ws/protocol.ts");
  const c = new WsClient();
  const seen = [];
  const id = c.attach("one", 80, 24, {
    onReset: () => seen.push("reset"),
    onData: () => seen.push("data"),
    onClosed: () => seen.push("closed"),
    onSize() {},
  });
  c.connect();
  const ws = b.sockets[0];
  ws.open();
  ws.message(JSON.stringify({ type: "attached", channelId: id }));
  ws.message(new Uint8Array([id, 65]).buffer);
  assert.deepEqual(seen, ["reset", "data"]);
  ws.message(JSON.stringify({ type: "channel-reset", channelId: id }));
  assert.equal(JSON.parse(ws.sent.at(-1)).type, "attach");
  assert.ok(!seen.includes("closed"));
});
test("disconnect cancels queued reconnect; repeated connect opens one socket", () => {
  const b = browser();
  const { WsClient } = b.load("src/ws/protocol.ts");
  const c = new WsClient();
  c.connect();
  c.connect();
  assert.equal(b.sockets.length, 1);
  b.sockets[0].close();
  assert.equal(b.timers.size, 1);
  c.disconnect();
  assert.equal(b.timers.size, 0);
});

test("read-only attachment blocks input and retains permission on reconnect", () => {
  const b = browser();
  const { WsClient } = b.load("src/ws/protocol.ts");
  const c = new WsClient();
  const id = c.attach(
    "one",
    80,
    24,
    { onReset() {}, onData() {}, onClosed() {}, onSize() {} },
    "passive",
  );
  c.connect();
  b.sockets[0].open();
  assert.equal(JSON.parse(b.sockets[0].sent[0]).readOnly, true);
  const count = b.sockets[0].sent.length;
  c.sendInput(id, new Uint8Array([65]));
  assert.equal(b.sockets[0].sent.length, count);
  b.sockets[0].close();
  [...b.timers.values()][0]();
  b.sockets[1].open();
  assert.equal(JSON.parse(b.sockets[1].sent[0]).readOnly, true);
});
